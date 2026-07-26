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
		{"structured-host-context-v1.json", TreatmentStructuredHostContextV1, false},
		{"python-safe-repair-v1.json", TreatmentPythonSafeRepairV1, false},
		{"hybrid-two-stage-router-v1.json", TreatmentHybridTwoStageRouterV1, false},
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
