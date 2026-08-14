package agentfunction

import (
	"context"
	"strings"
	"testing"
)

func TestCacheDomainsSeparateCallbackAndSemanticGuestProvenance(t *testing.T) {
	invocation := domainTestInvocation()
	store, err := NewBoundedStore(t.TempDir(), invocation.ProjectSHA256, 16, 4096)
	if err != nil {
		t.Fatal(err)
	}
	engine := Engine{Store: store, CacheEnabled: true}
	callback, err := engine.Execute(context.Background(), invocation, func(context.Context, *Guard) ([]byte, error) {
		return []byte("1"), nil
	})
	if err != nil || callback.CacheHit {
		t.Fatalf("callback=%+v err=%v", callback, err)
	}
	guest, err := engine.execute(context.Background(), invocation, func(context.Context, *Guard) ([]byte, error) {
		return []byte("2"), nil
	}, "fresh-guest")
	if err != nil || guest.CacheHit || string(guest.Value) != "2" {
		t.Fatalf("guest=%+v err=%v", guest, err)
	}
	guestHit, err := engine.execute(context.Background(), invocation, func(context.Context, *Guard) ([]byte, error) {
		t.Fatal("guest domain must hit")
		return nil, nil
	}, "fresh-guest")
	if err != nil || !guestHit.CacheHit || string(guestHit.Value) != "2" {
		t.Fatalf("guest-hit=%+v err=%v", guestHit, err)
	}
	callbackHit, err := engine.Execute(context.Background(), invocation, func(context.Context, *Guard) ([]byte, error) {
		t.Fatal("callback domain must hit")
		return nil, nil
	})
	if err != nil || !callbackHit.CacheHit || string(callbackHit.Value) != "1" {
		t.Fatalf("callback-hit=%+v err=%v", callbackHit, err)
	}
}

func domainTestInvocation() Invocation {
	digest := func(character string) string { return "sha256:" + strings.Repeat(character, 64) }
	return Invocation{
		SchemaVersion: InvocationSchemaVersion, Admission: Cacheable,
		ProjectSHA256: digest("1"), FunctionSourceSHA256: digest("2"), ArtifactSHA256: digest("3"),
		ExecutionProfileSHA256: digest("4"), ImportClosureSHA256: digest("5"),
		CanonicalInputs: []byte(`{"value":1}`), ImmutableRootSHA256: []string{digest("6")},
		DeterministicSettingsSHA256: digest("7"), OutputSchemaSHA256: digest("a"),
		PolicyEpochSHA256: digest("b"), PrivacyPartition: "private",
	}
}
