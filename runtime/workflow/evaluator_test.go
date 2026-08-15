package workflow_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/workflow"
)

func TestWaitDestroysGuestAndResumeUsesFreshGuestWithLocalLookup(t *testing.T) {
	factory := &guestFactory{}
	var computeCalls, observationCalls int
	graph := workflow.Graph{
		SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: "fixture-workflow",
		Nodes: []workflow.Node{
			{ID: "seed", Kind: workflow.Compute, VersionSHA256: digest('1'), Compute: func(context.Context, workflow.Guest, map[string][]byte) ([]byte, error) {
				computeCalls++
				return []byte("seed"), nil
			}},
			{ID: "live", Kind: workflow.Observation, VersionSHA256: digest('2'), Dependencies: []string{"seed"}, RefreshOnResume: true, Observe: func(context.Context, workflow.Guest, map[string][]byte) (workflow.ObservedValue, error) {
				observationCalls++
				return workflow.ObservedValue{Value: []byte("v1"), FreshnessSHA256: digest('a'), PolicySHA256: digest('b')}, nil
			}},
			{ID: "derived", Kind: workflow.Compute, VersionSHA256: digest('3'), Dependencies: []string{"seed", "live"}, Compute: func(_ context.Context, _ workflow.Guest, values map[string][]byte) ([]byte, error) {
				computeCalls++
				return append(append([]byte(nil), values["seed"]...), values["live"]...), nil
			}},
			{ID: "wait", Kind: workflow.Wait, VersionSHA256: digest('4'), Dependencies: []string{"derived"}},
			{ID: "after", Kind: workflow.Compute, VersionSHA256: digest('5'), Dependencies: []string{"derived"}, Compute: func(_ context.Context, _ workflow.Guest, values map[string][]byte) ([]byte, error) {
				computeCalls++
				return append([]byte("after:"), values["derived"]...), nil
			}},
			{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: digest('6'), Dependencies: []string{"after"}},
		},
	}
	evaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: authority('a', "private-a")})
	if err != nil {
		t.Fatal(err)
	}
	first, err := evaluator.Start(context.Background(), []byte(`{"request":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.Disposition != workflow.Suspended || !factory.guests[0].closed || len(factory.guests) != 1 || computeCalls != 2 || observationCalls != 1 {
		t.Fatalf("first=%+v guests=%+v compute=%d observe=%d", first, factory.guests, computeCalls, observationCalls)
	}
	if _, exists := first.State.Records["after"]; exists {
		t.Fatal("post-wait node executed before resume")
	}
	second, err := evaluator.Resume(context.Background(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disposition != workflow.Completed || len(factory.guests) != 2 || factory.guests[0] == factory.guests[1] || !factory.guests[1].closed {
		t.Fatalf("second=%+v guests=%+v", second, factory.guests)
	}
	if computeCalls != 3 || observationCalls != 2 || second.Metrics.Lookups < 3 || second.Metrics.GuestInstances != 1 || second.Metrics.RetainedStateBytes == 0 {
		t.Fatalf("compute=%d observe=%d metrics=%+v", computeCalls, observationCalls, second.Metrics)
	}
	if string(second.Output) != "after:seedv1" {
		t.Fatalf("output=%q", second.Output)
	}
	if first.ExecutionSHA256 == second.ExecutionSHA256 || first.ExecutionSHA256 == "" || second.ExecutionSHA256 == "" {
		t.Fatalf("execution identities first=%q second=%q", first.ExecutionSHA256, second.ExecutionSHA256)
	}
	assertNoHiddenState(t, second.State)
}

func TestChangedObservationInvalidatesOnlyTransitiveDescendants(t *testing.T) {
	factory := &guestFactory{}
	observationValue := "v1"
	calls := map[string]int{}
	compute := func(id, value string) workflow.ComputeFunc {
		return func(context.Context, workflow.Guest, map[string][]byte) ([]byte, error) {
			calls[id]++
			return []byte(value), nil
		}
	}
	graph := workflow.Graph{SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: "invalidation", Nodes: []workflow.Node{
		{ID: "independent", Kind: workflow.Compute, VersionSHA256: digest('1'), Compute: compute("independent", "stable")},
		{ID: "live", Kind: workflow.Observation, VersionSHA256: digest('2'), RefreshOnResume: true, Observe: func(context.Context, workflow.Guest, map[string][]byte) (workflow.ObservedValue, error) {
			calls["live"]++
			return workflow.ObservedValue{Value: []byte(observationValue), FreshnessSHA256: digest(observationValue[1]), PolicySHA256: digest('b')}, nil
		}},
		{ID: "dependent", Kind: workflow.Compute, VersionSHA256: digest('3'), Dependencies: []string{"live"}, Compute: func(_ context.Context, _ workflow.Guest, values map[string][]byte) ([]byte, error) {
			calls["dependent"]++
			return append([]byte("derived:"), values["live"]...), nil
		}},
		{ID: "wait", Kind: workflow.Wait, VersionSHA256: digest('4'), Dependencies: []string{"dependent"}},
		{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: digest('5'), Dependencies: []string{"independent", "dependent"}},
	}}
	evaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: authority('a', "private-a")})
	if err != nil {
		t.Fatal(err)
	}
	first, err := evaluator.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	observationValue = "v2"
	second, err := evaluator.Resume(context.Background(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if calls["independent"] != 1 || calls["live"] != 2 || calls["dependent"] != 2 || second.Metrics.Invalidated != 2 {
		t.Fatalf("calls=%v metrics=%+v", calls, second.Metrics)
	}
	if string(second.Output) != "derived:v2" {
		t.Fatalf("output=%q", second.Output)
	}
}

func TestEvictedComputeRecordSafelyRecomputes(t *testing.T) {
	factory := &guestFactory{}
	var calls int
	graph := workflow.Graph{SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: "eviction", Nodes: []workflow.Node{
		{ID: "compute", Kind: workflow.Compute, VersionSHA256: digest('1'), Compute: func(context.Context, workflow.Guest, map[string][]byte) ([]byte, error) {
			calls++
			return []byte("value"), nil
		}},
		{ID: "wait", Kind: workflow.Wait, VersionSHA256: digest('2'), Dependencies: []string{"compute"}},
		{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: digest('3'), Dependencies: []string{"compute"}},
	}}
	evaluator, _ := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: authority('a', "private-a")})
	first, _ := evaluator.Start(context.Background(), nil)
	if err := first.State.Evict("compute"); err != nil {
		t.Fatal(err)
	}
	second, err := evaluator.Resume(context.Background(), first.State)
	if err != nil || calls != 2 || second.Metrics.Recomputed == 0 {
		t.Fatalf("calls=%d metrics=%+v err=%v", calls, second.Metrics, err)
	}
}

func TestResumeDisabledFallsBackToOrdinaryFreshStart(t *testing.T) {
	factory := &guestFactory{}
	graph := workflow.Graph{SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: "fallback", Nodes: []workflow.Node{
		{ID: "wait", Kind: workflow.Wait, VersionSHA256: digest('1')},
		{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: digest('2')},
	}}
	evaluator, _ := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: false, Authority: authority('a', "private-a")})
	first, err := evaluator.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.Resume(context.Background(), first.State); !errors.Is(err, workflow.ErrResumeDisabled) {
		t.Fatalf("Resume() error=%v", err)
	}
	second, err := evaluator.Start(context.Background(), nil)
	if err != nil || len(factory.guests) != 2 || !factory.guests[0].closed || !factory.guests[1].closed || second.Disposition != workflow.Suspended {
		t.Fatalf("fallback second=%+v guests=%+v err=%v", second, factory.guests, err)
	}
}

func TestAuthorityChangeRevalidatesOnlyObservationsAndDescendants(t *testing.T) {
	factory := &guestFactory{}
	calls := map[string]int{}
	graph := workflow.Graph{SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: "authority-revalidation", Nodes: []workflow.Node{
		{ID: "independent", Kind: workflow.Compute, VersionSHA256: digest('1'), Compute: func(context.Context, workflow.Guest, map[string][]byte) ([]byte, error) {
			calls["independent"]++
			return []byte("stable"), nil
		}},
		{ID: "live", Kind: workflow.Observation, VersionSHA256: digest('2'), Observe: func(context.Context, workflow.Guest, map[string][]byte) (workflow.ObservedValue, error) {
			calls["live"]++
			return workflow.ObservedValue{Value: []byte("observed"), FreshnessSHA256: digest('f'), PolicySHA256: digest('e')}, nil
		}},
		{ID: "dependent", Kind: workflow.Compute, VersionSHA256: digest('3'), Dependencies: []string{"live"}, Compute: func(_ context.Context, _ workflow.Guest, values map[string][]byte) ([]byte, error) {
			calls["dependent"]++
			return append([]byte(nil), values["live"]...), nil
		}},
		{ID: "wait", Kind: workflow.Wait, VersionSHA256: digest('4'), Dependencies: []string{"dependent"}},
		{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: digest('5'), Dependencies: []string{"independent", "dependent"}},
	}}
	firstEvaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: authority('a', "private-a")})
	if err != nil {
		t.Fatal(err)
	}
	first, err := firstEvaluator.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	variants := []struct {
		name   string
		mutate func(*workflow.AuthorityEnvelope)
	}{
		{"plan", func(value *workflow.AuthorityEnvelope) { value.PlanSHA256 = digest('b') }},
		{"grant", func(value *workflow.AuthorityEnvelope) { value.GrantSetSHA256 = digest('c') }},
		{"epoch", func(value *workflow.AuthorityEnvelope) { value.EpochSHA256 = digest('d') }},
		{"expiry", func(value *workflow.AuthorityEnvelope) { value.NotAfterUnixMS-- }},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			secondAuthority := authority('a', "private-a")
			variant.mutate(&secondAuthority)
			secondEvaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: secondAuthority})
			if err != nil {
				t.Fatal(err)
			}
			second, err := secondEvaluator.Resume(context.Background(), first.State)
			if err != nil {
				t.Fatal(err)
			}
			if second.Metrics.Invalidated != 2 || second.State.Authority != secondAuthority || first.ExecutionSHA256 == second.ExecutionSHA256 {
				t.Fatalf("state=%+v metrics=%+v first_execution=%q second_execution=%q", second.State.Authority, second.Metrics, first.ExecutionSHA256, second.ExecutionSHA256)
			}
		})
	}
	if calls["independent"] != 1 || calls["live"] != 1+len(variants) || calls["dependent"] != 1+len(variants) {
		t.Fatalf("calls=%v", calls)
	}
}

func TestExpiredRevokedAndCrossPrivacyAuthorityFailBeforeFreshGuest(t *testing.T) {
	graph := workflow.Graph{SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: "authority-rejection", Nodes: []workflow.Node{
		{ID: "wait", Kind: workflow.Wait, VersionSHA256: digest('1')},
		{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: digest('2')},
	}}
	factory := &guestFactory{}
	valid := authority('a', "private-a")
	evaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: valid})
	if err != nil {
		t.Fatal(err)
	}
	first, err := evaluator.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	expired := valid
	expired.NotAfterUnixMS = 1
	expiredEvaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: expired})
	if err != nil {
		t.Fatal(err)
	}
	before := len(factory.guests)
	if _, err := expiredEvaluator.Resume(context.Background(), first.State); !errors.Is(err, workflow.ErrAuthorityUnavailable) {
		t.Fatalf("expired resume error=%v", err)
	}
	revoked := valid
	revoked.Revoked = true
	revokedEvaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: revoked})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revokedEvaluator.Resume(context.Background(), first.State); !errors.Is(err, workflow.ErrAuthorityUnavailable) {
		t.Fatalf("revoked resume error=%v", err)
	}
	crossPrivacy := valid
	crossPrivacy.PrivacyPartition = "private-b"
	crossPrivacyEvaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true, Authority: crossPrivacy})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crossPrivacyEvaluator.Resume(context.Background(), first.State); !errors.Is(err, workflow.ErrAuthorityMismatch) {
		t.Fatalf("cross-privacy resume error=%v", err)
	}
	if len(factory.guests) != before {
		t.Fatalf("rejected resumes created guests: before=%d after=%d", before, len(factory.guests))
	}
	tampered := first.State
	tampered.Authority.PlanSHA256 = "tampered"
	if _, err := evaluator.Resume(context.Background(), tampered); !errors.Is(err, workflow.ErrInvalidState) {
		t.Fatalf("tampered resume error=%v", err)
	}
}

func assertNoHiddenState(t *testing.T, state workflow.State) {
	t.Helper()
	encoded, err := state.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"frame", "heap", "globals", "file_descriptor", "wasm_memory", "guest_id", "/tmp"} {
		if contains(string(encoded), forbidden) {
			t.Fatalf("state leaks %q: %s", forbidden, encoded)
		}
	}
}

type guestFactory struct {
	mu     sync.Mutex
	guests []*fixtureGuest
}

func (factory *guestFactory) NewGuest(context.Context) (workflow.Guest, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	guest := &fixtureGuest{id: len(factory.guests) + 1}
	factory.guests = append(factory.guests, guest)
	return guest, nil
}

type fixtureGuest struct {
	id     int
	closed bool
}

func (guest *fixtureGuest) Close(context.Context) error { guest.closed = true; return nil }

func contains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}

func authority(character byte, privacy string) workflow.AuthorityEnvelope {
	return workflow.AuthorityEnvelope{
		SchemaVersion: workflow.AuthorityEnvelopeSchemaVersion,
		PlanSHA256:    digest(character), GrantSetSHA256: digest(character + 1),
		PrivacyPartition: privacy, EpochSHA256: digest(character + 2), NotAfterUnixMS: 1 << 62,
	}
}

func digest(character byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = character
	}
	return "sha256:" + string(value)
}
