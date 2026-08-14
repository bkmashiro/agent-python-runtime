package agentfunction_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestExecuteGuestRejectsUnshareableRunnerBeforeRun(t *testing.T) {
	invocation, request := guestInvocation()
	base := engine.Properties{
		Backend: "wazero", ArtifactSHA256: invocation.ArtifactSHA256,
		ExecutionProfileBindingSHA256: invocation.ExecutionProfileSHA256,
		DeterministicProfileSHA256:    invocation.DeterministicSettingsSHA256,
		AvailableImports:              []string{"sys"},
		QualifiedImports:              []string{"sys"},
	}
	for name, mutate := range map[string]func(*engine.Properties){
		"wrong artifact":           func(properties *engine.Properties) { properties.ArtifactSHA256 = digest('0') },
		"workspace":                func(properties *engine.Properties) { properties.WorkspaceMounted = true },
		"broker":                   func(properties *engine.Properties) { properties.CapabilityBrokerAvailable = true },
		"no deterministic profile": func(properties *engine.Properties) { properties.DeterministicProfileSHA256 = "" },
		"wrong import closure":     func(properties *engine.Properties) { properties.QualifiedImports = []string{"json"} },
	} {
		t.Run(name, func(t *testing.T) {
			properties := base
			mutate(&properties)
			runner := &probeRunner{properties: properties}
			compute := agentfunction.FreshGuestCompute{
				NewRunner:      func(context.Context) (string, engine.Runner, error) { return "physical-1", runner, nil },
				Request:        request,
				MaxResultBytes: 16,
				DecodeResult:   func(value []byte) ([]byte, error) { return value, nil },
			}
			result, err := (agentfunction.Engine{}).ExecuteGuest(context.Background(), invocation, compute)
			if !errors.Is(err, agentfunction.ErrGuestNotShareable) || len(result.Value) != 0 || runner.runs.Load() != 0 || runner.closes.Load() != 1 {
				t.Fatalf("result=%+v err=%v runs=%d closes=%d", result, err, runner.runs.Load(), runner.closes.Load())
			}
		})
	}
}

func TestExecuteGuestRejectsRequestIdentityMismatchBeforeRunnerCreation(t *testing.T) {
	invocation, _ := guestInvocation()
	var created atomic.Int32
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) {
			created.Add(1)
			return "", nil, nil
		},
		Request: []byte(`{"run_id":"mismatch","code":"result = 2","inputs":{"value":1}}`), MaxResultBytes: 16,
		DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	}
	result, err := (agentfunction.Engine{}).ExecuteGuest(context.Background(), invocation, compute)
	if !errors.Is(err, agentfunction.ErrGuestIdentity) || len(result.Value) != 0 || created.Load() != 0 {
		t.Fatalf("result=%+v err=%v created=%d", result, err, created.Load())
	}
}

func TestExecuteGuestRejectsOutputSchemaIdentityMismatch(t *testing.T) {
	invocation, _ := guestInvocation()
	request := []byte(`{"run_id":"guest","code":"result = 1","inputs":{"value":1},"output_schema":{"type":"integer"}}`)
	created := atomic.Int32{}
	result, err := (agentfunction.Engine{}).ExecuteGuest(context.Background(), invocation, agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) { created.Add(1); return "", nil, nil },
		Request:   request, MaxResultBytes: 16, DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	})
	if !errors.Is(err, agentfunction.ErrGuestIdentity) || len(result.Value) != 0 || created.Load() != 0 {
		t.Fatalf("result=%+v err=%v created=%d", result, err, created.Load())
	}
}

