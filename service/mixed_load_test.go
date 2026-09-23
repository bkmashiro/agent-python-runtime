package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

// This is a bounded lifecycle regression, not a throughput or capacity claim.
func TestMixedLoadReleasesResources(t *testing.T) {
	cycles := 8
	if raw := os.Getenv("PYSOLATE_MIXED_CYCLES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 128 {
			t.Fatal("mixed cycles must be 1..128")
		}
		cycles = n
	}
	modes := []bool{false}
	if runtime.GOOS == "linux" {
		modes = append(modes, true)
	}
	for _, optimized := range modes {
		t.Run(fmt.Sprint(optimized), func(t *testing.T) {
			guest := os.Getenv("PYSOLATE_GUEST")
			if guest == "" {
				guest = filepath.Join("..", "dist", "pysolate.wasm")
			}
			wasm, err := os.ReadFile(guest)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			manager, err := workspacepkg.NewManager(root)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var current chan struct{}
			started := make(chan struct{}, 1)
			manifest := pysolate.Manifest{"slow_read": {Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
				mu.Lock()
				gate := current
				mu.Unlock()
				started <- struct{}{}
				select {
				case <-gate:
					return "done", nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}}
			server, err := New(context.Background(), wasm, manifest, manager, 2, Options{COWDataImage: optimized, MaxRunDuration: 10 * time.Second})
			if err != nil {
				manager.Close()
				t.Fatal(err)
			}
			defer server.Close(context.Background())
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			client := httpServer.Client()
			client.Timeout = 15 * time.Second
			baselineFiles, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			initialFDs := -1
			for cycle := 0; cycle < cycles; cycle++ {
				func() {
					gate := make(chan struct{})
					release := sync.OnceFunc(func() { close(gate) })
					defer release()
					mu.Lock()
					current = gate
					mu.Unlock()
					done := make(chan mixedReply, 1)
					go func() {
						done <- mixedRequest(client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{"source": "result=slow_read()"})
					}()
					select {
					case <-started:
					case <-time.After(15 * time.Second):
						t.Fatal("slow read did not start")
					}
					request := func(method, path string, body any, want int) map[string]any {
						t.Helper()
						r := mixedRequest(client, method, httpServer.URL+path, body)
						if r.err != nil || r.status != want {
							t.Fatalf("cycle=%d %s: status=%d body=%v err=%v", cycle, path, r.status, r.body, r.err)
						}
						return r.body
					}
					if r := request("POST", "/v1/run", map[string]any{"source": "result=sum(range(100))"}, 200); r["value"] != float64(4950) {
						t.Fatal(r)
					}
					request("POST", "/v1/run", map[string]any{"source": "raise ValueError('expected failure')"}, 422)
					request("POST", "/v1/run", map[string]any{"source": "while True: pass", "timeout_ms": 50}, 408)
					created := request("POST", "/v1/workspaces", map[string]any{"files": []map[string]any{{"path": "value.txt", "data": []byte("before")}}}, 201)
					ref, ok := created["workspace"].(string)
					if !ok {
						t.Fatal(created)
					}
					path := "/v1/workspaces/" + ref
					request("POST", path+"/run", map[string]any{"source": "open('value.txt','a').write('-after')\nresult=1"}, 200)
					if r := request("POST", path+"/run", map[string]any{"source": "result=open('value.txt').read()"}, 200); r["value"] != "before-after" {
						t.Fatal(r)
					}
					request("DELETE", path, nil, 200)
					if len(server.slots) != 1 {
						t.Fatalf("expected only held slow read, active=%d", len(server.slots))
					}
					release()
					select {
					case r := <-done:
						if r.err != nil || r.status != 200 || r.body["value"] != "done" {
							t.Fatalf("slow read: %+v", r)
						}
					case <-time.After(15 * time.Second):
						t.Fatal("slow read did not finish")
					}
					if len(server.slots) != 0 {
						t.Fatal("execution slot leaked")
					}
					server.mu.Lock()
					leases := len(server.leases)
					server.mu.Unlock()
					if leases != 0 {
						t.Fatalf("lease count=%d", leases)
					}
					files, err := os.ReadDir(root)
					if err != nil || len(files) != len(baselineFiles) {
						t.Fatalf("workspace files leaked: %d %v", len(files), err)
					}
					snapshot := mixedSnapshot()
					if cycle == 0 {
						initialFDs = snapshot["fds"]
					}
					if initialFDs >= 0 && snapshot["fds"] > initialFDs+2 {
						t.Fatalf("descriptor growth: first=%d now=%v", initialFDs, snapshot)
					}
					if maps, err := os.ReadFile("/proc/self/maps"); err == nil && bytes.Contains(maps, []byte("memfd:pysolate-spine-cow")) {
						t.Fatal("Guest COW mapping retained between cycles")
					}
					snapshot["cycle"] = cycle
					snapshot["leases"] = leases
					snapshot["active"] = len(server.slots)
					data, _ := json.Marshal(snapshot)
					t.Log(string(data))
				}()
			}
			client.CloseIdleConnections()
			if err := server.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS == "linux" {
				fds, _ := os.ReadDir("/proc/self/fd")
				for _, fd := range fds {
					target, _ := os.Readlink("/proc/self/fd/" + fd.Name())
					if strings.Contains(target, "memfd:pysolate-spine-cow") {
						t.Fatal("image descriptor survived Close")
					}
				}
			}
		})
	}
}

type mixedReply struct {
	status int
	body   map[string]any
	err    error
}

func mixedRequest(client *http.Client, method, url string, value any) mixedReply {
	var data []byte
	if value != nil {
		data, _ = json.Marshal(value)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(data))
	if err != nil {
		return mixedReply{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return mixedReply{err: err}
	}
	defer res.Body.Close()
	var body map[string]any
	err = json.NewDecoder(res.Body).Decode(&body)
	return mixedReply{res.StatusCode, body, err}
}
func mixedSnapshot() map[string]int {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	result := map[string]int{"heap_alloc_kib": int(m.HeapAlloc / 1024), "gc_cycles": int(m.NumGC), "pss_kib": -1, "fds": -1}
	if fds, err := os.ReadDir("/proc/self/fd"); err == nil {
		result["fds"] = len(fds)
	}
	if data, err := os.ReadFile("/proc/self/smaps_rollup"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "Pss:" {
				result["pss_kib"], _ = strconv.Atoi(fields[1])
			}
		}
	}
	return result
}
