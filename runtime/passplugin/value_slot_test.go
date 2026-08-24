package passplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
)

type valueSlotTransformer struct {
	applied bool
}

func (transformer valueSlotTransformer) TransformSourcePass(_ context.Context, raw []byte) ([]byte, error) {
	var request sourcepatch.Request
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	patch := sourcepatch.Patch{
		SchemaVersion:        sourcepatch.SchemaVersion,
		Status:               "not_applicable",
		PassName:             request.PassName,
		PassVersion:          request.PassVersion,
		RegistrationSHA256:   request.RegistrationSHA256,
		OriginalSourceSHA256: valueSlotDigest(request.Source),
		OriginalASTSHA256:    valueSlotDigest("original-ast"),
	}
	if transformer.applied {
		patch.Status = "applied"
		patch.DerivedSource = "result = __pysolate_materialize_slot__('slot-numpy-sum-v1')\n"
		patch.DerivedSourceSHA256 = valueSlotDigest(patch.DerivedSource)
		patch.DerivedASTSHA256 = valueSlotDigest("derived-ast")
		patch.ReplacementCount = 1
	}
	return json.Marshal(patch)
}

type valueSlotRunner struct {
	baselineRuns int
	derivedRuns  int
	closed       int
}

type invalidValueSlotPlugin struct {
	registration passregistration.Registration
}

func (plugin invalidValueSlotPlugin) Registration() passregistration.Registration {
	return plugin.registration
}

func (invalidValueSlotPlugin) ValueSlotBound() bool { return true }

func (invalidValueSlotPlugin) Transform(context.Context, sourcepatch.Transformer, string) (sourcepatch.Patch, error) {
	return sourcepatch.Patch{Status: "applied"}, nil
}

func (runner *valueSlotRunner) Run(context.Context, []byte, string) ([]byte, error) {
	runner.baselineRuns++
	return []byte("baseline"), nil
}

func (runner *valueSlotRunner) RunValueSlotSourcePatchDerived(context.Context, []byte, sourcepatch.Patch, passregistration.Registration) (ValueSlotRun, error) {
	runner.derivedRuns++
	return ValueSlotRun{Payload: []byte("derived"), Applied: true}, nil
}

func (runner *valueSlotRunner) Close(context.Context) error {
	runner.closed++
	return nil
}

func TestExecuteValueSlotSelectsBeforeFactoryAndFallsBackBeforeEffects(t *testing.T) {
	pass, err := sourcepatch.NewDataLocalNumpySum(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := New(pass)
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(sourcepatch.DataLocalNumpySumName)
	if err != nil {
		t.Fatal(err)
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "value-slot-order", Code: "result = 1\n", Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("not applicable never constructs selected runner", func(t *testing.T) {
		baseline := &valueSlotRunner{}
		selectedFactories := 0
		execution, runErr := registry.ExecuteValueSlot(
			context.Background(), sourcepatch.DataLocalNumpySumName, valueSlotTransformer{applied: false},
			func(context.Context) (ValueSlotSourcePatchRunner, error) { return baseline, nil },
			func(context.Context) (ValueSlotSourcePatchRunner, error) {
				selectedFactories++
				return &valueSlotRunner{}, nil
			},
			request, "",
		)
		if runErr != nil || string(execution.Payload) != "baseline" || execution.Applied || selectedFactories != 0 || baseline.baselineRuns != 1 || baseline.closed != 1 {
			t.Fatalf("execution=%+v err=%v selected_factories=%d baseline=%+v", execution, runErr, selectedFactories, baseline)
		}
	})

	t.Run("selected factory failure falls back", func(t *testing.T) {
		baseline := &valueSlotRunner{}
		producerErr := errors.New("producer rejected revision")
		execution, runErr := registry.ExecuteValueSlot(
			context.Background(), sourcepatch.DataLocalNumpySumName, valueSlotTransformer{applied: true},
			func(context.Context) (ValueSlotSourcePatchRunner, error) { return baseline, nil },
			func(context.Context) (ValueSlotSourcePatchRunner, error) { return nil, producerErr },
			request, "",
		)
		if runErr != nil || string(execution.Payload) != "baseline" || execution.Applied || !errors.Is(execution.PassError, producerErr) || baseline.baselineRuns != 1 || baseline.closed != 1 {
			t.Fatalf("execution=%+v err=%v baseline=%+v", execution, runErr, baseline)
		}
	})

	t.Run("applied patch constructs only selected runner", func(t *testing.T) {
		baselineFactories := 0
		selected := &valueSlotRunner{}
		execution, runErr := registry.ExecuteValueSlot(
			context.Background(), sourcepatch.DataLocalNumpySumName, valueSlotTransformer{applied: true},
			func(context.Context) (ValueSlotSourcePatchRunner, error) {
				baselineFactories++
				return &valueSlotRunner{}, nil
			},
			func(context.Context) (ValueSlotSourcePatchRunner, error) { return selected, nil },
			request, "",
		)
		if runErr != nil || string(execution.Payload) != "derived" || !execution.Applied || baselineFactories != 0 || selected.derivedRuns != 1 || selected.closed != 1 {
			t.Fatalf("execution=%+v err=%v baseline_factories=%d selected=%+v", execution, runErr, baselineFactories, selected)
		}
	})

	t.Run("invalid applied patch records fallback reason", func(t *testing.T) {
		invalidRegistry, createErr := New(invalidValueSlotPlugin{registration: pass.Registration()})
		if createErr != nil {
			t.Fatal(createErr)
		}
		invalidRegistry, createErr = invalidRegistry.Enable(sourcepatch.DataLocalNumpySumName)
		if createErr != nil {
			t.Fatal(createErr)
		}
		baseline := &valueSlotRunner{}
		selectedFactories := 0
		execution, runErr := invalidRegistry.ExecuteValueSlot(
			context.Background(), sourcepatch.DataLocalNumpySumName, valueSlotTransformer{applied: true},
			func(context.Context) (ValueSlotSourcePatchRunner, error) { return baseline, nil },
			func(context.Context) (ValueSlotSourcePatchRunner, error) {
				selectedFactories++
				return &valueSlotRunner{}, nil
			},
			request, "",
		)
		if runErr != nil || string(execution.Payload) != "baseline" || execution.Applied ||
			!errors.Is(execution.PassError, sourcepatch.ErrInvalidPatch) || selectedFactories != 0 || baseline.closed != 1 {
			t.Fatalf("execution=%+v err=%v selected_factories=%d baseline=%+v", execution, runErr, selectedFactories, baseline)
		}
	})
}

func valueSlotDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}
