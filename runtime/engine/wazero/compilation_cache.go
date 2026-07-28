package wazero

import (
	"context"
	"errors"
	"sync"

	wazerort "github.com/tetratelabs/wazero"
)

// CompilationCache is an explicitly owned wazero compilation cache. Factories
// borrow it only while CompileModule is active; runners never close it.
type CompilationCache struct {
	mu        sync.Mutex
	inner     wazerort.CompilationCache
	active    sync.WaitGroup
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func NewCompilationCache() *CompilationCache {
	return &CompilationCache{inner: wazerort.NewCompilationCache()}
}

// NewCompilationCacheWithDir creates an explicitly owned disk-backed cache.
// The caller controls directory privacy, persistence, generation policy, and
// Close lifecycle; factories only borrow the cache during CompileModule.
func NewCompilationCacheWithDir(path string) (*CompilationCache, error) {
	if path == "" {
		return nil, errors.New("compilation cache directory is empty")
	}
	inner, err := wazerort.NewCompilationCacheWithDir(path)
	if err != nil {
		return nil, err
	}
	return &CompilationCache{inner: inner}, nil
}

func (cache *CompilationCache) acquire() (wazerort.CompilationCache, func(), error) {
	if cache == nil {
		return nil, nil, errors.New("compilation cache is nil")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.closed || cache.inner == nil {
		return nil, nil, errors.New("compilation cache is closed")
	}
	cache.active.Add(1)
	var once sync.Once
	return cache.inner, func() { once.Do(cache.active.Done) }, nil
}

func (cache *CompilationCache) Close(ctx context.Context) error {
	if cache == nil {
		return nil
	}
	cache.closeOnce.Do(func() {
		cache.mu.Lock()
		cache.closed = true
		inner := cache.inner
		cache.mu.Unlock()
		cache.active.Wait()
		if inner != nil {
			cache.closeErr = inner.Close(ctx)
		}
	})
	return cache.closeErr
}
