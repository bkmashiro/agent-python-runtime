package agentic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeActivation(t *testing.T, plan DevelopmentPilotPlan, hostDigest string) string {
	t.Helper()
	document := map[string]any{
		"schema_version": "agentic-pilot-activation/v1", "status": "approved", "plan_digest": plan.Digest,
		"repository_commit": strings.Repeat("a", 40), "host_artifact_digest": hostDigest,
		"dataset_manifest_digest": plan.DatasetManifestDigest,
		"guest_artifacts":         map[string]any{"core": "sha256:" + strings.Repeat("c", 64)},
		"maximum_spend":           map[string]any{"currency": "USD", "decimal": "5.00"},
		"approved_by":             "owner", "approved_at": "2026-07-26T12:00:00Z",
	}
	content, _ := json.Marshal(document)
	path := filepath.Join(t.TempDir(), "activation.json")
	if os.WriteFile(path, content, 0o600) != nil {
		t.Fatal("write activation")
	}
	return path
}

func TestLoadPilotActivationBindsExactArtifacts(t *testing.T) {
	root := datasetRoot(t)
	plan, _, err := LoadDevelopmentPilotPlan(filepath.Join(root, "development-pilot-plan.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	host := "sha256:" + strings.Repeat("b", 64)
	path := writeActivation(t, plan, host)
	activation, err := LoadPilotActivation(path, plan, host)
	if err != nil || activation.Digest == "" {
		t.Fatalf("activation=%+v err=%v", activation, err)
	}
	direct, err := activation.Identity(ConditionDirect)
	if err != nil || direct.GuestArtifactDigest != "" {
		t.Fatalf("direct=%+v err=%v", direct, err)
	}
	python, err := activation.Identity(ConditionPython)
	if err != nil || python.GuestProfile != "core" || python.GuestArtifactDigest == "" {
		t.Fatalf("python=%+v err=%v", python, err)
	}
	if _, err := LoadPilotActivation(path, plan, "sha256:"+strings.Repeat("d", 64)); err == nil {
		t.Fatal("mismatched Host artifact accepted")
	}
}
