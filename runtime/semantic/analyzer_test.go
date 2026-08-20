package semantic_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/internal/semantictrusted"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestAnalyzeVerifiedPublicAuthorityIsConcreteTargetWazero(t *testing.T) {
	functionType := reflect.TypeOf(semantic.AnalyzeVerified)
	if got, want := functionType.In(1), reflect.TypeOf((*wazeroengine.Engine)(nil)); got != want {
		t.Fatalf("runner parameter=%v want=%v", got, want)
	}
}

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
	runner.mismatch = false
	runner.badAnalyzer = true
	if _, err := semantic.Analyze(context.Background(), runner, request); !errors.Is(err, semantic.ErrAnalysisBinding) {
		t.Fatalf("analyzer identity error=%v", err)
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
	verified, err := semantic.AnalyzeVerifiedTrusted(context.Background(), semantictrusted.New(runner), request)
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
			if _, err := semantic.AnalyzeVerifiedTrusted(context.Background(), semantictrusted.New(candidate), request); !errors.Is(err, semantic.ErrAnalyzerEngineBinding) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAnalyzeRejectsMissingWriteEffectCoverage(t *testing.T) {
	bindings := semantic.Bindings{
		ArtifactSHA256: digestFor('1'), ExecutionProfileSHA256: digestFor('2'),
		ImportClosureSHA256: digestFor('3'), CapabilityPlanSHA256: digestFor('4'),
	}
	request := semantic.Request{
		Source: "result = workspace.write_text()\n", Bindings: bindings,
		Capabilities: []semantic.CapabilityProjection{{
			Name: "workspace.write_text", EffectClass: capability.EffectWorkspaceWrite,
			Playback: capability.PlaybackLiveOnly, Module: "workspace", Method: "write_text", Arguments: []string{},
		}},
	}
	runner := &fakeSemanticRunner{bindings: bindings, emitCall: true, emitRegion: true}
	if _, err := semantic.Analyze(context.Background(), runner, request); !errors.Is(err, semantic.ErrAnalysisBinding) {
		t.Fatalf("missing write coverage error=%v", err)
	}
	runner.writeEffects = true
	if _, err := semantic.Analyze(context.Background(), runner, request); err != nil {
		t.Fatalf("covered write rejected: %v", err)
	}
}

func TestAnalyzeBindsCallSitesToSourceCapabilityAndCanonicalArguments(t *testing.T) {
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
	runner := &fakeSemanticRunner{bindings: bindings, emitCall: true, emitRegion: true}
	analysis, err := semantic.Analyze(context.Background(), runner, request)
	if err != nil || len(analysis.CallSites) != 1 || len(analysis.CandidateRegions) != 1 || !analysis.CallSites[0].NecessarilyReached {
		t.Fatalf("analysis=%+v err=%v", analysis, err)
	}
	runner.tamperRegion = true
	if _, err := semantic.Analyze(context.Background(), runner, request); !errors.Is(err, semantic.ErrAnalysisBinding) {
		t.Fatalf("tampered candidate-region error=%v", err)
	}
	runner.tamperRegion = false
	runner.regionOutsideSource = true
	if _, err := semantic.Analyze(context.Background(), runner, request); !errors.Is(err, semantic.ErrAnalysisBinding) {
		t.Fatalf("out-of-source candidate-region error=%v", err)
	}
	runner.regionOutsideSource = false
	runner.omitRegions = true
	if _, err := semantic.Analyze(context.Background(), runner, request); !errors.Is(err, semantic.ErrInvalidAnalysis) {
		t.Fatalf("missing candidate-regions error=%v", err)
	}
	runner.omitRegions = false
	runner.tamperCall = true
	if _, err := semantic.Analyze(context.Background(), runner, request); !errors.Is(err, semantic.ErrAnalysisBinding) {
		t.Fatalf("tampered call-site error=%v", err)
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
	calls               int
	badAnalyzer         bool
	mismatch            bool
	writeEffects        bool
	workspace           bool
	broker              bool
	emitCall            bool
	tamperCall          bool
	emitRegion          bool
	tamperRegion        bool
	omitRegions         bool
	regionOutsideSource bool
	bindings            semantic.Bindings
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
		ASTSHA256:     digestFor('5'), AnalyzerSHA256: testSemanticDigest("pysolate.semantic-analyzer.v10"),
		ArtifactSHA256: artifact, ExecutionProfileSHA256: request.Bindings.ExecutionProfileSHA256,
		ImportClosureSHA256:  request.Bindings.ImportClosureSHA256,
		CapabilityPlanSHA256: request.Bindings.CapabilityPlanSHA256,
		ModuleSpan:           semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 128},
		Functions:            []semantic.FunctionSummary{}, Barriers: []semantic.Barrier{},
		CallSiteCoverage: "positive_only", CandidateRegionCoverage: "module_top_level_complete", CallSites: []semantic.CallSite{}, CandidateRegions: []semantic.CandidateRegion{},
	}
	if runner.badAnalyzer {
		analysis.AnalyzerSHA256 = digestFor('6')
	}
	if runner.emitCall {
		sourceSHA := analysis.SourceSHA256
		span := semantic.SourceSpan{StartLine: 1, StartColumn: 9, EndLine: 1, EndColumn: 31}
		callID := testSemanticDigest(fmt.Sprintf("pysolate.semantic-call-site.v0\x00%s\x00%s\x00%d:%d:%d:%d",
			sourceSHA, request.Capabilities[0].Name, span.StartLine, span.StartColumn, span.EndLine, span.EndColumn))
		if runner.tamperCall {
			callID = digestFor('f')
		}
		if runner.writeEffects {
			analysis.ModuleEffects = semantic.EffectSummary{MayPublish: true, MaySuspend: true}
		} else {
			analysis.ModuleEffects = semantic.EffectSummary{MayObserveLive: true, MaySuspend: true}
		}
		analysis.CallSites = []semantic.CallSite{{
			ID: callID, Span: span, Capability: request.Capabilities[0].Name,
			ControlRegionID:    testSemanticDigest("pysolate.semantic-control-region.v0\x00" + sourceSHA + "\x00module-entry"),
			NecessarilyReached: true, ArgumentsCanonical: true, CanonicalArguments: json.RawMessage(`{}`), DynamicOccurrence: 1,
		}}
	}
	if request.Source != "" && !runner.omitRegions {
		span := semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: uint32(len([]byte(strings.TrimSuffix(request.Source, "\n"))))}
		if runner.regionOutsideSource {
			span.EndColumn++
		}
		regionID := testSemanticDigest(fmt.Sprintf("pysolate.semantic-candidate-region.v0\x00%s\x000\x00%d:%d:%d:%d",
			analysis.SourceSHA256, span.StartLine, span.StartColumn, span.EndLine, span.EndColumn))
		if runner.tamperRegion {
			regionID = digestFor('e')
		}
		occurrences := []string{}
		if len(analysis.CallSites) == 1 {
			occurrences = []string{analysis.CallSites[0].ID}
		}
		analysis.CandidateRegionCount = 1
		analysis.CandidateRegions = []semantic.CandidateRegion{{
			ID: regionID, Kind: semantic.CandidateRegionStraightLine, Span: span,
			ControlRegionID:     testSemanticDigest("pysolate.semantic-control-region.v0\x00" + analysis.SourceSHA256 + "\x00module-entry"),
			ControlPredecessors: []string{}, DataDependencies: []semantic.RegionDataDependency{},
			LiveIns: []string{}, LiveOuts: []string{}, LiveInsCanonical: true, LiveOutsCanonical: true,
			Effects: analysis.ModuleEffects, CapabilityOccurrences: occurrences,
			Barriers: []semantic.BarrierCode{}, RejectionReasons: []semantic.CandidateRejection{},
		}}
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

func testSemanticDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func digestFor(value byte) string { return "sha256:" + string(makeBytes(value, 64)) }
func makeBytes(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
