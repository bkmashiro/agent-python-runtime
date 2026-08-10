package workspacecapsule

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestOptionsValidateBounds(t *testing.T) {
	valid := DefaultOptions()
	valid.StressIterations = MaxStressIterations
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []int{-1, MaxStressIterations + 1} {
		invalid := DefaultOptions()
		invalid.StressIterations = value
		if err := invalid.validate(); err == nil {
			t.Fatalf("stress iterations %d accepted", value)
		}
	}
	invalidDelay := DefaultOptions()
	invalidDelay.CancellationBarrierTimeout = 0
	if err := invalidDelay.validate(); err == nil {
		t.Fatal("zero cancellation delay accepted")
	}
	invalidDelay.CancellationBarrierTimeout = 16 * time.Second
	if err := invalidDelay.validate(); err == nil {
		t.Fatal("unbounded cancellation barrier accepted")
	}
}

func TestVerifyRejectsEmptyArtifact(t *testing.T) {
	report, err := Verify(context.Background(), nil, runtimeconfig.DefaultRunConfig(), wazeroengine.Factory{})
	if err == nil || report.SchemaVersion != SchemaVersion || report.Status != StatusFailed {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestVerifyRejectsPreboundFactory(t *testing.T) {
	report, err := Verify(context.Background(), []byte("not reached"), runtimeconfig.DefaultRunConfig(), wazeroengine.Factory{
		WorkspaceManager: &workspace.Manager{}, WorkspaceRef: "ws-existing", WorkspaceOwner: "owner",
	})
	if err == nil || !strings.Contains(err.Error(), "bindings") || report.Status != StatusFailed {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestVerifyRejectsPreconfiguredBrokerFactory(t *testing.T) {
	report, err := Verify(context.Background(), []byte("not reached"), runtimeconfig.DefaultRunConfig(), wazeroengine.Factory{
		BrokerFactory: func(context.Context) (*capability.Broker, error) { return nil, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "bindings") || report.Status != StatusFailed {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestInterruptObservedClassifiesDeadlineAndCancellation(t *testing.T) {
	if !interruptObserved(nil, fmtWrap(context.DeadlineExceeded), context.DeadlineExceeded) {
		t.Fatal("wrapped deadline was not recognized")
	}
	if !interruptObserved([]byte(`{"error":{"message":"context canceled"}}`), nil, context.Canceled) {
		t.Fatal("payload cancellation was not recognized")
	}
	if interruptObserved([]byte(`{"status":"ok"}`), nil, context.Canceled) {
		t.Fatal("successful payload was misclassified")
	}
}

func TestRunStressReportsExactCompletion(t *testing.T) {
	runner := &stressRunner{failAt: -1}
	report := Report{}
	payloads := [][]byte{}
	sampler := &scriptedDescriptorSampler{values: []int{20, 20}}
	runStress(context.Background(), runner, 3, &report, &payloads, sampler.sample)
	if report.Stress == nil || report.Stress.CompletedIterations != 3 || len(report.Checks) != 5 || len(payloads) != 3 || report.Stress.OpenFDDelta == nil || *report.Stress.OpenFDDelta != 0 {
		t.Fatalf("report=%+v payloads=%d", report, len(payloads))
	}
	for _, check := range report.Checks {
		if check.Status != CheckPass {
			t.Fatalf("check=%+v", check)
		}
	}
}

func TestRunStressOmitsUnavailableDescriptorOracle(t *testing.T) {
	runner := &stressRunner{failAt: -1}
	report := Report{}
	payloads := [][]byte{}
	runStress(context.Background(), runner, 1, &report, &payloads, func() (int, bool) { return 0, false })
	if len(report.Checks) != 4 || report.Stress == nil || report.Stress.OpenFDsBefore != nil || report.Stress.OpenFDsAfter != nil || report.Stress.OpenFDDelta != nil {
		t.Fatalf("report=%+v", report)
	}
}

func TestRunStressFailsClosedAfterRunnerError(t *testing.T) {
	runner := &stressRunner{failAt: 2}
	report := Report{}
	payloads := [][]byte{}
	sampler := &scriptedDescriptorSampler{values: []int{20, 20}}
	runStress(context.Background(), runner, 5, &report, &payloads, sampler.sample)
	if report.Stress == nil || report.Stress.CompletedIterations != 2 || len(payloads) != 2 {
		t.Fatalf("report=%+v payloads=%d", report, len(payloads))
	}
	for _, check := range report.Checks {
		if check.Status != CheckFail {
			t.Fatalf("check silently passed after runner error: %+v", check)
		}
	}
}

type scriptedDescriptorSampler struct {
	values []int
	index  int
}

func (sampler *scriptedDescriptorSampler) sample() (int, bool) {
	if sampler.index >= len(sampler.values) {
		return 0, false
	}
	value := sampler.values[sampler.index]
	sampler.index++
	return value, true
}

type stressRunner struct {
	calls  int
	failAt int
}

func (runner *stressRunner) Run(context.Context, []byte, string) ([]byte, error) {
	if runner.calls == runner.failAt {
		return nil, errors.New("injected stress failure")
	}
	result, _ := json.Marshal(map[string]any{
		"before": runner.calls, "heap_continued": false, "tmp_continued": false,
	})
	runner.calls++
	response, _ := json.Marshal(runtimeconfig.RunResponse{
		Status: runtimeconfig.RunResponseOK, Result: result, Receipts: json.RawMessage(`[]`),
		Metrics: &runtimeconfig.RunMetrics{ResultBytes: uint32(len(result))},
	})
	return response, nil
}

func (*stressRunner) Close(context.Context) error           { return nil }
func (*stressRunner) Properties() enginecontract.Properties { return enginecontract.Properties{} }
func fmtWrap(err error) error                               { return &wrappedError{err: err} }

type wrappedError struct{ err error }

func (value *wrappedError) Error() string { return "wrapped: " + value.err.Error() }
func (value *wrappedError) Unwrap() error { return value.err }

func TestSanitizeErrorRemovesEveryHostPath(t *testing.T) {
	err := sanitizeError("probe", &pathError{"/private/base", "/private/source"}, "/private/base", "/private/source")
	if strings.Contains(err.Error(), "/private/") || !strings.Contains(err.Error(), "[HOST_PATH]") {
		t.Fatalf("unsanitized error: %v", err)
	}
}

type pathError struct {
	base   string
	source string
}

func (value *pathError) Error() string { return value.base + ": " + value.source }
