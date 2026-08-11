package placement

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedPlacementCanaryStopEvidenceIsSanitizedAndBound(t *testing.T) {
	root := filepath.Join("..", "agentic", "results", "placement-canary-stop-2026-08-11")
	data, err := os.ReadFile(filepath.Join(root, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var summary map[string]any
	if err := decoder.Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary["schema_version"] != "placement-canary-stop/v1" || summary["status"] != "stopped_before_three_arm_model_canary" ||
		summary["source_commit"] != "362f91073cab320cd8a7acd25d5f047ffcb8a208" ||
		summary["corpus_manifest_sha256"] != "sha256:f756770de51bda5f9dbf78ece677a25bda7cbdd427a66e48409af0cb6db2116a" {
		t.Fatalf("stop evidence identity drift: %+v", summary)
	}
	canary, ok := summary["model_canary"].(map[string]any)
	if !ok || canary["private_debug_published"] != false || canary["failure_layer"] != "model_program" ||
		canary["proposal_code_sha256"] != "sha256:f8cad6071e383b943c7b8ce0cba8e97150f7f3d6d8b30f7e226583b45495153e" {
		t.Fatalf("invalid sanitized canary evidence: %+v", canary)
	}
	text := string(data)
	for _, forbidden := range []string{"content_response =", "raw_content =", "provider_response", "developer_prompt", "api_key"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("published evidence contains raw or credential-like field %q", forbidden)
		}
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil || !strings.Contains(string(readme), "Stop before the paid three-arm development screen") ||
		!strings.Contains(string(readme), "does **not** support making Pysolate the default") {
		t.Fatalf("invalid stop report: err=%v", err)
	}
}

func TestPublishedPlacementCanaryCorrectionSupersedesTheStopRationale(t *testing.T) {
	root := filepath.Join("..", "agentic", "results", "placement-canary-profile-correction-2026-08-11")
	data, err := os.ReadFile(filepath.Join(root, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var summary map[string]any
	if err := decoder.Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary["schema_version"] != "placement-canary-correction/v1" ||
		summary["status"] != "stop_rationale_superseded" ||
		summary["source_commit"] != "01e95272ebce599e9e2b5513c38f3fa4c6878885" ||
		summary["identity_lock_sha256"] != "sha256:2fe7663b712754db615d4a9122d7966dd57fa7b98b3b225efcc5aa21b0fe867f" {
		t.Fatalf("correction identity drift: %+v", summary)
	}
	classification, ok := summary["corrected_classification"].(map[string]any)
	if !ok || classification["csv_rejection"] != "harness_profile_policy_defect" ||
		classification["missing_directory_change"] != "model_program_failure" ||
		classification["single_model_program_failure_stops_cohort"] != false {
		t.Fatalf("invalid correction classification: %+v", classification)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil || !strings.Contains(string(readme), "Historical record retained") ||
		!strings.Contains(string(readme), "must be counted") {
		t.Fatalf("invalid correction report: err=%v", err)
	}
}