func TestFreshGuestComputeClosesPartiallyCreatedRunnerOnFactoryError(t *testing.T) {
	runner := &probeRunner{}
	_, err := (agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) {
			return "physical-partial", runner, errors.New("factory failed")
		},
		Request: []byte(`{"run_id":"partial","code":"result = 1","inputs":{}}`), MaxResultBytes: 16,
		DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	}).Run(context.Background(), &agentfunction.Guard{})
	if !errors.Is(err, agentfunction.ErrInvalidGuestCompute) || runner.runs.Load() != 0 || runner.closes.Load() != 1 {
		t.Fatalf("err=%v runs=%d closes=%d", err, runner.runs.Load(), runner.closes.Load())
	}
}

func TestFreshGuestComputeClosesRunnerAfterPanic(t *testing.T) {
	runner := &probeRunner{panicRun: true}
	defer func() {
		if recovered := recover(); recovered == nil || runner.closes.Load() != 1 {
			t.Fatalf("recovered=%v closes=%d", recovered, runner.closes.Load())
		}
	}()
	_, _ = (agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) { return "physical-panic", runner, nil },
		Request:   []byte(`{"run_id":"panic","code":"result = 1","inputs":{}}`), MaxResultBytes: 16,
		DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	}).Run(context.Background(), &agentfunction.Guard{})
}

func TestFreshGuestComputeRejectsSuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &probeRunner{cancelRun: cancel, payload: []byte("result")}
	value, err := (agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) { return "physical-cancel", runner, nil },
		Request:   []byte(`{"run_id":"cancel","code":"result = 1","inputs":{}}`), MaxResultBytes: 16,
		DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	}).Run(ctx, &agentfunction.Guard{})
	if !errors.Is(err, context.Canceled) || len(value) != 0 || runner.closes.Load() != 1 {
		t.Fatalf("value=%q err=%v closes=%d", value, err, runner.closes.Load())
	}
}

func TestExecuteGuestIsDomainSeparatedFromCallbackFlights(t *testing.T) {
	invocation, request := guestInvocation()
	flights := agentfunction.NewFlightGroup()
	functionEngine := agentfunction.Engine{Flights: flights}
	started := make(chan struct{})
	release := make(chan struct{})
	callbackDone := make(chan error, 1)
	go func() {
		_, err := functionEngine.Execute(context.Background(), invocation, func(context.Context, *agentfunction.Guard) ([]byte, error) {
			close(started)
			<-release
			return []byte("callback"), nil
		})
		callbackDone <- err
	}()
	<-started
	runner := &probeRunner{properties: engine.Properties{
		Backend: "wazero", ArtifactSHA256: invocation.ArtifactSHA256,
		ExecutionProfileBindingSHA256: invocation.ExecutionProfileSHA256,
		DeterministicProfileSHA256:    invocation.DeterministicSettingsSHA256,
		AvailableImports:              []string{"sys"}, QualifiedImports: []string{"sys"},
	}}
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) { return "physical-guest", runner, nil },
		Request:   request, MaxResultBytes: 16, DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	}
	result, err := functionEngine.ExecuteGuest(context.Background(), invocation, compute)
	close(release)
	if err != nil || string(result.Value) != "result" || result.PhysicalExecutionID != "physical-guest" || runner.runs.Load() != 1 {
		t.Fatalf("result=%+v err=%v runs=%d", result, err, runner.runs.Load())
	}
	if err := <-callbackDone; err != nil {
		t.Fatal(err)
	}
}

