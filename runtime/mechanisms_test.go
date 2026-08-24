package runtime_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestDefaultMechanismsAreAllOff(t *testing.T) {
	config := runtime.DefaultRunConfig()
	if config.ProgramSurface != runtime.ProgramSurfaceDirect {
		t.Fatalf("default program surface = %q", config.ProgramSurface)
	}
	if err := config.Mechanisms.Validate(); err != nil {
		t.Fatalf("default mechanisms: %v", err)
	}
	if got := config.Mechanisms.Enabled(); len(got) != 0 {
		t.Fatalf("default enabled mechanisms = %v", got)
	}
}

func TestFutureCallsDoNotRequireSemanticAnalyzer(t *testing.T) {
	if err := (runtime.MechanismSet{SplitPhaseCalls: true}).Validate(); err != nil {
		t.Fatalf("direct Future calls unexpectedly require an analyzer: %v", err)
	}
}

func TestValueSlotsDoNotRequireSemanticAnalyzer(t *testing.T) {
	if err := (runtime.MechanismSet{ValueSlots: true}).Validate(); err != nil {
		t.Fatalf("direct ValueSlots unexpectedly require an analyzer: %v", err)
	}
}

func TestMechanismDependenciesFailClosed(t *testing.T) {
	tests := []struct {
		name string
		set  runtime.MechanismSet
	}{
		{"streaming without private workspace", runtime.MechanismSet{Streaming: true}},
		{"staged observation without streaming", runtime.MechanismSet{StagedObservation: true}},
		{"semantic pre-dispatch without analysis", runtime.MechanismSet{SemanticPreDispatch: true, StagedObservation: true}},
		{"semantic pre-dispatch without staged observation", runtime.MechanismSet{SemanticPreDispatch: true, SemanticAnalysis: true}},
		{"fanout without streaming", runtime.MechanismSet{ImmutableBranches: true, ChildFanout: true}},
		{"fanout without branches", runtime.MechanismSet{Streaming: true, ChildFanout: true}},
		{"function cache without branches", runtime.MechanismSet{FunctionCache: true}},
		{"fresh reevaluation without agent functions", runtime.MechanismSet{FreshReevaluation: true}},
		{"cow without prepared runtime", runtime.MechanismSet{MemoryCOW: true}},
		{"cold continuation without cow", runtime.MechanismSet{ColdIOContinuation: true}},
		{"semantic reuse without analysis", runtime.MechanismSet{SingleFlight: true, SemanticReuse: true}},
		{"semantic reuse without reuse mechanism", runtime.MechanismSet{SemanticAnalysis: true, SemanticReuse: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.set.Validate(); !errors.Is(err, runtime.ErrInvalidMechanismSet) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestProgramSurfaceAndMechanismMustMatch(t *testing.T) {
	for _, candidate := range []runtime.RunConfig{
		func() runtime.RunConfig {
			value := runtime.DefaultRunConfig()
			value.ProgramSurface = runtime.ProgramSurfaceProgrammatic
			return value
		}(),
		func() runtime.RunConfig {
			value := runtime.DefaultRunConfig()
			value.ProgramSurface = runtime.ProgramSurfaceBoth
			return value
		}(),
		func() runtime.RunConfig {
			value := runtime.DefaultRunConfig()
			value.Mechanisms.ProgrammaticToolCalling = true
			return value
		}(),
	} {
		if err := candidate.Validate(); err == nil {
			t.Fatalf("invalid surface/mechanism pair accepted: %#v", candidate)
		}
	}
	for _, mode := range []runtime.ProgramSurfaceMode{runtime.ProgramSurfaceProgrammatic, runtime.ProgramSurfaceBoth} {
		candidate := runtime.DefaultRunConfig()
		candidate.ProgramSurface = mode
		candidate.Mechanisms.ProgrammaticToolCalling = true
		if err := candidate.Validate(); err != nil {
			t.Fatalf("valid mode %q rejected: %v", mode, err)
		}
	}
}

func TestProgrammaticToolsAndApprovalAreIndependent(t *testing.T) {
	approvalOnly := runtime.DefaultRunConfig()
	approvalOnly.Mechanisms.ApprovalSuspension = true
	if err := approvalOnly.Validate(); err != nil {
		t.Fatalf("approval-only config rejected: %v", err)
	}
	programmaticOnly := runtime.DefaultRunConfig()
	programmaticOnly.ProgramSurface = runtime.ProgramSurfaceProgrammatic
	programmaticOnly.Mechanisms.ProgrammaticToolCalling = true
	if err := programmaticOnly.Validate(); err != nil {
		t.Fatalf("programmatic-only config rejected: %v", err)
	}
}

func TestSingleFlightDoesNotRequireDurableCache(t *testing.T) {
	set := runtime.MechanismSet{SingleFlight: true}
	if err := set.Validate(); err != nil {
		t.Fatalf("retention-independent single-flight rejected: %v", err)
	}
}

func TestResolveMechanismsReportsSelectedFallbackAndOff(t *testing.T) {
	requested := runtime.MechanismSet{
		Streaming:          true,
		StagedObservation:  true,
		ImmutableBranches:  true,
		ChildFanout:        true,
		FunctionCache:      true,
		SingleFlight:       true,
		FreshReevaluation:  true,
		PreparedRuntime:    true,
		MemoryCOW:          true,
		PrivateWorkspace:   true,
		ColdIOContinuation: true,
		SemanticAnalysis:   true,
		SemanticReuse:      true,
		SplitPhaseCalls:    true,
		ValueSlots:         true,
	}
	available := requested
	available.ChildFanout = false
	available.MemoryCOW = false
	available.ColdIOContinuation = false
	available.SemanticReuse = false

	resolved, evidence, err := runtime.ResolveMechanisms(requested, available)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ChildFanout || resolved.MemoryCOW || resolved.ColdIOContinuation || resolved.SemanticReuse ||
		!resolved.Streaming || !resolved.SingleFlight || !resolved.SemanticAnalysis || !resolved.SplitPhaseCalls || !resolved.ValueSlots {
		t.Fatalf("unexpected resolved set: %#v", resolved)
	}
	if err := evidence.Validate(); err != nil {
		t.Fatalf("evidence invalid: %v", err)
	}
	if got := evidence.Disposition(runtime.MechanismChildFanout); got != runtime.MechanismFallback {
		t.Fatalf("child fanout disposition = %q", got)
	}
	if got := evidence.Disposition(runtime.MechanismMemoryCOW); got != runtime.MechanismFallback {
		t.Fatalf("memory COW disposition = %q", got)
	}
	if got := evidence.Disposition(runtime.MechanismColdIOContinuation); got != runtime.MechanismFallback {
		t.Fatalf("cold continuation disposition = %q", got)
	}
	if got := evidence.Disposition(runtime.MechanismSemanticReuse); got != runtime.MechanismFallback {
		t.Fatalf("semantic reuse disposition = %q", got)
	}
	if got := evidence.Disposition(runtime.MechanismSemanticAnalysis); got != runtime.MechanismSelected {
		t.Fatalf("semantic analysis disposition = %q", got)
	}
	if got := evidence.Disposition(runtime.MechanismStreaming); got != runtime.MechanismSelected {
		t.Fatalf("streaming disposition = %q", got)
	}
	if got := evidence.Disposition(runtime.MechanismPreparedRuntime); got != runtime.MechanismSelected {
		t.Fatalf("prepared disposition = %q", got)
	}

	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedKeys(envelope), []string{"mechanisms", "schema_version"}) {
		t.Fatalf("unexpected evidence fields: %v", sortedKeys(envelope))
	}
	if string(encoded) == "" || json.Valid(encoded) == false {
		t.Fatalf("invalid evidence JSON: %s", encoded)
	}
}

func TestMechanismEvidenceRejectsUnknownOrPrivateReason(t *testing.T) {
	requested := runtime.MechanismSet{Streaming: true, PrivateWorkspace: true}
	_, evidence, err := runtime.ResolveMechanisms(requested, runtime.MechanismSet{})
	if err != nil {
		t.Fatal(err)
	}
	for index := range evidence.Mechanisms {
		if evidence.Mechanisms[index].Name == runtime.MechanismStreaming {
			evidence.Mechanisms[index].Reason = "host path /Users/example/private was unavailable"
		}
	}
	if err := evidence.Validate(); !errors.Is(err, runtime.ErrInvalidMechanismEvidence) {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMechanismSetDoesNotMutateCapabilityGrants(t *testing.T) {
	config := runtime.DefaultRunConfig()
	config.CapabilityGrants["read"] = runtime.CapabilityGrant{Name: "read"}
	before := map[string]runtime.CapabilityGrant{"read": {Name: "read"}}
	config.Mechanisms = runtime.MechanismSet{Streaming: true, PrivateWorkspace: true, StagedObservation: true}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.CapabilityGrants, before) {
		t.Fatalf("mechanisms widened or changed grants: %#v", config.CapabilityGrants)
	}
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
