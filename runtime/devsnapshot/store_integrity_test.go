package devsnapshot_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/devsnapshot"
	_ "modernc.org/sqlite"
)

func TestStoreRejectsSchemaDriftAndFutureVersion(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate string
	}{{"drift", `ALTER TABLE dev_snapshots ADD COLUMN rogue TEXT`}, {"future", `PRAGMA user_version=99`}} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshots.db")
			store, err := devsnapshot.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			_ = store.Close()
			db, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(test.mutate); err != nil {
				t.Fatal(err)
			}
			_ = db.Close()
			if _, err := devsnapshot.Open(path); !errors.Is(err, devsnapshot.ErrUnsupportedSchema) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestStoreDetectsPayloadTamper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshots.db")
	store, err := devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), "job:test", map[string]json.RawMessage{"provider": json.RawMessage(`{"value":1}`)}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE dev_snapshots SET payload=? WHERE id=?`, []byte(`{"schema_version":1,"components":{"provider":{"value":2}}}`), "job:test"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err = devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Get(context.Background(), "job:test"); !errors.Is(err, devsnapshot.ErrIntegrity) {
		t.Fatalf("err=%v", err)
	}
}

func TestStoreTwoHandlesNeverExposeMixedBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshots.db")
	first, err := devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	bundles := []map[string]json.RawMessage{{"provider": json.RawMessage(`{"generation":1}`), "controller": json.RawMessage(`{"generation":1}`)}, {"provider": json.RawMessage(`{"generation":2}`), "controller": json.RawMessage(`{"generation":2}`)}}
	stores := []*devsnapshot.Store{first, second}
	errs := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(1)
	for index := range stores {
		go func(index int) {
			start.Wait()
			_, err := stores[index].Put(context.Background(), "job:shared", bundles[index])
			errs <- err
		}(index)
	}
	start.Done()
	for range stores {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := first.Get(context.Background(), "job:shared")
	if err != nil {
		t.Fatal(err)
	}
	provider := string(loaded.Components["provider"])
	controller := string(loaded.Components["controller"])
	if (provider == `{"generation":1}`) != (controller == `{"generation":1}`) {
		t.Fatalf("mixed bundle provider=%s controller=%s", provider, controller)
	}
}