func TestExecuteGuestRejectsCompletedRetention(t *testing.T) {
	invocation, request := guestInvocation()
	result, err := (agentfunction.Engine{CacheEnabled: true}).ExecuteGuest(context.Background(), invocation, agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) { return "", nil, nil },
		Request:   request, MaxResultBytes: 16, DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	})
	if !errors.Is(err, agentfunction.ErrGuestRetention) || len(result.Value) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestExecuteQualifiedGuestCollapsesAndRetainsExactResult(t *testing.T) {
	invocation, request := guestInvocation()
	request = qualifiedGuestRequest(t, request)
	qualified := qualifiedGuestInvocation(t, invocation, request)
	store, err := agentfunction.NewBoundedStore(t.TempDir(), invocation.ProjectSHA256, 16, 4096)
	if err != nil {
		t.Fatal(err)
	}
	flights := agentfunction.NewFlightGroup()
	functionEngine := agentfunction.Engine{Store: store, CacheEnabled: true, Flights: flights}
	properties := engine.Properties{
		Backend: "wazero", ArtifactSHA256: invocation.ArtifactSHA256,
		ExecutionProfileBindingSHA256: invocation.ExecutionProfileSHA256,
		DeterministicProfileSHA256:    invocation.DeterministicSettingsSHA256,
		AvailableImports:              []string{"sys"}, QualifiedImports: []string{"sys"},
	}
	var created atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	once := sync.Once{}
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) {
			created.Add(1)
			once.Do(func() { close(started) })
			<-release
			return "physical-qualified", &probeRunner{properties: properties, payload: []byte(`{"status":"ok","result":{"v":1}}`)}, nil
		},
		Request: request, MaxResultBytes: 16,
		DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	}
	type outcome struct {
		result agentfunction.Result
		err    error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		result, err := functionEngine.ExecuteQualifiedGuest(context.Background(), qualified, compute)
		outcomes <- outcome{result, err}
	}()
	<-started
	go func() {
		result, err := functionEngine.ExecuteQualifiedGuest(context.Background(), qualified, compute)
		outcomes <- outcome{result, err}
	}()
	deadline := time.Now().Add(time.Second)
	for flights.Stats().Waiters != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if flights.Stats().Waiters != 1 {
		t.Fatal("second invocation did not join flight")
	}
	close(release)
	seen := map[agentfunction.Disposition]int{}
	for range 2 {
		outcome := <-outcomes
		if outcome.err != nil || string(outcome.result.Value) != `{"v":1}` {
			t.Fatalf("result=%+v err=%v", outcome.result, outcome.err)
		}
		seen[outcome.result.Disposition]++
	}
	if seen[agentfunction.Leader] != 1 || seen[agentfunction.Waiter] != 1 || created.Load() != 1 {
		t.Fatalf("dispositions=%v created=%d", seen, created.Load())
	}
	retained, err := functionEngine.ExecuteQualifiedGuest(context.Background(), qualified, compute)
	if err != nil || retained.Disposition != agentfunction.Retained || retained.PhysicalExecutionID != "" || created.Load() != 1 {
		t.Fatalf("retained=%+v err=%v created=%d", retained, err, created.Load())
	}
	small := compute
	small.MaxResultBytes = 1
	if result, err := functionEngine.ExecuteQualifiedGuest(context.Background(), qualified, small); !errors.Is(err, agentfunction.ErrGuestResultTooLarge) || len(result.Value) != 0 || created.Load() != 1 {
		t.Fatalf("small-limit=%+v err=%v created=%d", result, err, created.Load())
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := functionEngine.ExecuteQualifiedGuest(cancelled, qualified, compute); !errors.Is(err, context.Canceled) || len(result.Value) != 0 || created.Load() != 1 {
		t.Fatalf("cancelled=%+v err=%v created=%d", result, err, created.Load())
	}
	var changedEnvelope map[string]any
	if err := json.Unmarshal(request, &changedEnvelope); err != nil {
		t.Fatal(err)
	}
	changedEnvelope["compatibility"] = map[string]any{"profile": "base", "imports": []string{"sys"}}
	changedRequest, err := json.Marshal(changedEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	changedCompute := compute
	changedCompute.Request = changedRequest
	changed, err := functionEngine.ExecuteQualifiedGuest(context.Background(), qualified, changedCompute)
	if !errors.Is(err, agentfunction.ErrGuestQualification) || len(changed.Value) != 0 || created.Load() != 1 {
		t.Fatalf("changed-contract=%+v err=%v created=%d", changed, err, created.Load())
	}
	key, _, err := qualified.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Directory(), cacheStorageFilename("fresh-guest", key)), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	recomputed, err := functionEngine.ExecuteQualifiedGuest(context.Background(), qualified, compute)
	if err != nil || recomputed.CacheHit || string(recomputed.Value) != `{"v":1}` || created.Load() != 2 {
		t.Fatalf("recomputed=%+v err=%v created=%d", recomputed, err, created.Load())
	}
	if err := store.EvictQualified(qualified); err != nil {
		t.Fatal(err)
	}
	afterEviction, err := functionEngine.ExecuteQualifiedGuest(context.Background(), qualified, compute)
	if err != nil || afterEviction.CacheHit || string(afterEviction.Value) != `{"v":1}` || created.Load() != 3 {
		t.Fatalf("after-eviction=%+v err=%v created=%d", afterEviction, err, created.Load())
	}
	stats := store.Stats()
	if stats.Hits != 2 || stats.Writes != 3 || stats.Corruptions != 1 || stats.Evictions != 1 {
		t.Fatalf("store stats=%+v", stats)
	}
}

