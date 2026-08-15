package placementcensus

import (
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestBuildProducesNoGoWhenSemanticOverlayAddsNoPlacementPrecision(t *testing.T) {
	report, err := build(testTarget(), []observation{
		{ProgramID: "agent-a", Baseline: PlacementWASM, Semantic: semantic.BackendUnknown, Rejections: []semantic.RejectionReason{semantic.RejectBackendContractMissing}},
		{ProgramID: "agent-b", Baseline: PlacementNative, Semantic: semantic.BackendUnknown, Rejections: []semantic.RejectionReason{semantic.RejectBackendContractMissing}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SafePrecisionGains != 0 || report.ReplacementRegressions != 2 || report.Decision.Status != "no_go" || !report.Decision.CurrentRouterRetained || report.Decision.ConsumerAdmitted {
		t.Fatalf("report=%+v", report)
	}
	if len(report.Decision.ReasonCodes) != 2 || report.Decision.ReasonCodes[0] != "no_safe_precision_gain" || report.Decision.ReasonCodes[1] != "semantic_would_regress_decisive_baseline" {
		t.Fatalf("reasons=%v", report.Decision.ReasonCodes)
	}
	encoded, err := Encode(report)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("encoded=%q err=%v", encoded, err)
	}
}

func TestBuildPreRegistersGoOnlyForSafeGainWithoutDisagreement(t *testing.T) {
	report, err := build(testTarget(), []observation{
		{ProgramID: "agent-a", Baseline: PlacementUnknown, Semantic: semantic.BackendPysolate, Rejections: []semantic.RejectionReason{}},
		{ProgramID: "agent-b", Baseline: PlacementNative, Semantic: semantic.BackendNative, Rejections: []semantic.RejectionReason{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SafePrecisionGains != 1 || report.Agreements != 1 || report.Decision.Status != "go_for_minimal_integration" || report.Decision.CurrentRouterRetained {
		t.Fatalf("report=%+v", report)
	}
}

func TestBuildRejectsIntegrationWhenGainWouldAlsoRegressBaseline(t *testing.T) {
	report, err := build(testTarget(), []observation{
		{ProgramID: "agent-a", Baseline: PlacementUnknown, Semantic: semantic.BackendPysolate, Rejections: []semantic.RejectionReason{}},
		{ProgramID: "agent-b", Baseline: PlacementNative, Semantic: semantic.BackendUnknown, Rejections: []semantic.RejectionReason{semantic.RejectBackendContractMissing}},
	})
	if err != nil || report.SafePrecisionGains != 1 || report.ReplacementRegressions != 1 || report.Decision.Status != "no_go" || !report.Decision.CurrentRouterRetained {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestBuildRejectsDisagreementAndUnsortedRows(t *testing.T) {
	target := testTarget()
	report, err := build(target, []observation{{ProgramID: "agent-a", Baseline: PlacementNative, Semantic: semantic.BackendPysolate, Rejections: []semantic.RejectionReason{}}})
	if err != nil || report.Decision.Status != "no_go" || report.Disagreements != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, err := build(target, []observation{
		{ProgramID: "agent-b", Baseline: PlacementWASM, Semantic: semantic.BackendUnknown, Rejections: []semantic.RejectionReason{semantic.RejectBackendContractMissing}},
		{ProgramID: "agent-a", Baseline: PlacementWASM, Semantic: semantic.BackendUnknown, Rejections: []semantic.RejectionReason{semantic.RejectBackendContractMissing}},
	}); err == nil {
		t.Fatal("unsorted rows admitted")
	}
}

func TestEncodeDecodeSealAndCanonicality(t *testing.T) {
	report, err := build(testTarget(), []observation{{
		ProgramID: "agent-a", Baseline: PlacementWASM, Semantic: semantic.BackendUnknown,
		Rejections: []semantic.RejectionReason{semantic.RejectBackendContractMissing},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(report)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil || decoded.Validate() != nil {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	decoded.SafePrecisionGains++
	if decoded.Validate() == nil {
		t.Fatal("sealed mutation admitted")
	}
	var raw map[string]any
	if json.Unmarshal(encoded, &raw) != nil {
		t.Fatal("invalid fixture")
	}
	raw["unknown"] = true
	changed, _ := json.Marshal(raw)
	if _, err := Decode(changed); err == nil {
		t.Fatal("unknown field admitted")
	}
}

func testTarget() Target {
	return Target{
		ArtifactSourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArtifactSHA256:       testDigest("artifact"), AnalyzerSHA256: testDigest("analyzer"),
		ExecutionProfileSHA256: testDigest("profile"), ImportClosureSHA256: testDigest("imports"),
		CapabilityPlanSHA256: testDigest("plan"), CorpusSHA256: testDigest("corpus"),
	}
}

func testDigest(seed string) string {
	encoded := make([]byte, 64)
	for index := range encoded {
		encoded[index] = "0123456789abcdef"[(index+len(seed))%16]
	}
	return "sha256:" + string(encoded)
}
