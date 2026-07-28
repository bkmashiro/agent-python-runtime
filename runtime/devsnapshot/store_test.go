package devsnapshot_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/devsnapshot"
)

func TestStorePutReopenAndReplaceAtomicBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshots.db")
	store, err := devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := map[string]json.RawMessage{"provider": json.RawMessage(`{"generation":1}`), "controller": json.RawMessage(`{"generation":1}`)}
	saved, err := store.Put(context.Background(), "job:test", first)
	if err != nil || saved.Digest == "" {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	reopened, err := devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.Get(context.Background(), "job:test")
	if err != nil || loaded.Digest != saved.Digest || string(loaded.Components["provider"]) != `{"generation":1}` {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	second := map[string]json.RawMessage{"provider": json.RawMessage(`{"generation":2}`), "controller": json.RawMessage(`{"generation":2}`)}
	if _, err := reopened.Put(context.Background(), "job:test", second); err != nil {
		t.Fatal(err)
	}
	loaded, err = reopened.Get(context.Background(), "job:test")
	if err != nil || string(loaded.Components["provider"]) != `{"generation":2}` || string(loaded.Components["controller"]) != `{"generation":2}` {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestStoreRejectsRelativeSymlinkAndInvalidPayload(t *testing.T) {
	if _, err := devsnapshot.Open("relative.db"); err == nil {
		t.Fatal("relative path accepted")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	store, err := devsnapshot.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := devsnapshot.Open(link); err == nil {
		t.Fatal("symlink accepted")
	}
	store, err = devsnapshot.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Put(context.Background(), "job:test", map[string]json.RawMessage{"provider": json.RawMessage(`not-json`)}); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}
