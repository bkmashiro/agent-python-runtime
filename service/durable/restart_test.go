package durableservice

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

func TestDurableServiceRestartHelper(t *testing.T) {
	directory := os.Getenv("PYSOLATE_DURABLE_SERVICE_CHILD")
	if directory == "" {
		return
	}
	hold := os.Getenv("PYSOLATE_DURABLE_SERVICE_HOLD") == "1"
	ctx := context.Background()
	store, err := durable.Open(filepath.Join(directory, "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var preparation []durable.Preparation
	if os.Getenv("PYSOLATE_TEST_COW_DATA_IMAGE") == "1" {
		preparation = []durable.Preparation{{Seed: "restart-seed", COW: true, COWDataImage: true}}
	}
	runner, err := durable.NewRunner(ctx, store, restartGuest(t), "service-restart-v1", restartTools(t, directory, hold), preparation...)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(ctx)
	server, err := New(runner, "service-restart-v1", durable.Limits{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(ctx)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("ready:http://%s\n", listener.Addr())
	if err := http.Serve(listener, server); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPServiceRecoversAfterRealProcessKill(t *testing.T) {
	t.Run("default", testHTTPServiceRecovery)
	if runtime.GOOS == "linux" {
		t.Run("cow-data-image", func(t *testing.T) { t.Setenv("PYSOLATE_TEST_COW_DATA_IMAGE", "1"); testHTTPServiceRecovery(t) })
	}
}

func testHTTPServiceRecovery(t *testing.T) {
	directory := t.TempDir()
	first := startDurableServiceChild(t, directory, true)
	defer first.stop()

	status, created := durableRequest(t, http.DefaultClient, http.MethodPost, first.url+"/v1/durable/runs", map[string]any{
		"id": "restart-run", "source": "result = ledger.write(value=inputs['value'])", "inputs": map[string]any{"value": 7}, "seed": "restart-seed",
	})
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%#v stderr=%s", status, created, &first.stderr)
	}
	attemptDone := make(chan error, 1)
	go func() {
		body := bytes.NewBufferString("{}")
		request, err := http.NewRequest(http.MethodPost, first.url+"/v1/durable/runs/restart-run/attempts", body)
		if err == nil {
			var response *http.Response
			response, err = http.DefaultClient.Do(request)
			if response != nil {
				response.Body.Close()
			}
		}
		attemptDone <- err
	}()
	if line := first.nextLine(t); line != "effect-committed" {
		t.Fatalf("unexpected child line %q stderr=%s", line, &first.stderr)
	}
	first.kill(t)
	select {
	case <-attemptDone:
	case <-time.After(10 * time.Second):
		t.Fatal("killed attempt request did not return")
	}

	second := startDurableServiceChild(t, directory, false)
	defer second.stop()
	status, attempt := durableRequest(t, http.DefaultClient, http.MethodPost, second.url+"/v1/durable/runs/restart-run/attempts", map[string]any{})
	value, _ := attempt["value"].(map[string]any)
	if status != http.StatusOK || attempt["state"] != string(durable.StateCompleted) || value["value"] != float64(7) {
		t.Fatalf("resume status=%d body=%#v stderr=%s", status, attempt, &second.stderr)
	}
	status, run := durableRequest(t, http.DefaultClient, http.MethodGet, second.url+"/v1/durable/runs/restart-run", nil)
	if status != http.StatusOK || run["status"] != durable.StatusCompleted {
		t.Fatalf("get status=%d body=%#v", status, run)
	}

	status, history := durableRequest(t, http.DefaultClient, http.MethodGet, second.url+"/v1/durable/runs/restart-run/history?limit=1", nil)
	calls, ok := history["calls"].([]any)
	if status != http.StatusOK || !ok || len(calls) != 1 {
		t.Fatalf("history status=%d body=%#v", status, history)
	}
	encoded, _ := json.Marshal(history)
	for _, forbidden := range []string{`"arguments"`, `"outcome"`, `"operation_key"`, `"call_id"`} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("history leaked %s: %s", forbidden, encoded)
		}
	}
	call, ok := calls[0].(map[string]any)
	if !ok || call["state"] != durable.CallCompleted {
		t.Fatalf("unexpected history call: %#v", calls[0])
	}
	db := restartProviderDB(t, directory)
	var requests, effects int
	if err := db.QueryRow("SELECT (SELECT count(*) FROM requests),(SELECT count(*) FROM effects)").Scan(&requests, &effects); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || effects != 1 {
		t.Fatalf("requests=%d effects=%d", requests, effects)
	}
}

type durableServiceChild struct {
	command *exec.Cmd
	url     string
	lines   <-chan string
	stderr  bytes.Buffer
	waited  bool
}

func startDurableServiceChild(t *testing.T, directory string, hold bool) *durableServiceChild {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestDurableServiceRestartHelper$")
	command.Env = append(os.Environ(), "PYSOLATE_DURABLE_SERVICE_CHILD="+directory)
	if hold {
		command.Env = append(command.Env, "PYSOLATE_DURABLE_SERVICE_HOLD=1")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child := &durableServiceChild{command: command}
	command.Stderr = &child.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 8)
	child.lines = lines
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	ready := child.nextLine(t)
	if !strings.HasPrefix(ready, "ready:http://") {
		child.kill(t)
		t.Fatalf("unexpected ready line %q stderr=%s", ready, &child.stderr)
	}
	child.url = strings.TrimPrefix(ready, "ready:")
	return child
}

func (child *durableServiceChild) nextLine(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-child.lines:
		if !ok {
			return "EOF"
		}
		return line
	case <-time.After(time.Minute):
		child.kill(t)
		t.Fatalf("child output timeout: %s", &child.stderr)
		return ""
	}
}

func (child *durableServiceChild) kill(t *testing.T) {
	t.Helper()
	if child == nil || child.command == nil || child.command.Process == nil || child.waited {
		return
	}
	if err := child.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.command.Wait()
	child.waited = true
}

func (child *durableServiceChild) stop() {
	if child == nil || child.command == nil || child.command.Process == nil || child.waited {
		return
	}
	_ = child.command.Process.Kill()
	_ = child.command.Wait()
	child.waited = true
}

func restartGuest(t *testing.T) []byte {
	t.Helper()
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = filepath.Join("..", "..", "dist", "pysolate.wasm")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return wasm
}

func restartProviderDB(t *testing.T, directory string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(directory, "provider.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=synchronous(full)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS requests(id INTEGER PRIMARY KEY); CREATE TABLE IF NOT EXISTS effects(id TEXT PRIMARY KEY, args BLOB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func restartTools(t *testing.T, directory string, hold bool) []durable.Tool {
	t.Helper()
	db := restartProviderDB(t, directory)
	return []durable.Tool{{
		Name: "ledger.write", Version: "v1", Recovery: durable.Idempotent,
		Call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			key, ok := durable.OperationKey(ctx)
			if !ok {
				return nil, fmt.Errorf("missing operation key")
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(ctx, "INSERT INTO requests DEFAULT VALUES"); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO effects(id,args) VALUES (?,?)", key, []byte(arguments)); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			if hold {
				fmt.Println("effect-committed")
				select {}
			}
			var saved []byte
			if err := db.QueryRowContext(ctx, "SELECT args FROM effects WHERE id=?", key).Scan(&saved); err != nil {
				return nil, err
			}
			return json.RawMessage(saved), nil
		},
	}}
}
