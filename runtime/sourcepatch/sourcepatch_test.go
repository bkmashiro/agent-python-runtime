package sourcepatch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

type fakeTransformer struct {
	request Request
}

type capabilityTransformer struct {
	request Request
}

func (fake *fakeTransformer) TransformSourcePass(_ context.Context, raw []byte) ([]byte, error) {
	if err := json.Unmarshal(raw, &fake.request); err != nil {
		return nil, err
	}
	derived := "seed = 7\nleft = seed * seed\nright = left       \nresult = right\n"
	patch := Patch{
		SchemaVersion: SchemaVersion, Status: "applied",
		PassName: fake.request.PassName, PassVersion: fake.request.PassVersion,
		RegistrationSHA256:   fake.request.RegistrationSHA256,
		OriginalSourceSHA256: digest([]byte(fake.request.Source)), OriginalASTSHA256: digest([]byte("original-ast")),
		DerivedSource: derived, DerivedSourceSHA256: digest([]byte(derived)), DerivedASTSHA256: digest([]byte("derived-ast")),
		ReplacementCount: 1,
	}
	return json.Marshal(patch)
}

func (fake *capabilityTransformer) TransformSourcePass(_ context.Context, raw []byte) ([]byte, error) {
	if err := json.Unmarshal(raw, &fake.request); err != nil {
		return nil, err
	}
	derived := "result = 1\n"
	patch := Patch{
		SchemaVersion: SchemaVersion, Status: "applied",
		PassName: fake.request.PassName, PassVersion: fake.request.PassVersion,
		RegistrationSHA256:   fake.request.RegistrationSHA256,
		OriginalSourceSHA256: digest([]byte(fake.request.Source)), OriginalASTSHA256: digest([]byte("original-ast")),
		DerivedSource: derived, DerivedSourceSHA256: digest([]byte(derived)), DerivedASTSHA256: digest([]byte("derived-ast")),
		ReplacementCount: 1, CapabilityProjections: fake.request.CapabilityProjections,
	}
	return json.Marshal(patch)
}

func TestPureScalarCSEIsAStaticWholeProgramPlugin(t *testing.T) {
	pass, err := NewPureScalarCSE(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registration := pass.Registration()
	if registration.Name() != PureScalarCSEName || registration.Stage() != passregistration.StageWholeProgramPatch ||
		registration.Consumer() != passregistration.ExecutionPatch {
		t.Fatalf("registration=%+v", registration)
	}
	source := "seed = 7\nleft = seed * seed\nright = seed * seed\nresult = right\n"
	transformer := &fakeTransformer{}
	patch, err := pass.Transform(context.Background(), transformer, source)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Status != "applied" || patch.ReplacementCount != 1 || transformer.request.Source != source ||
		transformer.request.RegistrationSHA256 != registration.IdentitySHA256() {
		t.Fatalf("request=%+v patch=%+v", transformer.request, patch)
	}
}

func TestPureScalarFoldIsAStaticWholeProgramPlugin(t *testing.T) {
	pass, err := NewPureScalarFold(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registration := pass.Registration()
	if registration.Name() != PureScalarFoldName || registration.Stage() != passregistration.StageWholeProgramPatch ||
		registration.Consumer() != passregistration.ExecutionPatch {
		t.Fatalf("registration=%+v", registration)
	}
}

func TestSplitPhaseSourcesReadIsAStaticHostScheduledPlugin(t *testing.T) {
	pass, err := NewSplitPhaseSourcesRead(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registration := pass.Registration()
	if registration.Name() != SplitPhaseSourcesReadName || registration.Stage() != passregistration.StageWholeProgramPatch ||
		registration.Consumer() != passregistration.ExecutionPatch || !pass.HostScheduled() {
		t.Fatalf("registration=%+v", registration)
	}
}

func TestSplitPhaseCapabilityCallsBindsHostProjectionManifest(t *testing.T) {
	pass, err := NewSplitPhaseCapabilityCalls(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registration := pass.Registration()
	if registration.Name() != SplitPhaseCapabilityCallsName || registration.Stage() != passregistration.StageWholeProgramPatch ||
		registration.Consumer() != passregistration.ExecutionPatch || !pass.HostScheduled() {
		t.Fatalf("registration=%+v", registration)
	}
	projections := []CapabilityProjection{
		{Capability: "tools.get", Module: "tools", Method: "get", Arguments: []string{"key"}, ResultField: "value"},
		{Capability: "tools.price", Module: "tools", Method: "price", Arguments: []string{"value"}, ResultField: "quote"},
	}
	transformer := &capabilityTransformer{}
	patch, err := pass.Transform(context.Background(), transformer, "value = tools.get('a')\nresult = value\n", projections)
	if err != nil || patch.ReplacementCount != 1 || len(patch.CapabilityProjections) != 2 ||
		len(transformer.request.CapabilityProjections) != 2 {
		t.Fatalf("request=%+v patch=%+v err=%v", transformer.request, patch, err)
	}
}

func TestDataLocalNumpySumIsAStaticValueSlotPlugin(t *testing.T) {
	pass, err := NewDataLocalNumpySum(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registration := pass.Registration()
	if registration.Name() != DataLocalNumpySumName || registration.Stage() != passregistration.StageWholeProgramPatch ||
		registration.Consumer() != passregistration.ExecutionPatch || !pass.ValueSlotBound() {
		t.Fatalf("registration=%+v", registration)
	}
}
