package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentCoreCommonUseCases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := executeAcceptance(ctx, filepath.Join("..", "..", "dist", "pysolate.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Value) == 0 {
		t.Fatal("acceptance returned no value")
	}
}
