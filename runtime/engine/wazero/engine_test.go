package wazero_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
	engine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestPreparedCapacityHardBoundFailsBeforeCompile(t *testing.T) {
	factory := engine.Factory{PreparedCapacity: 5}
	if _, err := factory.New(context.Background(), []byte("not wasm"), runtime.DefaultRunConfig()); err == nil || !strings.Contains(err.Error(), "prepared capacity") {
		t.Fatalf("expected prepared capacity rejection, got %v", err)
	}
}

func TestFactoryBorrowsCompilationCacheUntilOwnerCloses(t *testing.T) {
	ctx := context.Background()
	cache := engine.NewCompilationCache()
	factory := engine.Factory{CompilationCache: cache}
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

	first, err := factory.New(ctx, wasm, runtime.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := factory.New(ctx, wasm, runtime.DefaultRunConfig())
	if err != nil {
		t.Fatalf("runner close also closed borrowed compilation cache: %v", err)
	}
	if err := second.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.New(ctx, wasm, runtime.DefaultRunConfig()); err == nil || !strings.Contains(err.Error(), "compilation cache is closed") {
		t.Fatalf("closed compilation cache was accepted: %v", err)
	}
	if err := cache.Close(ctx); err != nil {
		t.Fatalf("repeated compilation cache close failed: %v", err)
	}
}

func TestPreparedPoolDiscardsTrappedModule(t *testing.T) {
	wasm, err := base64.StdEncoding.DecodeString("AGFzbQEAAAABEwRgAABgAn9/AX9gAX8Bf2ABfwADBwYAAQECAwEFAwEAAQdVBwZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAADHJ1bnRpbWVfaW5pdAABD3J1bnRpbWVfcHJlcGFyZQACBWFsbG9jAAMHZGVhbGxvYwAEB2V4ZWN1dGUABQoaBgIACwQAQQALBABBAAsEAEEICwIACwMAAAs=")
	if err != nil {
		t.Fatal(err)
	}
	factory := engine.Factory{PreparedCapacity: 1}
	runner, err := factory.New(context.Background(), wasm, runtime.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	ready := runner.(interface{ PreparedReady() int })
	request := []byte(`{"run_id":"trap","code":"result = 1","inputs":{}}`)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := runner.Run(context.Background(), request, ""); err == nil || !strings.Contains(err.Error(), "unreachable") {
			t.Fatalf("attempt %d did not trap: %v", attempt, err)
		}
		deadline := time.Now().Add(time.Second)
		for ready.PreparedReady() != 1 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if ready.PreparedReady() != 1 {
			t.Fatalf("attempt %d did not refill after trap", attempt)
		}
	}
}

func TestPreparedPoolInitializationUsesHostTimeout(t *testing.T) {
	wasm, err := base64.StdEncoding.DecodeString("AGFzbQEAAAABEwRgAABgAn9/AX9gAX8Bf2ABfwADBwYAAQECAwEFAwEAAQdVBwZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAADHJ1bnRpbWVfaW5pdAABD3J1bnRpbWVfcHJlcGFyZQACBWFsbG9jAAMHZGVhbGxvYwAEB2V4ZWN1dGUABQogBgIACwkAA0AMAAtBAAsEAEEACwQAQQgLAgALBABBCAsAEwRuYW1lAwwBAQEAB2ZvcmV2ZXI=")
	if err != nil {
		t.Fatal(err)
	}
	config := runtime.DefaultRunConfig()
	config.Timeout = 50 * time.Millisecond
	started := time.Now()
	runner, err := (engine.Factory{PreparedCapacity: 1}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("prepared initialization ignored Host timeout: %s", elapsed)
	}
	if ready := runner.(interface{ PreparedReady() int }).PreparedReady(); ready != 0 {
		t.Fatalf("failed prepared candidate was admitted: %d", ready)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	config := runtime.DefaultRunConfig()
	config.MemoryLimitPages = 0
	if _, err := engine.New(context.Background(), []byte("not wasm"), config); err == nil {
		t.Fatal("expected invalid config rejection")
	}
}

func TestNewRejectsMalformedModule(t *testing.T) {
	if _, err := engine.New(
		context.Background(),
		[]byte("not wasm"),
		runtime.DefaultRunConfig(),
	); err == nil {
		t.Fatal("expected malformed module rejection")
	}
}
