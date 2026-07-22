package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

func TestOperatorCLIWithRealGuestArtifact(t *testing.T) {
	artifact := guestArtifact(t)
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "apyrun")
	build := exec.Command("go", "build", "-o", binary, "./cmd/apyrun")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build apyrun: %v\n%s", err, output)
	}

	run := func(t *testing.T, config string, request string) (int, []byte, []byte) {
		t.Helper()
		args := []string{"-artifact", artifact}
		if config != "" {
			path := filepath.Join(t.TempDir(), "operator.json")
			if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "-config", path)
		}
		command := exec.Command(binary, args...)
		command.Stdin = strings.NewReader(request)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		if err == nil {
			return 0, stdout.Bytes(), stderr.Bytes()
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), stdout.Bytes(), stderr.Bytes()
		}
		t.Fatalf("run apyrun: %v", err)
		return -1, nil, nil
	}

	t.Run("no grant", func(t *testing.T) {
		exit, stdout, stderr := run(t, `{"prepared_capacity":1}`, `{"run_id":"guest-label","code":"result = inputs['value'] + 1","inputs":{"value":41}}`)
		if exit != 0 || len(stderr) != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr)
		}
		var response guestResponse
		if err := json.Unmarshal(stdout, &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != "ok" || response.Result.(float64) != 42 || len(response.Receipts) != 0 {
			t.Fatalf("unexpected response: %#v", response)
		}
	})

	t.Run("Host granted fetch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "Bearer fixture-owned" {
				http.Error(writer, "missing Host credential", http.StatusUnauthorized)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"value":42}`))
		}))
		defer server.Close()
		config, err := json.Marshal(map[string]any{
			"fetch_many": map[string]any{
				"max_calls": 1, "max_requests_per_call": 1, "max_total_requests": 1,
				"max_concurrency": 1, "max_response_bytes": 1024, "per_request_timeout_ms": 1000,
				"targets": map[string]any{
					"fixture": map[string]any{
						"base_url": server.URL,
						"headers":  map[string]string{"Authorization": "Bearer fixture-owned"},
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		request := `{"run_id":"guest-cannot-author-receipt-id","code":"from agent_runtime import tools\nimport json\nitems = tools.fetch_many([{'request_id':'one','target':'fixture','path':'/value'}])\nresult = json.loads(items[0]['body'])['value']","inputs":{}}`
		exit, stdout, stderr := run(t, string(config), request)
		if exit != 0 || len(stderr) != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr)
		}
		var response guestResponse
		if err := json.Unmarshal(stdout, &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != "ok" || response.Result.(float64) != 42 || len(response.Receipts) != 1 {
			t.Fatalf("unexpected response: %#v", response)
		}
		if !strings.HasPrefix(response.Receipts[0].RunID, "host-") || response.Receipts[0].RunID == "guest-cannot-author-receipt-id" {
			t.Fatalf("receipt did not use Host identity: %#v", response.Receipts[0])
		}
	})

	t.Run("timeout", func(t *testing.T) {
		exit, stdout, stderr := run(t, `{"timeout_ms":50}`, `{"run_id":"timeout","code":"while True:\n    pass","inputs":{}}`)
		if exit == 0 || len(stdout) != 0 || !strings.Contains(string(stderr), "execute guest") {
			t.Fatalf("timeout did not fail closed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
		}
	})
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
