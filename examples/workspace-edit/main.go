// Command workspace-edit runs a real Host-tool plus private-workspace acceptance.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

const program = `from pathlib import Path
import json

quote = web.fetch_json(symbol="AAPL")
record = catalog.query_one(symbol="AAPL")
root = Path("/workspace")
source = root.joinpath("src/pricing.py")
source.write_text(source.read_text().replace("return 0", f"return {quote['price']}"))
config_path = root.joinpath("config.json")
config = json.loads(config_path.read_text())
config.update({"enabled": True, "last_price": quote["price"]})
config_path.write_text(json.dumps(config, sort_keys=True) + "\n")
root.joinpath("REPORT.md").write_text(f"# AAPL\n\nPrice: {quote['price']}\n\n{record['note']}\n")
effect = audit.record(request_id="workspace-edit-v1", message=f"AAPL:{quote['price']}")
result = {"price": quote["price"], "note": record["note"], "audit": effect["status"]}
`

type acceptanceResult struct {
	Value          json.RawMessage             `json:"value"`
	Before         workspacepkg.Revision       `json:"before"`
	After          workspacepkg.Revision       `json:"after"`
	Changes        workspacepkg.ChangeSummary  `json:"changes"`
	Export         workspacepkg.ChangeSet      `json:"export"`
	Target         workspacepkg.ConflictReport `json:"target"`
	ExternalWrites int                         `json:"external_writes"`
}

func main() {
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest artifact")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := executeAcceptance(ctx, *guest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func executeAcceptance(ctx context.Context, guestPath string) (acceptanceResult, error) {
	var result acceptanceResult
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		return result, err
	}
	temporary, err := os.MkdirTemp("", "pysolate-workspace-edit-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(temporary)

	source := filepath.Join(temporary, "source")
	if err := createFixture(source); err != nil {
		return result, err
	}
	originalSource, err := os.ReadFile(filepath.Join(source, "src", "pricing.py"))
	if err != nil {
		return result, err
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/prices/AAPL" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"price":123}`))
	}))
	defer server.Close()

	database, err := openCatalog(filepath.Join(temporary, "catalog.db"))
	if err != nil {
		return result, err
	}
	defer database.Close()

	audit := &idempotentAudit{records: make(map[string]string)}
	manifest := pysolate.Manifest{
		"http.local/fetch-json": {
			PythonPath:  "web.fetch_json",
			Description: "Fetch one allowlisted market quote from a Host-owned HTTP client",
			InputSchema: json.RawMessage(`{"type":"object","required":["symbol"],"properties":{"symbol":{"type":"string"}}}`),
			Call:        fetchTool(server.Client(), server.URL),
		},
		"sqlite.catalog/query-one": {
			PythonPath:  "catalog.query_one",
			Description: "Run one fixed parameterized read-only catalog query",
			InputSchema: json.RawMessage(`{"type":"object","required":["symbol"],"properties":{"symbol":{"type":"string"}}}`),
			Call:        queryTool(database),
		},
		"audit/record": {
			PythonPath:  "audit.record",
			Description: "Record one idempotent external effect",
			InputSchema: json.RawMessage(`{"type":"object","required":["request_id","message"]}`),
			Call:        audit.call,
		},
	}

	base := filepath.Join(temporary, "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		return result, err
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		return result, err
	}
	ref, err := manager.CreateFromDirectory(source, workspacepkg.DefaultLimits())
	if err != nil {
		_ = manager.Close()
		return result, err
	}
	lease, err := manager.Acquire(ref, "workspace-edit-acceptance")
	if err != nil {
		_ = manager.Close()
		return result, err
	}
	defer func() {
		_ = lease.Release()
		_ = manager.Close()
	}()

	before, err := lease.Snapshot()
	if err != nil {
		return result, err
	}
	result.Before = before.Revision
	runner, err := pysolate.New(ctx, wasm, manifest)
	if err != nil {
		return result, err
	}
	defer runner.Close(context.Background())
	output, err := runner.RunWorkspace(ctx, program, nil, lease)
	if err != nil {
		return result, err
	}
	after, err := lease.Snapshot()
	if err != nil {
		return result, err
	}
	result.After = after.Revision
	result.Changes = workspacepkg.Diff(before, after)
	result.Export, err = lease.ExportChanges(before, 1<<20)
	if err != nil {
		return result, err
	}
	result.Target, err = workspacepkg.CheckDirectoryConflicts(source, result.Export, workspacepkg.DefaultLimits())
	if err != nil {
		return result, err
	}
	result.Value = append(json.RawMessage(nil), output.Value...)
	result.ExternalWrites = audit.count()

	if err := verifyAcceptance(source, originalSource, lease, result); err != nil {
		return result, err
	}
	return result, nil
}

