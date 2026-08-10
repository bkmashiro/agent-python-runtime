package workspacecapsule

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReportSchemaAcceptsDefaultAndStressReports(t *testing.T) {
	base := Report{
		SchemaVersion:  SchemaVersion,
		Status:         StatusVerified,
		ArtifactSHA256: strings.Repeat("a", 64),
		Engine: EngineProperties{
			Backend: "wazero", ResetMode: "fresh-instance",
			RequestedStrategy: "single-use-preinitialized",
			ActiveStrategy:    "single-use-preinitialized",
		},
		Checks: []Check{{Name: "workspace_continuation", Status: CheckPass, Detail: "observed"}},
	}
	assertReportSchemaAccepts(t, base)
	before, after, delta := 7, 7, 0
	base.Stress = &StressSummary{
		RequestedIterations: 100, CompletedIterations: 100,
		OpenFDsBefore: &before, OpenFDsAfter: &after, OpenFDDelta: &delta,
	}
	assertReportSchemaAccepts(t, base)
	base.Status = StatusFailed
	base.Checks[0].Status = CheckFail
	assertReportSchemaAccepts(t, base)
}

func TestReportSchemaRejectsPartialFDOracleAndUnknownFields(t *testing.T) {
	partial := []byte(`{
		"schema_version":"workspace-capsule-verification/v2",
		"status":"verified",
		"artifact_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"engine":{"backend":"wazero","reset_mode":"fresh-instance","requested_strategy":"fresh-instance","active_strategy":"fresh-instance","fallback":false},
		"checks":[],
		"stress":{"requested_iterations":1,"completed_iterations":1,"open_fds_before":7}
	}`)
	if err := ValidateReportJSON(partial); err == nil {
		t.Fatal("partial FD oracle was accepted")
	}
	unknown := []byte(`{
		"schema_version":"workspace-capsule-verification/v2",
		"status":"failed",
		"artifact_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"engine":{"backend":"","reset_mode":"","requested_strategy":"","active_strategy":"","fallback":false},
		"checks":[],
		"unexpected":true
	}`)
	if err := ValidateReportJSON(unknown); err == nil {
		t.Fatal("unknown report field was accepted")
	}
	duplicate := []byte(`{
		"schema_version":"workspace-capsule-verification/v2",
		"status":"failed",
		"status":"verified",
		"artifact_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"engine":{"backend":"","reset_mode":"","requested_strategy":"","active_strategy":"","fallback":false},
		"checks":[]
	}`)
	if err := ValidateReportJSON(duplicate); err == nil {
		t.Fatal("duplicate report key was accepted")
	}
}

func assertReportSchemaAccepts(t *testing.T, report Report) {
	t.Helper()
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReportJSON(payload); err != nil {
		t.Fatalf("report=%s err=%v", payload, err)
	}
}
