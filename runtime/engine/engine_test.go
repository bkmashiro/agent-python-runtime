package engine_test

import (
	"context"
	"testing"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

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
