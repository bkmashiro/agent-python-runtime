package agentfunction_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
)

func TestStoreRetentionBoundFailsClosed(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "bounded")
	store, err := agentfunction.NewBoundedStore(directory, digest('1'), 1024, 1024)
	if err != nil {
		t.Fatal(err)
	}
	engine := agentfunction.Engine{Store: store, CacheEnabled: true}
	_, err = engine.Execute(context.Background(), cacheableInvocation(), func(context.Context, *agentfunction.Guard) ([]byte, error) {
		return make([]byte, 900), nil
	})
	if !errors.Is(err, agentfunction.ErrRetentionLimit) || store.Stats().Writes != 0 {
		t.Fatalf("error=%v stats=%+v", err, store.Stats())
	}
}

func TestCacheableFunctionUsesProjectPrivateContentAddressedResult(t *testing.T) {
	store := newStore(t, digest('1'))
	engine := agentfunction.Engine{Store: store, CacheEnabled: true}
	invocation := cacheableInvocation()
	var calls atomic.Int32
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		calls.Add(1)
		return []byte(`{"answer":42}`), nil
	}
	first, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheHit || !second.CacheHit || calls.Load() != 1 || string(second.Value) != `{"answer":42}` || first.Key != second.Key {
		t.Fatalf("first=%+v second=%+v calls=%d", first, second, calls.Load())
	}
	stats := store.Stats()
	if stats.Hits != 1 || stats.Misses != 1 || stats.Writes != 1 || stats.StoredBytes == 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestCacheOffAndNotCacheableAlwaysUseFreshCompute(t *testing.T) {
	store := newStore(t, digest('1'))
	var calls atomic.Int32
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		return []byte{byte(calls.Add(1))}, nil
	}
	for _, invocation := range []agentfunction.Invocation{
		cacheableInvocation(),
		func() agentfunction.Invocation {
			value := cacheableInvocation()
			value.Admission = agentfunction.NotCacheable
			return value
		}(),
	} {
		engine := agentfunction.Engine{Store: store, CacheEnabled: invocation.Admission == agentfunction.NotCacheable}
		first, err := engine.Execute(context.Background(), invocation, compute)
		if err != nil {
			t.Fatal(err)
		}
		second, err := engine.Execute(context.Background(), invocation, compute)
		if err != nil {
			t.Fatal(err)
		}
		if first.CacheHit || second.CacheHit || string(first.Value) == string(second.Value) {
			t.Fatalf("fresh fallback first=%+v second=%+v", first, second)
		}
	}
}

func TestSemanticQualificationParticipatesInInvocationIdentity(t *testing.T) {
	base := cacheableInvocation()
	base.SemanticAnalysisSHA256 = digest('a')
	base.SemanticPlanSHA256 = digest('b')
	base.SemanticAnalyzerSHA256 = digest('c')
	base.SemanticRegionID = digest('d')
	base.SemanticRequestContractSHA256 = digest('f')
	baseKey, _, err := base.Identity()
	if err != nil {
		t.Fatal(err)
	}
	variants := []agentfunction.Invocation{base, base, base, base, base}
	variants[0].SemanticAnalysisSHA256 = digest('e')
	variants[1].SemanticPlanSHA256 = digest('e')
	variants[2].SemanticAnalyzerSHA256 = digest('e')
	variants[3].SemanticRegionID = digest('e')
	variants[4].SemanticRequestContractSHA256 = digest('e')
	for index, variant := range variants {
		key, _, err := variant.Identity()
		if err != nil || key == baseKey {
			t.Fatalf("variant %d key=%q err=%v", index, key, err)
		}
	}
	partial := base
	partial.SemanticRegionID = ""
	if err := partial.Validate(); !errors.Is(err, agentfunction.ErrInvalidInvocation) {
		t.Fatalf("partial semantic identity error=%v", err)
	}
}

func TestCacheableFunctionFailsClosedOnForbiddenAuthority(t *testing.T) {
	for name, operation := range map[string]func(*agentfunction.Guard) error{
		"host call":       func(guard *agentfunction.Guard) error { return guard.HostCall("fixture.read") },
		"undeclared read": func(guard *agentfunction.Guard) error { return guard.UndeclaredRead("hidden.txt") },
		"shared write":    func(guard *agentfunction.Guard) error { return guard.SharedWrite("out.txt") },
		"clock":           func(guard *agentfunction.Guard) error { return guard.Clock() },
		"random":          func(guard *agentfunction.Guard) error { return guard.Random() },
		"dynamic import":  func(guard *agentfunction.Guard) error { return guard.DynamicImport("module") },
	} {
		t.Run(name, func(t *testing.T) {
			store := newStore(t, digest('1'))
			engine := agentfunction.Engine{Store: store, CacheEnabled: true}
			_, err := engine.Execute(context.Background(), cacheableInvocation(), func(_ context.Context, guard *agentfunction.Guard) ([]byte, error) {
				if err := operation(guard); err != nil {
					return nil, err
				}
				return []byte(`{"unsafe":true}`), nil
			})
			if !errors.Is(err, agentfunction.ErrCachePurity) || store.Stats().Writes != 0 {
				t.Fatalf("error=%v stats=%+v", err, store.Stats())
			}
		})
	}
}

