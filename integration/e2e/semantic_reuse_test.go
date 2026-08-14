package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/semanticreuse"
)

func TestRealGuestSemanticReuseCollapsesAndRetainsWholeRun(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	code := "result={'value':inputs['value']+1}"
	inputs := []byte(`{"value":41}`)
	invocation, runConfig := sharedGuestInvocation(t, artifact, code, []string{"sys"}, inputs)
	runConfig.Mechanisms = runtimeconfig.MechanismSet{
		ImmutableBranches: true, FunctionCache: true, SingleFlight: true,
		SemanticAnalysis: true, SemanticReuse: true,
	}
	analysisPhaseStarted := time.Now()
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, runConfig)
	if err != nil {
		t.Fatal(err)
	}
	analysisRequest, err := semantic.NewRequest(code, semantic.Bindings{
		ArtifactSHA256:         invocation.ArtifactSHA256,
		ExecutionProfileSHA256: invocation.ExecutionProfileSHA256,
		ImportClosureSHA256:    invocation.ImportClosureSHA256,
		CapabilityPlanSHA256:   semanticTestDigest('2'),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	verifiedAnalysis, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, analysisRunner), analysisRequest)
	analysis, reportErr := verifiedAnalysis.Analysis()
	if closeErr := analysisRunner.Close(context.Background()); err == nil {
		err = closeErr
	}
	if err == nil {
		err = reportErr
	}
	if err != nil {
		t.Fatal(err)
	}
	analysisPhaseMicros := time.Since(analysisPhaseStarted).Microseconds()
	dependencies, err := agentfunction.SemanticWholeRunDependencies(invocation)
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := semantic.BuildWholeRunPlan(analysis, semantic.WholeRunConfig{
		Dependencies: dependencies, InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil || !plan.Regions[0].Reusable() {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	verifiedPlan, err := semantic.BindVerifiedWholeRunPlan(verifiedAnalysis, plan)
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]any{
		"run_id": "semantic-reuse", "code": code,
		"inputs":        map[string]any{"value": 41},
		"compatibility": map[string]any{"profile": "base", "imports": []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := agentfunction.NewBoundedStore(t.TempDir(), invocation.ProjectSHA256, 1024, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	flights := agentfunction.NewFlightGroup()
	pass := &semanticreuse.Pass{Enabled: true, FunctionEngine: agentfunction.Engine{
		Store: store, CacheEnabled: true, Flights: flights,
	}}
	var physical atomic.Int32
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(ctx context.Context) (string, engine.Runner, error) {
			id := physical.Add(1)
			deadline := time.Now().Add(5 * time.Second)
			for flights.Stats().Waiters != 1 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			runner, createErr := (wazeroengine.Factory{}).New(ctx, artifact, runConfig)
			return fmt.Sprintf("semantic-physical-%d", id), runner, createErr
		},
		Request: request, MaxResultBytes: 1024, DecodeResult: decodeSuccessfulGuestResult,
	}
	type outcome struct {
		result agentfunction.Result
		err    error
	}
	outcomes := make(chan outcome, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	batchStarted := time.Now()
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			<-start
			result, executeErr := pass.ExecuteGuest(context.Background(), invocation, verifiedPlan, compute)
			outcomes <- outcome{result: result, err: executeErr}
		}()
	}
	close(start)
	wait.Wait()
	batchMicros := time.Since(batchStarted).Microseconds()
	close(outcomes)
	dispositions := map[agentfunction.Disposition]int{}
	for outcome := range outcomes {
		if outcome.err != nil || string(outcome.result.Value) != `{"value":42}` {
			t.Fatalf("result=%+v err=%v", outcome.result, outcome.err)
		}
		dispositions[outcome.result.Disposition]++
	}
	if physical.Load() != 1 || dispositions[agentfunction.Leader] != 1 || dispositions[agentfunction.Waiter] != 1 {
		t.Fatalf("physical=%d dispositions=%v", physical.Load(), dispositions)
	}
	retainedStarted := time.Now()
	retained, err := pass.ExecuteGuest(context.Background(), invocation, verifiedPlan, compute)
	retainedMicros := time.Since(retainedStarted).Microseconds()
	if err != nil || retained.Disposition != agentfunction.Retained || retained.PhysicalExecutionID != "" || physical.Load() != 1 {
		t.Fatalf("retained=%+v physical=%d err=%v", retained, physical.Load(), err)
	}
	stats := pass.Stats()
	if stats.Attempts != 3 || stats.Leaders != 1 || stats.Waiters != 1 || stats.Retained != 1 || stats.PhysicalComputes != 1 ||
		store.Stats().Writes != 1 || store.Stats().Hits != 1 {
		t.Fatalf("pass=%+v store=%+v", stats, store.Stats())
	}
	evidence, err := json.Marshal(map[string]any{
		"schema_version":          "pysolate.semantic-reuse-observation.v0",
		"analysis_phase_micros":   analysisPhaseMicros,
		"concurrent_batch_micros": batchMicros,
		"retained_hit_micros":     retainedMicros,
		"logical_invocations":     3,
		"physical_computes":       physical.Load(),
		"result_bytes":            len(retained.Value),
		"pass_stats":              stats,
		"store_stats":             store.Stats(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("semantic_reuse_observation=%s", evidence)
	disabled := &semanticreuse.Pass{FunctionEngine: pass.FunctionEngine}
	if _, err := disabled.ExecuteGuest(context.Background(), invocation, verifiedPlan, compute); !errorsIsReuseQualification(err) {
		t.Fatalf("disabled optimizer error=%v", err)
	}
}

func errorsIsReuseQualification(err error) bool {
	return err == semanticreuse.ErrReuseQualification
}
