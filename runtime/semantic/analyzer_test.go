package semantic_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestAnalyzeUsesOptionalExactGuestSurfaceAndChecksBindings(t *testing.T) {
	bindings := semantic.Bindings{
		ArtifactSHA256: digestFor('1'), ExecutionProfileSHA256: digestFor('2'),
		ImportClosureSHA256: digestFor('3'), CapabilityPlanSHA256: digestFor('4'),
	}
	request, err := semantic.NewRequest("result = inputs['value'] + 1\n", bindings, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeSemanticRunner{bindings: bindings}
	analysis, err := semantic.Analyze(context.Background(), runner, request)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.SourceSHA256 == "" || runner.calls != 1 {
		t.Fatalf("analysis=%+v calls=%d", analysis, runner.calls)
	}
	runner.bindings.ArtifactSHA256 = digestFor('9')
	if _, err := semantic.Analyze(context.Background(), runner, request); !errors.Is(err, semantic.ErrAnalyzerEngineBinding) {
		t.Fatalf("engine binding error=%v", err)
	}
	runner.bindings = bindings
	runner.mismatch = true
	if _, err := semantic.Analyze(context.Background(), runner, request); !errors.Is(err, semantic.ErrAnalysisBinding) {
		t.Fatalf("mismatch error=%v", err)
	}
	if _, err := semantic.Analyze(context.Background(), plainRunner{}, request); !errors.Is(err, semantic.ErrAnalyzerUnavailable) {
		t.Fatalf("unavailable error=%v", err)
	}
}

func TestAnalyzeVerifiedWithholdsAuthorityAndReturnsDetachedReports(t *testing.T) {
	bindings := semantic.Bindings{
		ArtifactSHA256: digestFor('1'), ExecutionProfileSHA256: digestFor('2'),
		ImportClosureSHA256: digestFor('3'), CapabilityPlanSHA256: digestFor('4'),
	}
	request, err := semantic.NewRequest("result = 1", bindings, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeSemanticRunner{bindings: bindings}
	verified, err := semantic.AnalyzeVerified(context.Background(), runner, request)
	if err != nil {
		t.Fatal(err)
	}
	report, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	report.AnalyzerSHA256 = digestFor('9')
	again, err := verified.Analysis()
	if err != nil || again.AnalyzerSHA256 == report.AnalyzerSHA256 {
		t.Fatalf("detached report=%+v err=%v", again, err)
	}
	for name, mutate := range map[string]func(*fakeSemanticRunner){
		"workspace": func(value *fakeSemanticRunner) { value.workspace = true },
		"broker":    func(value *fakeSemanticRunner) { value.broker = true },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := &fakeSemanticRunner{bindings: bindings}
			mutate(candidate)
			if _, err := semantic.AnalyzeVerified(context.Background(), candidate, request); !errors.Is(err, semantic.ErrAnalyzerEngineBinding) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestNewRequestProjectsTypedCapabilityMetadataAndRejectsAmbiguity(t *testing.T) {
	policy := capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1", Timeout: time.Second, MaxResponseBytes: 1024}
	spec, grant, err := capability.DemoCatalogDefinition(policy)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, capability.NewPlaybackHandler()); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	bindings := semantic.Bindings{
		ArtifactSHA256: digestFor('1'), ExecutionProfileSHA256: digestFor('2'),
		ImportClosureSHA256: digestFor('3'), CapabilityPlanSHA256: plan.Identity(),
	}
	request, err := semantic.NewRequest("result = sources.demo_catalog()\n", bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Capabilities) != 1 || request.Capabilities[0].Name != "sources.demo_catalog" || request.Capabilities[0].EffectClass != capability.EffectExternalRead {
		t.Fatalf("projections=%+v", request.Capabilities)
	}
	request.Capabilities = append(request.Capabilities, request.Capabilities[0])
	if err := request.Validate(); !errors.Is(err, semantic.ErrInvalidRequest) {
		t.Fatalf("duplicate projection error=%v", err)
	}
}

type fakeSemanticRunner struct {
	calls     int
	mismatch  bool
	workspace bool
	broker    bool
	bindings  semantic.Bindings
}

func (runner *fakeSemanticRunner) AnalyzeSemantic(_ context.Context, payload []byte) ([]byte, error) {
	runner.calls++
	var request semantic.Request
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	sourceDigest := sha256.Sum256([]byte(request.Source))
	artifact := request.Bindings.ArtifactSHA256
	if runner.mismatch {
		artifact = digestFor('9')
	}
	analysis := semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion,
		SourceSHA256:  fmt.Sprintf("sha256:%x", sourceDigest[:]),
		ASTSHA256:     digestFor('5'), AnalyzerSHA256: digestFor('6'),
		ArtifactSHA256: artifact, ExecutionProfileSHA256: request.Bindings.ExecutionProfileSHA256,
		ImportClosureSHA256:  request.Bindings.ImportClosureSHA256,
		CapabilityPlanSHA256: request.Bindings.CapabilityPlanSHA256,
		ModuleSpan:           semantic.SourceSpan{StartLine: 1, EndLine: 1},
		Functions:            []semantic.FunctionSummary{}, Barriers: []semantic.Barrier{},
	}
	return json.Marshal(analysis)
}

func (runner *fakeSemanticRunner) Run(context.Context, []byte, string) ([]byte, error) {
	return nil, nil
}
func (runner *fakeSemanticRunner) Close(context.Context) error { return nil }
func (runner *fakeSemanticRunner) Properties() enginecontract.Properties {
	return enginecontract.Properties{
		Backend: "fake", ExecutionProfileID: "base", AllowedImports: []string{"sys"},
		ArtifactSHA256: runner.bindings.ArtifactSHA256, ManifestSHA256: digestFor('7'),
		AvailableImports: []string{"sys"}, QualifiedImports: []string{"sys"},
		ExecutionProfileBindingSHA256: runner.bindings.ExecutionProfileSHA256,
		WorkspaceMounted:              runner.workspace, CapabilityBrokerAvailable: runner.broker,
	}
}

type plainRunner struct{}

func (plainRunner) Run(context.Context, []byte, string) ([]byte, error) { return nil, nil }
func (plainRunner) Close(context.Context) error                         { return nil }
func (plainRunner) Properties() enginecontract.Properties {
	return enginecontract.Properties{Backend: "plain"}
}

func digestFor(value byte) string { return "sha256:" + string(makeBytes(value, 64)) }
func makeBytes(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
