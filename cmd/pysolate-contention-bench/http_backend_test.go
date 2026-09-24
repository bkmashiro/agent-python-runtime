package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
)

type httpFixtureState struct{ Active, Completed, Cancelled int }

// A separate OS process owns the HTTP handler. The tests never infer its state
// from the client's context or from a returned Guest call.
func TestHTTPFixtureProcess(t *testing.T) {
	if os.Getenv("PYSOLATE_HTTP_FIXTURE") != "1" {
		t.Skip("subprocess helper")
	}
	f, err := os.OpenFile(os.Getenv("PYSOLATE_HTTP_SERVER_TRACE"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	trace := newTrace(f, nil)
	trace.emit(map[string]any{"kind": "server_header", "pid": os.Getpid()})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, release, settled, stop := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	state := httpFixtureState{}
	mux := http.NewServeMux()
	mux.HandleFunc("/read", func(w http.ResponseWriter, r *http.Request) {
		args, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if e != nil {
			t.Error(e)
			return
		}
		trace.emit(map[string]any{"kind": "server_started", "args": json.RawMessage(args), "ignore_cancel": os.Getenv("PYSOLATE_HTTP_IGNORE_CANCEL") == "1"})
		mu.Lock()
		state.Active++
		mu.Unlock()
		close(started)
		completed := false
		if os.Getenv("PYSOLATE_HTTP_IGNORE_CANCEL") == "1" {
			<-release
			completed = true
		} else {
			select {
			case <-release:
				completed = true
			case <-r.Context().Done():
			}
		}
		mu.Lock()
		state.Active--
		if completed {
			state.Completed++
		} else {
			state.Cancelled++
		}
		mu.Unlock()
		if completed {
			body := []byte(`{"value":42}`)
			_, writeErr := w.Write(body)
			text := ""
			if writeErr != nil {
				text = writeErr.Error()
			}
			trace.emit(map[string]any{"kind": "server_completed", "body": body, "write_error": text, "client_context_cancelled": r.Context().Err() != nil})
		} else {
			trace.emit(map[string]any{"kind": "server_cancelled"})
		}
		close(settled)
	})
	writeState := func(w http.ResponseWriter) { mu.Lock(); defer mu.Unlock(); _ = json.NewEncoder(w).Encode(state) }
	mux.HandleFunc("/started", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
			writeState(w)
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/settled", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-settled:
			writeState(w)
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) { writeState(w) })
	mux.HandleFunc("/release", func(w http.ResponseWriter, _ *http.Request) { close(release); w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/stop", func(w http.ResponseWriter, _ *http.Request) { close(stop); w.WriteHeader(http.StatusNoContent) })
	server := &http.Server{Handler: mux, ErrorLog: log.New(io.Discard, "", 0)}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"url": "http://" + listener.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	<-stop
	if err := server.Close(); err != nil {
		t.Error(err)
	}
	if err := <-serveDone; err != http.ErrServerClosed {
		t.Error(err)
	}
	mu.Lock()
	finalState := state
	mu.Unlock()
	trace.emit(map[string]any{"kind": "server_end", "state": finalState})
	if err := trace.failure(); err != nil {
		t.Error(err)
	}
	if err := f.Sync(); err != nil {
		t.Error(err)
	}
}

func TestHTTPBackendCancellationBoundary(t *testing.T) {
	if os.Getenv("PYSOLATE_HTTP_ACCEPTANCE") != "1" {
		t.Skip("set PYSOLATE_HTTP_ACCEPTANCE=1 for independent HTTP process acceptance")
	}
	wasm, err := os.ReadFile(os.Getenv("PYSOLATE_GUEST"))
	if err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("PYSOLATE_HTTP_RECORD_DIR")
	if root == "" {
		root = t.TempDir()
	} else if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, ignore := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			name := fmt.Sprintf("ignore-%t-deadline-%t", ignore, deadline)
			t.Run(name, func(t *testing.T) {
				dir := filepath.Join(root, name)
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(filepath.Join(dir, "client.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				trace := newTrace(f, nil)
				trace.emit(map[string]any{"kind": "client_process", "pid": os.Getpid()})
				trace.emit(map[string]any{"kind": "header", "source": "result=read(value=42)", "inputs": nil, "artifact_sha256": fmt.Sprintf("%x", sha256.Sum256(wasm)), "ignore_cancel": ignore, "deadline": deadline})
				commandCtx, stopCommand := context.WithTimeout(context.Background(), 2*time.Minute)
				defer stopCommand()
				cmd := exec.CommandContext(commandCtx, os.Args[0], "-test.run=^TestHTTPFixtureProcess$")
				cmd.Env = append(os.Environ(), "PYSOLATE_HTTP_FIXTURE=1", "PYSOLATE_HTTP_SERVER_TRACE="+filepath.Join(dir, "server.jsonl"), fmt.Sprintf("PYSOLATE_HTTP_IGNORE_CANCEL=%d", map[bool]int{false: 0, true: 1}[ignore]))
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				trace.emit(map[string]any{"kind": "fixture_process", "pid": cmd.Process.Pid})
				waited := false
				defer func() {
					if !waited {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
					}
				}()
				var ready struct {
					URL string `json:"url"`
				}
				if err := json.NewDecoder(stdout).Decode(&ready); err != nil {
					t.Fatalf("fixture readiness: %v", err)
				}
				control := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
				defer control.CloseIdleConnections()
				get := func(path string) httpFixtureState {
					t.Helper()
					response, err := control.Get(ready.URL + path)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					var state httpFixtureState
					if response.StatusCode != http.StatusNoContent {
						if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
							t.Fatal(err)
						}
					}
					return state
				}
				transport := &http.Transport{Proxy: nil}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport}
				manifest := pysolate.Manifest{"read": {AllowEarlyRead: true, Call: func(ctx context.Context, args json.RawMessage) (any, error) {
					trace.emit(map[string]any{"kind": "http_begin", "args": args})
					req, err := http.NewRequestWithContext(ctx, http.MethodPost, ready.URL+"/read", bytes.NewReader(args))
					if err != nil {
						return nil, err
					}
					response, err := client.Do(req)
					if err != nil {
						trace.emit(map[string]any{"kind": "http_end", "error": err.Error()})
						return nil, err
					}
					defer response.Body.Close()
					body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
					trace.emit(map[string]any{"kind": "http_end", "body": body})
					var value struct {
						Value int `json:"value"`
					}
					if err == nil {
						err = json.Unmarshal(body, &value)
					}
					return value.Value, err
				}}}
				runner, err := pysolate.NewPrepared(commandCtx, wasm, manifest)
				if err != nil {
					t.Fatal(err)
				}

				var ctx context.Context
				var cancel context.CancelFunc
				if deadline {
					ctx, cancel = context.WithTimeout(commandCtx, 3*time.Second)
				} else {
					ctx, cancel = context.WithCancel(commandCtx)
				}
				defer cancel()
				ctx, raw := perfdiag.WithGuestIO(ctx)
				type result struct {
					output pysolate.Output
					err    error
				}
				done := make(chan result, 1)
				returned := false
				defer func() {
					cancel()
					if !returned {
						select {
						case <-done:
							returned = true
						case <-time.After(10 * time.Second):
							t.Error("execution still live during cleanup")
						}
					}
					if returned {
						if err := runner.Close(context.Background()); err != nil {
							t.Error(err)
						}
					}
				}()
				go func() {
					output, err := runner.RunWithEarlyReads(ctx, "result=read(value=42)", nil)
					done <- result{output, err}
				}()
				state := get("/started")
				if state.Active != 1 {
					t.Fatalf("not actually running: %+v", state)
				}
				if !deadline {
					cancel()
				}
				var run result
				select {
				case run = <-done:
					returned = true
				case <-time.After(10 * time.Second):
					t.Fatal("Guest did not return after cancellation")
				}
				if run.err == nil || ctx.Err() == nil {
					t.Fatal("cancelled Guest unexpectedly succeeded")
				}
				trace.emit(map[string]any{"kind": "guest_end", "output": run.output, "raw_io": raw, "error": run.err.Error(), "context_error": ctx.Err().Error()})
				if ignore {
					state = get("/state")
					trace.emit(map[string]any{"kind": "server_after_guest_return", "state": state})
					if state.Active != 1 || state.Completed != 0 || state.Cancelled != 0 {
						t.Fatalf("remote work did not outlive client: %+v", state)
					}
					trace.emit(map[string]any{"kind": "release_backend"})
					get("/release")
				}
				state = get("/settled")
				if state.Active != 0 || (ignore && (state.Completed != 1 || state.Cancelled != 0)) || (!ignore && (state.Completed != 0 || state.Cancelled != 1)) {
					t.Fatalf("unexpected backend outcome: %+v", state)
				}
				trace.emit(map[string]any{"kind": "final_state", "state": state, "complete": true})
				get("/stop")
				err = cmd.Wait()
				waited = true
				if err != nil {
					t.Fatalf("fixture exit: %v %s", err, stderr.String())
				}
				if err := trace.failure(); err != nil {
					t.Fatal(err)
				}
				if err := f.Sync(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
