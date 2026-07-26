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
		"schema_version": "agentic-pilot-activation/v1", "status": "approved", "execution_mode": "pilot", "plan_digest": plan.Digest,
		"repository_commit": strings.Repeat("a", 40), "host_artifact_digest": hostDigest,
		"dataset_manifest_digest": plan.DatasetManifestDigest,
		"provider_catalog_digest": "sha256:" + strings.Repeat("d", 64), "provider_catalog_observed_at": "2026-07-26T11:00:00Z",
		"guest_artifacts": map[string]any{"core": "sha256:" + strings.Repeat("c", 64)},
		"approved_by":     "owner", "approved_at": "2026-07-26T12:00:00Z",
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
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if json.Unmarshal(content, &legacy) != nil {
		t.Fatal("decode activation")
	}
	invalidMode := make(map[string]any, len(legacy))
	for key, value := range legacy {
		invalidMode[key] = value
	}
	invalidMode["execution_mode"] = "full"
	invalidContent, _ := json.Marshal(invalidMode)
	invalidPath := filepath.Join(t.TempDir(), "invalid-mode.json")
	if os.WriteFile(invalidPath, invalidContent, 0o600) != nil {
		t.Fatal("write invalid mode activation")
	}
	if _, err := LoadPilotActivation(invalidPath, plan, host); err == nil {
		t.Fatal("unknown execution mode accepted")
	}
	legacy["maximum_spend"] = map[string]any{"currency": "USD", "decimal": "0.01"}
	content, _ = json.Marshal(legacy)
	if os.WriteFile(path, content, 0o600) != nil {
		t.Fatal("rewrite activation")
	}
	if _, err := LoadPilotActivation(path, plan, host); err == nil {
		t.Fatal("unenforced monetary cap field accepted")
	}
}
