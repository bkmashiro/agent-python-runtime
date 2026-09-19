package pysolate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPreparedCOWRealGuestLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux COW is intentionally unsupported on this platform")
	}
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	manifest := Manifest{"lookup": {Call: func(ctx context.Context, args json.RawMessage) (any, error) {
		var request struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(args, &request); err != nil {
			return nil, err
		}
		switch request.Key {
		case "price":
			return 21, nil
		case "first":
			return 20, nil
		case "second":
			return 6, nil
		default:
			return nil, errors.New("missing key: " + request.Key)
		}
	}, AllowEarlyRead: true}}
	r, err := NewPreparedCOW(ctx, wasm, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	// Exercise wazero's actual Memory.Grow, not just the allocator hook.
	m, err := r.newGuest(ctx, &boundedText{}, &boundedText{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := m.Memory().Read(0, 1)
	oldSize := m.Memory().Size()
	if _, ok := m.Memory().Grow(1); !ok {
		t.Fatal("COW memory growth failed")
	}
	after, _ := m.Memory().Read(0, 1)
	tail, ok := m.Memory().Read(oldSize, 65536)
	if !ok || &before[0] != &after[0] {
		t.Fatal("growth changed address or hid tail")
	}
	for _, b := range tail {
		if b != 0 {
			t.Fatal("new memory was not zero")
		}
	}
	if _, ok := m.Memory().Grow(65536); ok {
		t.Fatal("growth exceeded declared maximum")
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.image != nil {
		t.Fatal("COW runner retained a Go-owned full image copy")
	}
	if r.cow == nil || !r.cow.ready() {
		t.Fatal("COW runner did not retain a sealed prepared image")
	}

	if out, err := r.Run(ctx, `result=lookup(key="price")`, nil); err != nil || string(out.Value) != "21" {
		t.Fatalf("ordinary run: out=%+v err=%v", out, err)
	}
	if out, err := r.Run(ctx, `import builtins; builtins.cow_secret=123; result=builtins.cow_secret`, nil); err != nil || string(out.Value) != "123" {
		t.Fatalf("private-state write: out=%+v err=%v", out, err)
	}
	if out, err := r.Run(ctx, `import builtins; result=hasattr(builtins,"cow_secret")`, nil); err != nil || string(out.Value) != "false" {
		t.Fatalf("private-state leak: out=%+v err=%v", out, err)
	}
	out, err := r.RunPLM(ctx, "a=lookup(key='first')\nb=lookup(key='second')\nresult=a+b", nil)
	if err != nil || string(out.Value) != "26" || !strings.Contains(out.Transformed, "_pysolate_prepare") {
		t.Fatalf("PLM run: out=%+v err=%v", out, err)
	}
	chunks := make(chan string, 2)
	chunks <- "result=lookup(key='price')\n"
	chunks <- ""
	close(chunks)
	if out, err := r.RunPrefix(ctx, chunks, nil); err != nil || string(out.Value) != "21" {
		t.Fatalf("prefix run: out=%+v err=%v", out, err)
	}
	if _, err := r.Run(ctx, `result=lookup(key="absent")`, nil); err == nil || !strings.Contains(err.Error(), "missing key: absent") {
		t.Fatalf("guest/tool error: %v", err)
	}
	if out, err := r.Run(ctx, `result=7`, nil); err != nil || string(out.Value) != "7" {
		t.Fatalf("after error: out=%+v err=%v", out, err)
	}
	deadline, stop := context.WithTimeout(ctx, 2*time.Second)
	if _, err := r.Run(deadline, `while True: pass`, nil); err == nil || deadline.Err() == nil {
		t.Fatalf("timeout: err=%v deadline=%v", err, deadline.Err())
	}
	stop()
	if out, err := r.Run(ctx, `result=8`, nil); err != nil || string(out.Value) != "8" {
		t.Fatalf("after timeout: out=%+v err=%v", out, err)
	}
	maps, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(maps), "memfd:pysolate-spine-cow") {
		t.Fatal("COW mapping remained after runs completed")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		target, _ := os.Readlink("/proc/self/fd/" + entry.Name())
		if strings.Contains(target, "memfd:pysolate-spine-cow") {
			t.Fatal("COW fd leaked")
		}
	}
}
