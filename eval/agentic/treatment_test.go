package agentic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDevelopmentTreatmentsUsesStrictExactPolicies(t *testing.T) {
	root := datasetRoot(t)
	for _, test := range []struct {
		file, id    string
		implemented bool
	}{
		{"baseline-v1.json", TreatmentBaselineV1, true},
		{"structured-host-context-v1.json", TreatmentStructuredHostContextV1, true},
		{"python-safe-repair-v1.json", TreatmentPythonSafeRepairV1, true},
		{"hybrid-two-stage-router-v1.json", TreatmentHybridTwoStageRouterV1, true},
		{"hybrid-two-stage-safe-repair-v2.json", TreatmentHybridTwoStageSafeRepairV2, true},
		{"hybrid-two-stage-prebound-compact-v3.json", TreatmentHybridTwoStagePreboundCompactV3, true},
		{"hybrid-two-stage-prebound-compact-json-v4.json", TreatmentHybridTwoStagePreboundCompactJSONV4, true},
	} {
		treatment, err := LoadDevelopmentTreatment(filepath.Join(root, "treatments", test.file))
		if err != nil || treatment.ID != test.id || treatment.Digest == "" || treatment.Implemented() != test.implemented {
			t.Fatalf("file=%s treatment=%+v err=%v", test.file, treatment, err)
		}
	}
	baselineBytes, err := os.ReadFile(filepath.Join(root, "treatments", "baseline-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if BaselineTreatment().Digest != digest(baselineBytes) {
		t.Fatal("built-in baseline does not match frozen document")
	}
}

func TestPreboundCompactTreatmentFreezesPythonExecutionPolicies(t *testing.T) {
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-prebound-compact-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !treatment.UsesPreboundCompactPython() || !treatment.AllowsPythonRepair() || !treatment.UsesTwoStageRouter() ||
		treatment.AllowsAnyJSONPythonResult() || treatment.PythonBindingPolicy != "prebound-authorized-tools" || treatment.PythonResultPolicy != "default-empty-object" || treatment.PythonSourcePolicy != "compact-no-unused-values" {
		t.Fatalf("treatment=%+v", treatment)
	}
	jsonTreatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-prebound-compact-json-v4.json"))
	if err != nil || !jsonTreatment.UsesPreboundCompactPython() || !jsonTreatment.AllowsAnyJSONPythonResult() || !jsonTreatment.AllowsPythonRepair() || !jsonTreatment.UsesTwoStageRouter() {
		t.Fatalf("json treatment=%+v err=%v", jsonTreatment, err)
	}
}

func TestLoadDevelopmentTreatmentRejectsUnknownKeysAndUnsafeCombinations(t *testing.T) {
	base := map[string]any{
		"schema_version": "agentic-development-treatment/v1", "status": "frozen", "id": TreatmentBaselineV1,
		"host_context_policy": "none", "python_repair_policy": "none", "hybrid_strategy": "combined-surface-v1",
	}
	cases := []map[string]any{}
	unknown := cloneTreatmentMap(base)
	unknown["prompt"] = "free form"
	cases = append(cases, unknown)
	unsafe := cloneTreatmentMap(base)
	unsafe["python_repair_policy"] = "unbounded"
	cases = append(cases, unsafe)
	mismatch := cloneTreatmentMap(base)
	mismatch["id"] = TreatmentStructuredHostContextV1
	cases = append(cases, mismatch)
	ambientAnyJSON := cloneTreatmentMap(base)
	ambientAnyJSON["python_output_schema_policy"] = "any-json"
	cases = append(cases, ambientAnyJSON)
	for index, document := range cases {
		content, _ := json.Marshal(document)
		path := filepath.Join(t.TempDir(), "invalid.json")
		if os.WriteFile(path, content, 0o600) != nil {
			t.Fatal("write treatment")
		}
		if _, err := LoadDevelopmentTreatment(path); err == nil {
			t.Fatalf("invalid case %d accepted", index)
		}
	}
}

func cloneTreatmentMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