func TestQualifiedGuestFailuresNeverPublishRetention(t *testing.T) {
	for name, fixture := range map[string]struct {
		hostCall bool
		runErr   error
		panicRun bool
		payload  []byte
	}{
		"host call": {hostCall: true},
		"run error": {runErr: errors.New("trap")},
		"cancelled": {runErr: context.Canceled},
		"timeout":   {runErr: context.DeadlineExceeded},
		"oom":       {runErr: errors.New("MemoryError")},
		"panic":     {panicRun: true},
		"decode":    {payload: []byte("not-json")},
		"oversized": {payload: []byte(`{"status":"ok","result":"12345678901234567"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			invocation, request := guestInvocation()
			request = qualifiedGuestRequest(t, request)
			qualified := qualifiedGuestInvocation(t, invocation, request)
			store, err := agentfunction.NewBoundedStore(t.TempDir(), invocation.ProjectSHA256, 16, 4096)
			if err != nil {
				t.Fatal(err)
			}
			properties := engine.Properties{
				Backend: "wazero", ArtifactSHA256: invocation.ArtifactSHA256,
				ExecutionProfileBindingSHA256: invocation.ExecutionProfileSHA256,
				DeterministicProfileSHA256:    invocation.DeterministicSettingsSHA256,
				AvailableImports:              []string{"sys"}, QualifiedImports: []string{"sys"},
			}
			var created atomic.Int32
			payload := fixture.payload
			if payload == nil {
				payload = []byte(`{"status":"ok","result":{"v":1}}`)
			}
			compute := agentfunction.FreshGuestCompute{
				NewRunner: func(context.Context) (string, engine.Runner, error) {
					created.Add(1)
					return fmt.Sprintf("physical-%d", created.Load()), &probeRunner{
						properties: properties, hostCall: fixture.hostCall, runErr: fixture.runErr,
						panicRun: fixture.panicRun, payload: payload,
					}, nil
				},
				Request: request, MaxResultBytes: 16,
				DecodeResult: func([]byte) ([]byte, error) {
					t.Fatal("qualified path must use the fixed decoder")
					return nil, nil
				},
			}
			functionEngine := agentfunction.Engine{Store: store, CacheEnabled: true, Flights: agentfunction.NewFlightGroup()}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := functionEngine.ExecuteQualifiedGuest(context.Background(), qualified, compute)
				if err == nil || len(result.Value) != 0 {
					t.Fatalf("attempt=%d result=%+v err=%v", attempt, result, err)
				}
			}
			if created.Load() != 2 || store.Stats().Writes != 0 || store.Stats().Hits != 0 {
				t.Fatalf("created=%d stats=%+v", created.Load(), store.Stats())
			}
		})
	}
}

func TestExecuteQualifiedGuestRequiresCompleteQualification(t *testing.T) {
	invocation, request := guestInvocation()
	result, err := (agentfunction.Engine{CacheEnabled: true}).ExecuteQualifiedGuest(context.Background(), agentfunction.QualifiedGuestInvocation{}, agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) { return "", nil, nil },
		Request:   request, MaxResultBytes: 16, DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	})
	if !errors.Is(err, agentfunction.ErrGuestQualification) || len(result.Value) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	qualified := qualifiedGuestInvocation(t, invocation, qualifiedGuestRequest(t, request))
	result, err = (agentfunction.Engine{CacheEnabled: true}).ExecuteQualifiedGuest(context.Background(), qualified, agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) {
			t.Fatal("runner must not start")
			return "", nil, nil
		},
		Request: request, MaxResultBytes: 16, DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	})
	if !errors.Is(err, agentfunction.ErrGuestQualification) || len(result.Value) != 0 {
		t.Fatalf("source-contract result=%+v err=%v", result, err)
	}
}

func qualifiedGuestInvocation(t *testing.T, invocation agentfunction.Invocation, request []byte) agentfunction.QualifiedGuestInvocation {
	t.Helper()
	analysis := semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion,
		SourceSHA256:  invocation.FunctionSourceSHA256, ASTSHA256: digest('a'), AnalyzerSHA256: digest('b'),
		ArtifactSHA256: invocation.ArtifactSHA256, ExecutionProfileSHA256: invocation.ExecutionProfileSHA256,
		ImportClosureSHA256: invocation.ImportClosureSHA256, CapabilityPlanSHA256: digest('c'),
		ModuleSpan: semantic.SourceSpan{StartLine: 1, EndLine: 1},
		Functions:  []semantic.FunctionSummary{}, Barriers: []semantic.Barrier{},
	}
	verified := verifiedSemanticPlanFor(t, invocation, analysis)
	qualified, err := agentfunction.NewQualifiedGuestInvocation(invocation, verified, request)
	if err != nil {
		t.Fatal(err)
	}
	return qualified
}

func qualifiedGuestRequest(t *testing.T, request []byte) []byte {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(request, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["compatibility"] = map[string]any{"profile": "base", "imports": []string{}}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func guestInvocation() (agentfunction.Invocation, []byte) {
	invocation := cacheableInvocation()
	code := "result = 1"
	digest := sha256.Sum256([]byte(code))
	invocation.FunctionSourceSHA256 = fmt.Sprintf("sha256:%x", digest[:])
	invocation.CanonicalInputs = []byte(`{"value":1}`)
	emptySchema := sha256.Sum256(nil)
	invocation.OutputSchemaSHA256 = fmt.Sprintf("sha256:%x", emptySchema[:])
	invocation.ImportClosureSHA256 = agentfunction.ImportClosureIdentity([]string{"sys"}, []string{"sys"})
	request := []byte(`{"run_id":"guest","code":"result = 1","inputs":{"value":1}}`)
	return invocation, request
}

type probeRunner struct {
	properties engine.Properties
	runs       atomic.Int32
	closes     atomic.Int32
	panicRun   bool
	hostCall   bool
	runErr     error
	payload    []byte
	cancelRun  context.CancelFunc
}

func (runner *probeRunner) Run(ctx context.Context, _ []byte, _ string) ([]byte, error) {
	runner.runs.Add(1)
	if runner.hostCall {
		engine.MarkHostCallAttempt(ctx)
	}
	if runner.panicRun {
		panic("runner panic")
	}
	if runner.runErr != nil {
		return nil, runner.runErr
	}
	if runner.cancelRun != nil {
		runner.cancelRun()
	}
	if runner.payload != nil {
		return append([]byte(nil), runner.payload...), nil
	}
	return []byte("result"), nil
}
func (runner *probeRunner) Close(context.Context) error   { runner.closes.Add(1); return nil }
func (runner *probeRunner) Properties() engine.Properties { return runner.properties }
