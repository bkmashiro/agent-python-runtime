package fakeworkspace_test

import (
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeworkspace"
)

func TestAcquiredWorkspaceExpiresAndReacquiresWithStableIdentity(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	limits := fakeworkspace.DefaultLimits()
	store, err := fakeworkspace.NewStoreWithClock([]fakeworkspace.Fixture{{Alias: "seed", Revision: revision, Files: map[string][]byte{"seed.txt": []byte("seed")}}}, limits, func() time.Time { return now }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixture := fakeworkspace.Fixture{Alias: "acquired", Revision: "2222222222222222222222222222222222222222", Files: map[string][]byte{"README.md": []byte("acquired")}}
	manifest, err := fakeworkspace.FixtureManifest(fixture, limits)
	if err != nil {
		t.Fatal(err)
	}
	binding := fakeworkspace.Binding{RunIdentity: "run:acquired", TaskIdentity: "task:acquired"}
	first, err := store.Acquire(binding, fixture, manifest)
	if err != nil || store.WorkspaceCount() != 1 {
		t.Fatalf("first=%+v count=%d err=%v", first, store.WorkspaceCount(), err)
	}
	now = now.Add(2 * time.Minute)
	second, err := store.Acquire(binding, fixture, manifest)
	if err != nil || second.WorkspaceID != first.WorkspaceID || store.WorkspaceCount() != 1 {
		t.Fatalf("second=%+v count=%d err=%v", second, store.WorkspaceCount(), err)
	}
}
