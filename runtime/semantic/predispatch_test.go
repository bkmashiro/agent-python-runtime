package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

func TestSemanticPreDispatchClaimsExactlyOnceAtUnchangedBrokerBoundary(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	decision := CanPreissue(verified, plan, site.ID, legalityContext())
	call, ok := decision.QualifiedCall()
	if !ok {
		t.Fatal("call not qualified")
	}
	budget, err := NewPreDispatchBudget(1)
	if err != nil {
		t.Fatal(err)
	}
	launcher := &queuedLauncher{}
	controller, err := NewSemanticPreDispatch(call, plan, budget)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(context.Background(), launcher); err != nil {
		t.Fatal(err)
	}
	if launcher.Count() != 1 {
		t.Fatalf("launch count=%d", launcher.Count())
	}
	launcher.RunAll()

	broker, err := capability.NewBroker(capability.Config{RunIdentity: "semantic-stage", Plan: plan, StagedClaimer: controller})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"dynamic-one","capability":"sources.read","arguments":{"key":"profile"}}`))
	if err != nil || !json.Valid(response) || !containsSemanticResult(response, "ok") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if _, err := controller.Claim(context.Background(), "sources.read", json.RawMessage(`{"key":"profile"}`)); !errors.Is(err, streaming.ErrStagedObservationConsumed) {
		t.Fatalf("second claim error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.PhysicalIssues != 1 || snapshot.PhysicalStarts != 1 || snapshot.PhysicalFinishes != 1 || snapshot.LogicalClaims != 1 || snapshot.RejectedClaims != 1 || snapshot.Disposition != streaming.ObservationConsumed {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSemanticPreDispatchBudgetAndMismatchFailClosed(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	firstDecision := CanPreissue(verified, plan, site.ID, legalityContext())
	first, _ := firstDecision.QualifiedCall()
	secondContext := legalityContext()
	secondContext.BudgetReservationSHA256 = legalityDigest("second-reservation")
	secondDecision := CanPreissue(verified, plan, site.ID, secondContext)
	second, _ := secondDecision.QualifiedCall()
	budget, _ := NewPreDispatchBudget(1)
	launcher := &queuedLauncher{}
	firstController, _ := NewSemanticPreDispatch(first, plan, budget)
	secondController, _ := NewSemanticPreDispatch(second, plan, budget)
	if err := firstController.Start(context.Background(), launcher); err != nil {
		t.Fatal(err)
	}
	if err := secondController.Start(context.Background(), launcher); !errors.Is(err, ErrPreDispatchBudgetExhausted) {
		t.Fatalf("second start error=%v", err)
	}
	launcher.RunAll()
	if _, err := firstController.Claim(context.Background(), "sources.read", json.RawMessage(`{"key":"other"}`)); !errors.Is(err, ErrPreDispatchClaimMismatch) {
		t.Fatalf("mismatch error=%v", err)
	}
	if snapshot := firstController.Snapshot(); snapshot.LogicalClaims != 0 || snapshot.RejectedClaims != 1 || snapshot.PhysicalIssues != 1 || snapshot.PhysicalStarts != 1 || snapshot.PhysicalFinishes != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestExecuteSemanticPreDispatchFinalizesUnclaimedFailures(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	decision := CanPreissue(verified, plan, site.ID, legalityContext())
	call, _ := decision.QualifiedCall()
	budget, _ := NewPreDispatchBudget(1)
	launcher := &queuedLauncher{}
	controller, _ := NewSemanticPreDispatch(call, plan, budget)
	runFailure := errors.New("runner failed before Broker creation")
	_, err := ExecuteSemanticPreDispatch(context.Background(), controller, launcher, func() ([]byte, error) {
		launcher.RunAll()
		return nil, runFailure
	})
	if !errors.Is(err, runFailure) {
		t.Fatalf("execute error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.Disposition != streaming.ObservationCancelled || snapshot.PhysicalFinishes != 1 || snapshot.LogicalClaims != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if err := controller.Finalize(false); err != nil {
		t.Fatalf("idempotent finalize error=%v", err)
	}
}

func TestExecuteSemanticPreDispatchCancelsRunningPhysicalReadOnRunFailure(t *testing.T) {
	physicalStarted := make(chan struct{})
	plan := legalityTestPlanWithHandler(t, true, capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(physicalStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	decision := CanPreissue(verified, plan, site.ID, legalityContext())
	call, _ := decision.QualifiedCall()
	budget, _ := NewPreDispatchBudget(1)
	controller, _ := NewSemanticPreDispatch(call, plan, budget)
	runFailure := errors.New("Guest failed")
	_, err := ExecuteSemanticPreDispatch(context.Background(), controller, goroutineTestLauncher{}, func() ([]byte, error) {
		<-physicalStarted
		return nil, runFailure
	})
	if !errors.Is(err, runFailure) {
		t.Fatalf("execute error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.Disposition != streaming.ObservationCancelled || snapshot.PhysicalStarts != 1 || snapshot.PhysicalFinishes != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSemanticPreDispatchUnclaimedResultHasTypedTerminalDisposition(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	decision := CanPreissue(verified, plan, site.ID, legalityContext())
	call, _ := decision.QualifiedCall()
	budget, _ := NewPreDispatchBudget(1)
	launcher := &queuedLauncher{}
	controller, _ := NewSemanticPreDispatch(call, plan, budget)
	if err := controller.Start(context.Background(), launcher); err != nil {
		t.Fatal(err)
	}
	launcher.RunAll()
	if err := controller.TerminateUnclaimed(streaming.ObservationOrphaned); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Claim(context.Background(), "sources.read", json.RawMessage(`{"key":"profile"}`)); !errors.Is(err, streaming.ErrStagedObservationTerminal) {
		t.Fatalf("claim after orphan error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.Disposition != streaming.ObservationOrphaned {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

type goroutineTestLauncher struct{}

func (goroutineTestLauncher) Launch(task func()) error {
	go task()
	return nil
}

type queuedLauncher struct {
	mu    sync.Mutex
	tasks []func()
}

func (launcher *queuedLauncher) Launch(task func()) error {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	launcher.tasks = append(launcher.tasks, task)
	return nil
}

func (launcher *queuedLauncher) Count() int {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	return len(launcher.tasks)
}

func (launcher *queuedLauncher) RunAll() {
	launcher.mu.Lock()
	tasks := append([]func(){}, launcher.tasks...)
	launcher.tasks = nil
	launcher.mu.Unlock()
	for _, task := range tasks {
		task()
	}
}

func containsSemanticResult(raw []byte, want string) bool {
	var response struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	return json.Unmarshal(raw, &response) == nil && response.Result.Value == want
}

var _ capability.StagedObservationClaimer = (*SemanticPreDispatch)(nil)
