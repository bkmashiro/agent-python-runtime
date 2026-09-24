package main

import (
	"context"
	"encoding/json"
	"errors"
	pysolate "github.com/bkmashiro/agent-python-runtime"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeHost = `import sys,json,time
mode=sys.argv[sys.argv.index('--task')+1]
print(json.dumps({'kind':'ready','tools':[{'name':'echo.echo','python_path':'apis.echo.echo'}]}),flush=True)
if mode=='no-read': time.sleep(30)
for line in sys.stdin:
 r=json.loads(line)
 if mode=='hang': time.sleep(30)
 if mode=='bad-json':
  print('not json',flush=True)
  break
 value=r.get('arguments') if r['op']=='call' else {'success':True}
 print(json.dumps({'id':r['id']+(1 if mode=='bad-id' else 0),'value':value}),flush=True)
 if r['op']=='finish': break
`

func fakePeer(t *testing.T, mode string) (*peer, context.Context) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	script := filepath.Join(t.TempDir(), "host.py")
	if err = os.WriteFile(script, []byte(fakeHost), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	p, err := startPeer(ctx, python, script, mode, "test", "unused", io.Discard)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = p.close() })
	ready, err := p.receive(ctx)
	if err != nil || ready.Kind != "ready" {
		t.Fatalf("ready %v %v", ready, err)
	}
	return p, ctx
}
func TestPeerExchangeAndIdentity(t *testing.T) {
	p, ctx := fakePeer(t, "normal")
	value, err := p.call(ctx, "call", "echo.echo", json.RawMessage(`{"value":42}`))
	if err != nil || string(value) != `{"value": 42}` {
		t.Fatalf("%s %v", value, err)
	}
	if _, err = p.call(ctx, "finish", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"bad-id", "bad-json"} {
		t.Run(mode, func(t *testing.T) {
			p, ctx := fakePeer(t, mode)
			if _, err := p.call(ctx, "call", "echo.echo", json.RawMessage(`{}`)); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}
func TestPeerCancellation(t *testing.T) {
	p, parent := fakePeer(t, "hang")
	ctx, cancel := context.WithTimeout(parent, 30*time.Millisecond)
	defer cancel()
	if _, err := p.call(ctx, "call", "echo.echo", json.RawMessage(`{}`)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
}
func TestPeerCancellationWhileWriting(t *testing.T) {
	p, parent := fakePeer(t, "no-read")
	ctx, cancel := context.WithTimeout(parent, 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.call(ctx, "call", "echo.echo", json.RawMessage(`{"pad":"`+strings.Repeat("x", 1<<20)+`"}`))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked stdin write ignored call deadline")
	}
}

func TestManifestRejectsDuplicatesAndDoesNotGrantEarlyRead(t *testing.T) {
	b := toolBinding{Name: "echo.echo", PythonPath: "apis.echo.echo"}
	if _, err := makeManifest([]toolBinding{b, b}, nil); err == nil {
		t.Fatal("duplicate accepted")
	}
	m, err := makeManifest([]toolBinding{b}, nil)
	if err != nil || m[b.Name].AllowEarlyRead {
		t.Fatalf("%v %+v", err, m)
	}
}
func TestRealGuestCrossesOnlyToolBridge(t *testing.T) {
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = "../../dist/pysolate.wasm"
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Skip("real Guest unavailable")
	}
	p, ctx := fakePeer(t, "normal")
	m, err := makeManifest([]toolBinding{{Name: "echo.echo", PythonPath: "apis.echo.echo"}}, p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := pysolate.NewPrepared(ctx, wasm, m)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	out, err := r.Run(ctx, "result=apis.echo.echo(value=42)", nil)
	var value struct {
		Value int `json:"value"`
	}
	decodeErr := json.Unmarshal(out.Value, &value)
	if err != nil || decodeErr != nil || value.Value != 42 {
		t.Fatalf("%+v %v %v", out, err, decodeErr)
	}
	_, err = r.Run(ctx, "result=apis.admin.hidden()", nil)
	if err == nil {
		t.Fatal("unregistered tool succeeded")
	}
	if _, err = p.call(ctx, "finish", "", nil); err != nil {
		t.Fatal(err)
	}
}
