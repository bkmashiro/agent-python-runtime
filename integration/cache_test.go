package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/tetratelabs/wazero"
)

func TestCompilationCacheDoesNotRetainAuthorityOrGuestState(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := wazero.NewCompilationCacheWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close(context.Background())
	ctx := pysolate.WithCompilationCache(context.Background(), cache)
	for _, value := range []int{1, 2} {
		value := value
		manifest := pysolate.Manifest{"value": {Call: func(context.Context, json.RawMessage) (any, error) { return value, nil }}}
		r, err := pysolate.New(ctx, wasm, manifest)
		if err != nil {
			t.Fatal(err)
		}
		out, err := r.Run(context.Background(), "assert 'previous' not in globals()\nprevious = True\nresult = value()", nil)
		if err != nil || string(out.Value) != string(rune('0'+value)) {
			t.Fatalf("value=%s err=%v", out.Value, err)
		}
		if err = r.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
