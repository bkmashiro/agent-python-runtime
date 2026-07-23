package wazero

import (
	"context"
	"strings"
	"testing"
	"time"
)

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