func createFixture(root string) error {
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o700); err != nil {
		return err
	}
	files := map[string][]byte{
		"src/pricing.py":  []byte("def current_price():\n    return 0\n"),
		"config.json":     []byte("{\"enabled\":false}\n"),
		"README.md":       []byte("workspace edit fixture\n"),
		"assets/logo.bin": {0x00, 0x01, 0xfe, 0xff},
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func openCatalog(path string) (*sql.DB, error) {
	location := (&url.URL{Scheme: "file", Path: path}).String()
	seed, err := sql.Open("sqlite3", location)
	if err != nil {
		return nil, err
	}
	if _, err := seed.Exec(`CREATE TABLE catalog(symbol TEXT PRIMARY KEY, note TEXT NOT NULL); INSERT INTO catalog VALUES ('AAPL', 'Approved local catalog record')`); err != nil {
		_ = seed.Close()
		return nil, err
	}
	if err := seed.Close(); err != nil {
		return nil, err
	}
	readOnly := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	database, err := sql.Open("sqlite3", readOnly)
	if err != nil {
		return nil, err
	}
	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func fetchTool(client *http.Client, base string) pysolate.Tool {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var arguments struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, err
		}
		if arguments.Symbol != "AAPL" {
			return nil, errors.New("symbol is not allowlisted")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/prices/AAPL", nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("quote status %d", response.StatusCode)
		}
		limited := io.LimitReader(response.Body, 4097)
		body, err := io.ReadAll(limited)
		if err != nil {
			return nil, err
		}
		if len(body) > 4096 {
			return nil, errors.New("quote response exceeds 4096 bytes")
		}
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			return nil, err
		}
		return value, nil
	}
}

func queryTool(database *sql.DB) pysolate.Tool {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var arguments struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, err
		}
		var note string
		if err := database.QueryRowContext(ctx, `SELECT note FROM catalog WHERE symbol = ?`, arguments.Symbol).Scan(&note); err != nil {
			return nil, err
		}
		return map[string]any{"symbol": arguments.Symbol, "note": note}, nil
	}
}

type idempotentAudit struct {
	mu      sync.Mutex
	records map[string]string
}

func (audit *idempotentAudit) call(_ context.Context, raw json.RawMessage) (any, error) {
	var arguments struct {
		RequestID string `json:"request_id"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, err
	}
	if arguments.RequestID == "" || arguments.Message == "" {
		return nil, errors.New("request_id and message are required")
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if previous, exists := audit.records[arguments.RequestID]; exists {
		if previous != arguments.Message {
			return nil, errors.New("idempotency key reused with different message")
		}
		return map[string]any{"status": "duplicate"}, nil
	}
	audit.records[arguments.RequestID] = arguments.Message
	return map[string]any{"status": "created"}, nil
}

func (audit *idempotentAudit) count() int {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	return len(audit.records)
}

func verifyAcceptance(source string, original []byte, lease *workspacepkg.Lease, result acceptanceResult) error {
	unchanged, err := os.ReadFile(filepath.Join(source, "src", "pricing.py"))
	if err != nil || string(unchanged) != string(original) {
		return errors.New("source fixture was modified")
	}
	files, err := lease.Files()
	if err != nil {
		return err
	}
	contents := make(map[string][]byte, len(files))
	for _, file := range files {
		contents[file.Path] = file.Data
	}
	if string(contents["src/pricing.py"]) != "def current_price():\n    return 123\n" {
		return errors.New("Python source was not edited")
	}
	if string(contents["config.json"]) != "{\"enabled\": true, \"last_price\": 123}\n" {
		return errors.New("JSON configuration was not updated")
	}
	if string(contents["REPORT.md"]) != "# AAPL\n\nPrice: 123\n\nApproved local catalog record\n" {
		return errors.New("report was not generated")
	}
	if string(contents["assets/logo.bin"]) != string([]byte{0x00, 0x01, 0xfe, 0xff}) {
		return errors.New("binary fixture changed")
	}
	if result.Changes.Added != 1 || result.Changes.Modified != 2 || result.Changes.Deleted != 0 ||
		len(result.Export.Changes) != 3 || result.Export.Before != result.Before || result.Export.After != result.After ||
		!result.Target.Clean || len(result.Target.Conflicts) != 0 || result.ExternalWrites != 1 {
		return fmt.Errorf("unexpected acceptance summary: %#v", result)
	}
	return nil
}
