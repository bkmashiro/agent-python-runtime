package wazero

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	wazerort "github.com/tetratelabs/wazero"
)

func TestDiskCompilationCacheIsExplicitlyOwned(t *testing.T) {
	if _, err := NewCompilationCacheWithDir(""); err == nil {
		t.Fatal("empty cache directory was accepted")
	}
	cache, err := NewCompilationCacheWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, release, err := cache.acquire(); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	if err := cache.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDiskCompilationCacheReopensCompiledArtifact(t *testing.T) {
	dir := t.TempDir()
	wasm := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	for attempt := 0; attempt < 2; attempt++ {
		cache, err := NewCompilationCacheWithDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		inner, release, err := cache.acquire()
		if err != nil {
			t.Fatal(err)
		}
		r := wazerort.NewRuntimeWithConfig(context.Background(), wazerort.NewRuntimeConfigCompiler().WithCompilationCache(inner))
		compiled, err := r.CompileModule(context.Background(), wasm)
		if err != nil {
			t.Fatal(err)
		}
		_ = compiled.Close(context.Background())
		_ = r.Close(context.Background())
		release()
		if err := cache.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("disk cache was not populated: entries=%d err=%v", len(entries), err)
	}
}

func TestCompilationCacheCloseWaitsForActiveBorrow(t *testing.T) {
	cache := NewCompilationCache()
	_, release, err := cache.acquire()
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- cache.Close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("cache closed during active borrow: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.acquire(); err == nil || !strings.Contains(err.Error(), "compilation cache is closed") {
		t.Fatalf("closed cache allowed a new borrow: %v", err)
	}
}
