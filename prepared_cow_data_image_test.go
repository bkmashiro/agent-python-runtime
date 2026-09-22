package pysolate

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
	"github.com/tetratelabs/wazero"
)

func TestCOWDataImageTransformsCurrentArtifact(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	image, err := transformCOWDataImage(wasm)
	if err != nil {
		t.Fatal(err)
	}
	if len(image.shell) >= len(wasm) || len(image.active) == 0 {
		t.Fatalf("shell=%d original=%d active=%d", len(image.shell), len(wasm), len(image.active))
	}
	if !strings.Contains(string(image.shell), "\x00asm") {
		t.Fatal("shell lost wasm header")
	}
	rt := wazero.NewRuntime(context.Background())
	defer rt.Close(context.Background())
	if _, err := rt.CompileModule(context.Background(), image.shell); err != nil {
		t.Fatalf("shell does not compile: %v", err)
	}
}

func TestPreparedCOWDataImageRealGuestLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux COW is intentionally unsupported on this platform")
	}
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{"lookup": {Call: func(ctx context.Context, args json.RawMessage) (any, error) {
		var request struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(args, &request); err != nil {
			return nil, err
		}
		if request.Key == "price" {
			return 21, nil
		}
		return nil, errors.New("missing key: " + request.Key)
	}, AllowEarlyRead: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := NewPreparedCOW(ctx, wasm, manifest, COWOptions{DataImage: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	if r.cowSeed == nil || !r.cowSeed.Ready() || r.cow == nil || !r.cow.Ready() {
		t.Fatalf("seed=%v final=%v", r.cowSeed != nil && r.cowSeed.Ready(), r.cow != nil && r.cow.Ready())
	}
	if out, err := r.Run(ctx, `result=lookup(key="price")`, nil); err != nil || string(out.Value) != "21" {
		t.Fatalf("baseline result=%s err=%v", out.Value, err)
	}
	if out, err := r.Run(ctx, `import builtins; builtins.data_image_secret=123; result=123`, nil); err != nil || string(out.Value) != "123" {
		t.Fatalf("private write result=%s err=%v", out.Value, err)
	}
	if out, err := r.Run(ctx, `import builtins; result=hasattr(builtins, "data_image_secret")`, nil); err != nil || string(out.Value) != "false" {
		t.Fatalf("private state leaked result=%s err=%v", out.Value, err)
	}
	deadline, stop := context.WithTimeout(ctx, 2*time.Second)
	if _, err := r.Run(deadline, `while True: pass`, nil); err == nil || deadline.Err() == nil {
		t.Fatalf("timeout err=%v deadline=%v", err, deadline.Err())
	}
	stop()
	if out, err := r.Run(ctx, `result=8`, nil); err != nil || string(out.Value) != "8" {
		t.Fatalf("after timeout result=%s err=%v", out.Value, err)
	}
	images, allocated, err := perfdiag.COWStorage()
	// Current artifact: 128 MiB final image plus <32 MiB sparse seed.
	if err != nil || images != 2 || allocated >= 160<<20 {
		t.Fatalf("seed holes materialized: images=%d allocated=%d err=%v", images, allocated, err)
	}
}
