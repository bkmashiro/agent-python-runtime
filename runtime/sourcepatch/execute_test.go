package sourcepatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

type transformFunc func(context.Context, []byte) ([]byte, error)

func (fn transformFunc) TransformSourcePass(ctx context.Context, request []byte) ([]byte, error) {
	return fn(ctx, request)
}

type executionRunner struct {
	ordinary, derived, plm int
	request                []byte
	derivedErr             error
	trustedPrepare         string
	registration           passregistration.Registration
	projections            []CapabilityProjection
}

func (runner *executionRunner) Run(_ context.Context, request []byte, prepare string) ([]byte, error) {
	runner.ordinary++
	runner.request = bytes.Clone(request)
	runner.trustedPrepare = prepare
	return []byte(`{"original":true}`), nil
}

func (runner *executionRunner) RunSourcePatchDerived(_ context.Context, request []byte, _ Patch, _ passregistration.Registration) ([]byte, error) {
	runner.derived++
	runner.request = bytes.Clone(request)
	return []byte(`{"derived":true}`), runner.derivedErr
}

func (runner *executionRunner) RunCapabilitySourcePatchInline(_ context.Context, request []byte, registration passregistration.Registration, prepare string, projections []CapabilityProjection) (Execution, error) {
	runner.plm++
	runner.request = bytes.Clone(request)
	runner.registration, runner.trustedPrepare, runner.projections = registration, prepare, projections
	return Execution{Payload: []byte(`{"plm":true}`), Applied: true}, runner.derivedErr
}

func TestTypedSourceExecutionFallbackStopsBeforeGuestExecution(t *testing.T) {
	cse, err := NewPureScalarCSE(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	fold, err := NewPureScalarFold(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "source-execution", Code: "seed = 7\nresult = seed * seed\n", Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("execution failed")
	for name, execute := range map[string]func(context.Context, Transformer, SourcePatchRunner, []byte) (Execution, error){
		"cse": cse.Execute, "fold": fold.Execute,
	} {
		for _, mode := range []string{"applied", "not_applicable", "transform_error", "derived_error"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				runner := &executionRunner{}
				if mode == "derived_error" {
					runner.derivedErr = failure
				}
				transformer := transformFunc(func(ctx context.Context, raw []byte) ([]byte, error) {
					if mode == "transform_error" {
						return nil, failure
					}
					fake := &fakeTransformer{}
					payload, err := fake.TransformSourcePass(ctx, raw)
					if err != nil || mode != "not_applicable" {
						return payload, err
					}
					var value Patch
					if err := json.Unmarshal(payload, &value); err != nil {
						return nil, err
					}
					value.Status, value.ReplacementCount = "not_applicable", 0
					value.DerivedSource, value.DerivedSourceSHA256 = "", ""
					return json.Marshal(value)
				})
				result, err := execute(context.Background(), transformer, runner, request)
				if !bytes.Equal(runner.request, request) {
					t.Fatal("original request changed")
				}
				if mode == "derived_error" {
					if !errors.Is(err, failure) || runner.derived != 1 || runner.ordinary != 0 {
						t.Fatalf("derived failure retried original: runner=%+v error=%v", runner, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if mode == "applied" {
					if !result.Applied || runner.derived != 1 || runner.ordinary != 0 {
						t.Fatalf("runner=%+v result=%+v", runner, result)
					}
				} else {
					if result.Applied || runner.derived != 0 || runner.ordinary != 1 {
						t.Fatalf("runner=%+v result=%+v", runner, result)
					}
					if (mode == "transform_error") != errors.Is(result.PassError, failure) {
						t.Fatalf("pass error=%v", result.PassError)
					}
				}
			})
		}
	}
}

func TestTypedSourceExecutionRejectsInvalidRequestBeforeTransform(t *testing.T) {
	pass, err := NewPureScalarCSE(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	runner := &executionRunner{}
	transformer := transformFunc(func(context.Context, []byte) ([]byte, error) {
		t.Fatal("invalid request reached transformer")
		return nil, nil
	})
	if _, err := pass.Execute(context.Background(), transformer, runner, []byte(`{`)); err == nil {
		t.Fatal("invalid request accepted")
	}
	if runner.ordinary+runner.derived != 0 {
		t.Fatal("invalid request executed")
	}
}

func TestTypedPLMExecutionUsesTheSameRunnerAndDoesNotRetry(t *testing.T) {
	pass, err := NewPLMCapabilityCalls(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("PLM execution failed")
	runner := &executionRunner{derivedErr: failure}
	request := []byte(`{"run_id":"plm"}`)
	projections := []CapabilityProjection{{Capability: "source.read", Module: "source", Method: "read", Arguments: []string{"path"}}}
	result, err := pass.Execute(context.Background(), runner, request, "trusted-plan", projections)
	if !errors.Is(err, failure) || runner.plm != 1 || runner.ordinary != 0 || runner.derived != 0 {
		t.Fatalf("runner=%+v err=%v", runner, err)
	}
	if !bytes.Equal(runner.request, request) || runner.trustedPrepare != "trusted-plan" || runner.registration.IdentitySHA256() != pass.Registration().IdentitySHA256() || !equalCapabilityProjections(runner.projections, projections) {
		t.Fatalf("PLM handoff changed: %+v", runner)
	}
	if !result.Applied {
		t.Fatal("runner outcome was altered")
	}
}
