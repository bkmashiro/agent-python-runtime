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
