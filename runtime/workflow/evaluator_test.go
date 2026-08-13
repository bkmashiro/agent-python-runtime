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
	evaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true})
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
	evaluator, err := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true})
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
	evaluator, _ := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: true})
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
	evaluator, _ := workflow.New(workflow.Config{Graph: graph, Guests: factory, ResumeEnabled: false})
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

func digest(character byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = character
	}
	return "sha256:" + string(value)
}