func TestEvictionAndCorruptionSafelyRecompute(t *testing.T) {
	store := newStore(t, digest('1'))
	engine := agentfunction.Engine{Store: store, CacheEnabled: true}
	invocation := cacheableInvocation()
	var calls atomic.Int32
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		return []byte{byte(calls.Add(1))}, nil
	}
	first, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Evict(first.Key); err != nil {
		t.Fatal(err)
	}
	second, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil || second.CacheHit || calls.Load() != 2 {
		t.Fatalf("eviction result=%+v calls=%d err=%v", second, calls.Load(), err)
	}
	if err := os.WriteFile(filepath.Join(store.Directory(), cacheStorageFilename("callback", second.Key)), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil || third.CacheHit || calls.Load() != 3 || store.Stats().Corruptions != 1 {
		t.Fatalf("corruption result=%+v calls=%d stats=%+v err=%v", third, calls.Load(), store.Stats(), err)
	}
	path := filepath.Join(store.Directory(), cacheStorageFilename("callback", third.Key))
	record, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	record = bytes.Replace(record, []byte(`"physical_execution_id":""`), []byte(`"physical_execution_id":"bad id"`), 1)
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatal(err)
	}
	fourth, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil || fourth.CacheHit || calls.Load() != 4 || store.Stats().Corruptions != 2 {
		t.Fatalf("provenance corruption result=%+v calls=%d stats=%+v err=%v", fourth, calls.Load(), store.Stats(), err)
	}
}

func TestInvocationIdentityPartitionsEveryDeclaredDimension(t *testing.T) {
	base := cacheableInvocation()
	baseKey, _, err := base.Identity()
	if err != nil {
		t.Fatal(err)
	}
	variants := []agentfunction.Invocation{
		func() agentfunction.Invocation { value := base; value.FunctionSourceSHA256 = digest('a'); return value }(),
		func() agentfunction.Invocation { value := base; value.ArtifactSHA256 = digest('a'); return value }(),
		func() agentfunction.Invocation {
			value := base
			value.ExecutionProfileSHA256 = digest('a')
			return value
		}(),
		func() agentfunction.Invocation { value := base; value.ImportClosureSHA256 = digest('a'); return value }(),
		func() agentfunction.Invocation {
			value := base
			value.CanonicalInputs = []byte(`{"value":2}`)
			return value
		}(),
		func() agentfunction.Invocation {
			value := base
			value.ImmutableRootSHA256 = []string{digest('a')}
			return value
		}(),
		func() agentfunction.Invocation {
			value := base
			value.DeterministicSettingsSHA256 = digest('a')
			return value
		}(),
		func() agentfunction.Invocation { value := base; value.OutputSchemaSHA256 = digest('a'); return value }(),
		func() agentfunction.Invocation { value := base; value.PrivacyPartition = "partition-b"; return value }(),
		func() agentfunction.Invocation { value := base; value.PolicyEpochSHA256 = digest('a'); return value }(),
	}
	for index, variant := range variants {
		key, _, err := variant.Identity()
		if err != nil || key == baseKey {
			t.Fatalf("variant %d key=%q err=%v", index, key, err)
		}
	}
}

func TestStoreRejectsCrossProjectInvocation(t *testing.T) {
	store := newStore(t, digest('1'))
	invocation := cacheableInvocation()
	invocation.ProjectSHA256 = digest('2')
	_, err := (agentfunction.Engine{Store: store, CacheEnabled: true}).Execute(context.Background(), invocation, func(context.Context, *agentfunction.Guard) ([]byte, error) {
		return []byte(`{}`), nil
	})
	if !errors.Is(err, agentfunction.ErrProjectPartition) {
		t.Fatalf("error=%v", err)
	}
}

func newStore(t *testing.T, project string) *agentfunction.Store {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "function-cache")
	store, err := agentfunction.NewStore(directory, project, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func cacheStorageFilename(domain, key string) string {
	digest := sha256.Sum256([]byte(domain + "\x00" + key))
	return fmt.Sprintf("sha256:%x.json", digest[:])
}

func cacheableInvocation() agentfunction.Invocation {
	return agentfunction.Invocation{
		SchemaVersion: agentfunction.InvocationSchemaVersion, Admission: agentfunction.Cacheable,
		ProjectSHA256: digest('1'), FunctionSourceSHA256: digest('2'), ArtifactSHA256: digest('3'),
		ExecutionProfileSHA256: digest('4'), ImportClosureSHA256: digest('5'),
		CanonicalInputs: []byte(`{"value":1}`), ImmutableRootSHA256: []string{digest('6')},
		DeterministicSettingsSHA256: digest('7'), OutputSchemaSHA256: digest('8'),
		PrivacyPartition: "partition-a", PolicyEpochSHA256: digest('9'),
	}
}

func digest(character byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = character
	}
	return "sha256:" + string(value)
}
