package hermesbridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type fakeRunner struct {
	runs       int32
	run        func(context.Context, []byte) ([]byte, error)
	properties engine.Properties
}

func (runner *fakeRunner) Run(ctx context.Context, request []byte, trustedPrepare string) ([]byte, error) {
	atomic.AddInt32(&runner.runs, 1)
	if trustedPrepare != "" {
		return nil, errors.New("unexpected trusted prepare")
	}
	return runner.run(ctx, request)
}
func (runner *fakeRunner) Close(context.Context) error { return nil }
func (runner *fakeRunner) Properties() engine.Properties {
	if runner.properties.Backend != "" {
		return runner.properties
	}
	return engine.Properties{
		Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
		RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance,
	}
}

type fakeTrace struct {
	startErr, completeErr error
	started, completed    int
	completeContextErr    error
}

func (trace *fakeTrace) RuntimeStarted(context.Context, runtimeconfig.InvocationRef, string) (string, error) {
	trace.started++
	return "event-start", trace.startErr
}
func (trace *fakeTrace) RuntimeCompleted(ctx context.Context, _ string, _ runtimeconfig.ExecutionRef, _, _ string) error {
	trace.completed++
	trace.completeContextErr = ctx.Err()
	return trace.completeErr
}

func guestResponse(t *testing.T, ctx context.Context, result string) []byte {
	t.Helper()
	ref, ok := engine.InvocationRefFromContext(ctx)
	if !ok {
		t.Fatal("runner did not receive Host invocation ref")
	}
	executionRef := runtimeconfig.ExecutionRef{InvocationRef: ref, ExecutedCodeSHA256: digestString("result = inputs['left'] + inputs['right']")}
	payload, err := json.Marshal(runtimeconfig.RunResponse{
		Status: runtimeconfig.RunResponseOK, Result: json.RawMessage(result), Receipts: json.RawMessage(`[]`),
		Metrics: &runtimeconfig.RunMetrics{CapabilityCalls: 0, ResultBytes: uint32(len(result))}, ExecutionRef: &executionRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestServiceExecutesWithHostOwnedExecutionIdentity(t *testing.T) {
	trace := &fakeTrace{}
	runner := &fakeRunner{}
	runner.run = func(ctx context.Context, payload []byte) ([]byte, error) {
		request, err := runtimeconfig.DecodeRunRequest(payload)
		if err != nil {
			t.Fatal(err)
		}
		if request.RunID == "tool-call-1" || string(request.Inputs) != `{"left":19,"right":23}` {
			t.Fatalf("unexpected guest request: %#v", request)
		}
		return guestResponse(t, ctx, `42`), nil
	}
	service, err := NewService(runner, trace, func() (string, error) { return "execution-1", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response := service.Execute(context.Background(), validExecuteRequest())
	if response.Status != ResponseStatusOK || string(response.Result) != "42" || response.ExecutionRef == nil {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.ExecutionRef.ExecutionID != "execution-1" || response.ExecutionRef.InvocationID != "tool-call-1" {
		t.Fatalf("unexpected execution ref: %#v", response.ExecutionRef)
	}
	if trace.started != 1 || trace.completed != 1 {
		t.Fatalf("unexpected trace calls: %#v", trace)
	}
}

func TestServiceExecutesMatchingProfileManifest(t *testing.T) {
	trace := &fakeTrace{}
	runner := &fakeRunner{properties: engine.Properties{
		Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
		RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance,
		ExecutionProfileID: "base", AllowedImports: []string{"json"},
	}, run: func(ctx context.Context, payload []byte) ([]byte, error) {
		request, err := runtimeconfig.DecodeRunRequest(payload)
		if err != nil || request.Compatibility == nil || request.Compatibility.Profile != "base" {
			t.Fatalf("request=%+v err=%v", request, err)
		}
		return guestResponse(t, ctx, `42`), nil
	}}
	service, err := NewService(runner, trace, func() (string, error) { return "execution-profile-ok", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := validExecuteRequest()
	request.Compatibility = &runtimeconfig.CompatibilityDeclaration{Profile: "base", Imports: []string{"json.decoder"}}
	response := service.Execute(context.Background(), request)
	if response.Status != ResponseStatusOK || atomic.LoadInt32(&runner.runs) != 1 || trace.started != 1 || trace.completed != 1 {
		t.Fatalf("response=%+v runner=%d trace=%+v", response, runner.runs, trace)
	}
}

func TestServiceReportsUnsupportedWithoutStartingTraceOrRunner(t *testing.T) {
	trace := &fakeTrace{}
	runner := &fakeRunner{run: func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("runner must not be called")
	}}
	service, err := NewService(runner, trace, func() (string, error) { return "execution-unsupported", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := validExecuteRequest()
	request.Requirements = []runtimeconfig.RequiredFeature{runtimeconfig.RequiredFeaturePOSIX, runtimeconfig.RequiredFeatureBrowserRuntime}
	response := service.Execute(context.Background(), request)
	if response.Status != ResponseStatusError || response.Error == nil || response.Error.Code != "runtime_unsupported" || response.Outcome == nil ||
		!response.Outcome.EscalationRequired || response.Outcome.WorkspaceDisposition != runtimeconfig.WorkspaceNotStarted || response.Outcome.EffectDisposition != runtimeconfig.EffectsNotStarted ||
		len(response.Outcome.RequiredFeatures) != 2 || response.Outcome.RequiredFeatures[0] != runtimeconfig.RequiredFeatureBrowserRuntime || response.Outcome.RequiredFeatures[1] != runtimeconfig.RequiredFeaturePOSIX {
		t.Fatalf("response=%+v", response)
	}
	if atomic.LoadInt32(&runner.runs) != 0 || trace.started != 0 || trace.completed != 0 || response.ExecutionRef != nil {
		t.Fatalf("runner=%d trace=%+v execution_ref=%+v", runner.runs, trace, response.ExecutionRef)
	}
}

func TestServiceRejectsProfileBeforeStartingTraceOrRunner(t *testing.T) {
	trace := &fakeTrace{}
	runner := &fakeRunner{properties: engine.Properties{
		Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
		RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance,
		ExecutionProfileID: "base", AllowedImports: []string{"json"},
	}, run: func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("runner must not be called")
	}}
	service, err := NewService(runner, trace, func() (string, error) { return "execution-profile", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := validExecuteRequest()
	request.Compatibility = &runtimeconfig.CompatibilityDeclaration{Profile: "base", Imports: []string{"subprocess"}}
	response := service.Execute(context.Background(), request)
	if response.Status != ResponseStatusError || response.Error == nil || response.Error.Code != "profile_unsupported" || response.Outcome != nil {
		t.Fatalf("response=%+v", response)
	}
	if atomic.LoadInt32(&runner.runs) != 0 || trace.started != 0 || trace.completed != 0 || response.ExecutionRef != nil {
		t.Fatalf("runner=%d trace=%+v execution_ref=%+v", runner.runs, trace, response.ExecutionRef)
	}
}

func TestServiceRejectsIndeterminateSourceBeforeStartingTraceOrRunner(t *testing.T) {
	trace := &fakeTrace{}
	runner := &fakeRunner{properties: engine.Properties{
		Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
		RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance,
		ExecutionProfileID: "base", AllowedImports: []string{"json"},
	}, run: func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("runner must not be called")
	}}
	service, err := NewService(runner, trace, func() (string, error) { return "execution-profile", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := validExecuteRequest()
	request.Code = `result = __import__(inputs["module"])`
	request.Compatibility = &runtimeconfig.CompatibilityDeclaration{Profile: "base", Imports: []string{"json"}}
	response := service.Execute(context.Background(), request)
	if response.Status != ResponseStatusError || response.Error == nil || response.Error.Code != "profile_unsupported" || response.Outcome != nil {
		t.Fatalf("response=%+v", response)
	}
	if atomic.LoadInt32(&runner.runs) != 0 || trace.started != 0 || trace.completed != 0 || response.ExecutionRef != nil {
		t.Fatalf("runner=%d trace=%+v execution_ref=%+v", runner.runs, trace, response.ExecutionRef)
	}
}

func TestServiceRejectsForgedExecutionReference(t *testing.T) {
	runner := &fakeRunner{run: func(ctx context.Context, payload []byte) ([]byte, error) {
		response := guestResponse(t, ctx, `42`)
		var decoded runtimeconfig.RunResponse
		if err := json.Unmarshal(response, &decoded); err != nil {
			t.Fatal(err)
		}
		decoded.ExecutionRef.ExecutionID = "forged"
		return json.Marshal(decoded)
	}}
	service, err := NewService(runner, &fakeTrace{}, func() (string, error) { return "execution-1", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response := service.Execute(context.Background(), validExecuteRequest())
	if response.Status != ResponseStatusError || response.Error == nil || response.Error.Code != "execution_ref_mismatch" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestServiceRequiredTraceStartFailurePreventsExecution(t *testing.T) {
	runner := &fakeRunner{run: func(context.Context, []byte) ([]byte, error) { return nil, nil }}
	service, err := NewService(runner, &fakeTrace{startErr: errors.New("disk full")}, func() (string, error) { return "execution-1", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response := service.Execute(context.Background(), validExecuteRequest())
	if atomic.LoadInt32(&runner.runs) != 0 || response.Error == nil || response.Error.Code != "trace_required" {
		t.Fatalf("unexpected response/runs: %#v %d", response, runner.runs)
	}
}

func TestServiceRequiredTraceCompletionFailureFailsClosed(t *testing.T) {
	runner := &fakeRunner{run: func(ctx context.Context, payload []byte) ([]byte, error) { return guestResponse(t, ctx, `42`), nil }}
	service, err := NewService(runner, &fakeTrace{completeErr: errors.New("disk full")}, func() (string, error) { return "execution-1", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response := service.Execute(context.Background(), validExecuteRequest())
	if atomic.LoadInt32(&runner.runs) != 1 || response.Error == nil || response.Error.Code != "trace_required" || len(response.Result) != 0 {
		t.Fatalf("unexpected response/runs: %#v %d", response, runner.runs)
	}
}

func TestServiceAppliesHostTimeout(t *testing.T) {
	runner := &fakeRunner{run: func(ctx context.Context, payload []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	trace := &fakeTrace{}
	service, err := NewService(runner, trace, func() (string, error) { return "execution-1", nil }, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	response := service.Execute(context.Background(), validExecuteRequest())
	if response.Error == nil || response.Error.Code != "runtime_timeout" || trace.completeContextErr != nil {
		t.Fatalf("unexpected response/trace context: %#v %v", response, trace.completeContextErr)
	}
}
