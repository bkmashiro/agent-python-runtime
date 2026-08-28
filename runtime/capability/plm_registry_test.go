package capability_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestRegistryBindsPLMContractToExactHostValidatorIdentities(t *testing.T) {
	spec := plmRegistrySpec(capability.TemporalImmutable)
	contract := *spec.PLM
	adapter := &plmRegistryAdapter{identities: capability.PLMValidatorIdentities{
		Temporal: contract.TemporalValidator, ProviderNonInterference: contract.ProviderNonInterferenceValidator,
	}}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, basicGrant(t), adapter); err != nil {
		t.Fatalf("valid PLM adapter: %v", err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	sealed, ok := plan.PLMContract(spec.Name)
	if !ok || sealed.Version != capability.PLMContractVersionV1 || sealed.Resource.Argument != "path" {
		t.Fatalf("sealed=%+v ok=%t", sealed, ok)
	}
}

func TestRegistryRejectsAbsentOrMismatchedPLMValidators(t *testing.T) {
	spec := plmRegistrySpec(capability.TemporalImmutable)
	if err := capability.NewRegistry().Register(spec, basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"value"}`), nil
	})); err == nil {
		t.Fatal("PLM spec admitted without validator")
	}
	adapter := &plmRegistryAdapter{identities: capability.PLMValidatorIdentities{
		Temporal: "pysolate.test.wrong-temporal.v1", ProviderNonInterference: spec.PLM.ProviderNonInterferenceValidator,
	}}
	if err := capability.NewRegistry().Register(spec, basicGrant(t), adapter); err == nil {
		t.Fatal("PLM spec admitted with mismatched identity")
	}
}

func TestRegistryRequiresTransportPreparerForCurrentMode(t *testing.T) {
	spec := plmRegistrySpec(capability.TemporalCurrent)
	withoutTransport := &plmRegistryNoTransportAdapter{identities: capability.PLMValidatorIdentities{
		ProviderNonInterference: spec.PLM.ProviderNonInterferenceValidator,
	}}
	if err := capability.NewRegistry().Register(spec, basicGrant(t), withoutTransport); err == nil {
		t.Fatal("CURRENT admitted without transport preparer")
	}
	adapter := &plmRegistryAdapter{identities: withoutTransport.identities, transport: true}
	if err := capability.NewRegistry().Register(spec, basicGrant(t), adapter); err != nil {
		t.Fatalf("CURRENT transport adapter: %v", err)
	}
}

type plmRegistryNoTransportAdapter struct {
	identities capability.PLMValidatorIdentities
}

func (adapter *plmRegistryNoTransportAdapter) Call(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"text":"value"}`), nil
}

func (adapter *plmRegistryNoTransportAdapter) PLMValidatorIdentities() capability.PLMValidatorIdentities {
	return adapter.identities
}

func (adapter *plmRegistryNoTransportAdapter) ValidatePLM(context.Context, capability.PLMValidationRequest) (capability.PLMValidationResult, error) {
	return capability.PLMValidationResult{}, nil
}

type plmRegistryAdapter struct {
	identities capability.PLMValidatorIdentities
	transport  bool
}

func (adapter *plmRegistryAdapter) Call(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"text":"value"}`), nil
}

func (adapter *plmRegistryAdapter) PLMValidatorIdentities() capability.PLMValidatorIdentities {
	return adapter.identities
}

func (adapter *plmRegistryAdapter) ValidatePLM(context.Context, capability.PLMValidationRequest) (capability.PLMValidationResult, error) {
	return capability.PLMValidationResult{}, nil
}

func (adapter *plmRegistryAdapter) PreparePLMTransport(context.Context, json.RawMessage) error {
	if !adapter.transport {
		return capability.ErrPLMTransportUnavailable
	}
	return nil
}

func plmRegistrySpec(mode capability.TemporalMode) capability.Spec {
	spec := stagedTestSpec()
	spec.PreDispatch = nil
	contract := plmValueContract(mode)
	contract.Resource = capability.ResourceReference{Namespace: "workspace", Argument: "path"}
	if mode == capability.TemporalCurrent {
		contract.PrepareEffect = capability.PrepareTransportOnly
		contract.Speculation = capability.SpeculationNever
		contract.TemporalValidator = ""
		contract.MaxResultBytes = 0
	}
	spec.PLM = &contract
	return spec
}
