package wazero

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const randomGetProbeBase64 = "AGFzbQEAAAABCwJgAn9/AX9gAAF/AiUBFndhc2lfc25hcHNob3RfcHJldmlldzEKcmFuZG9tX2dldAAAAwIBAQUDAQABBxgCBm1lbW9yeQIAC2ZpbGxfcmFuZG9tAAEKCgEIAEEAQSAQAAsAFARuYW1lAQ0BAApyYW5kb21fZ2V0"

func TestHostEntropyDiffersAcrossFreshInstances(t *testing.T) {
	ctx := context.Background()
	wasm, err := base64.StdEncoding.DecodeString(randomGetProbeBase64)
	if err != nil {
		t.Fatal(err)
	}
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Close(ctx)

	read := func() []byte {
		moduleConfig, _, err := newModuleConfig(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		module, err := runtime.InstantiateModule(ctx, compiled, moduleConfig)
		if err != nil {
			t.Fatal(err)
		}
		defer module.Close(ctx)
		fill := module.ExportedFunction("fill_random")
		values, err := fill.Call(ctx)
		if err != nil || len(values) != 1 || values[0] != 0 {
			t.Fatalf("random_get: values=%v err=%v", values, err)
		}
		value, ok := module.Memory().Read(0, 32)
		if !ok {
			t.Fatal("read random bytes")
		}
		return append([]byte(nil), value...)
	}

	first := read()
	second := read()
	if bytes.Equal(first, second) {
		t.Fatalf("fresh instances reused deterministic WASI entropy: %x", first)
	}
}
