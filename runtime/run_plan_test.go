package runtime

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestFrozenRunPlanBindsHostAndExactGuestEvidence(t *testing.T) {
	request := sourceCompatibilityRequest(t, "import json\nresult = 1", []string{"json"})
	profile := sourceCompatibilityProfile(t, "json")
	compatibility, err := EvaluateRunCompatibility(request, &profile)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	validation := decodeSourceValidationFixture(t, `{
		"schema_version":1,"validator":"exact-guest-static-imports-v1","status":"ready",
		"source_sha256":"`+compatibility.SourceSHA256()+`","profile":"base",
		"declared_import_roots":["json"],"ast_import_roots":["json"],"bytecode_checked":true,
		"baseline_modules":["agent_runtime","sys"],
		"entry_closure_modules":["json","json.decoder"],
		"sealed_modules":["agent_runtime","json","json.decoder","sys"]
	}`)
	plan, err := NewFrozenRunPlan(raw, request, compatibility, validation)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if plan.State() != RunPlanFrozen || plan.ProfileID() != "base" || plan.SourceSHA256() != compatibility.SourceSHA256() {
		t.Fatalf("plan identity drifted: %s", mustRunPlanJSON(t, plan))
	}
	if plan.CompatibilityEvidenceSHA256() != compatibility.EvidenceSHA256() || plan.SourceValidationEvidenceSHA256() != validation.EvidenceSHA256() {
		t.Fatalf("evidence not bound: %s", mustRunPlanJSON(t, plan))
	}
	if !reflect.DeepEqual(plan.EntryClosureModules(), []string{"json", "json.decoder"}) ||
		!reflect.DeepEqual(plan.SealedModules(), []string{"agent_runtime", "json", "json.decoder", "sys"}) {
		t.Fatalf("closure drifted: %s", mustRunPlanJSON(t, plan))
	}
	modules := plan.SealedModules()
	modules[0] = "mutated"
	if plan.SealedModules()[0] != "agent_runtime" {
		t.Fatal("RunPlan exposed mutable module policy")
	}
}

func TestFrozenRunPlanRejectsMismatchedOrTamperedEvidence(t *testing.T) {
	request := sourceCompatibilityRequest(t, "import json\nresult = 1", []string{"json"})
	profile := sourceCompatibilityProfile(t, "json")
	compatibility, err := EvaluateRunCompatibility(request, &profile)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(request)
	base := `{"schema_version":1,"validator":"exact-guest-static-imports-v1","status":"ready","source_sha256":"` + compatibility.SourceSHA256() + `","profile":"base","declared_import_roots":["json"],"ast_import_roots":["json"],"bytecode_checked":true,"baseline_modules":["sys"],"entry_closure_modules":["json"],"sealed_modules":["json","sys"]}`
	for name, mutate := range map[string]func(string) string{
		"profile": func(value string) string {
			return string(bytes.ReplaceAll([]byte(value), []byte(`"profile":"base"`), []byte(`"profile":"numpy-core"`)))
		},
		"roots": func(value string) string {
			value = string(bytes.ReplaceAll([]byte(value), []byte(`"declared_import_roots":["json"]`), []byte(`"declared_import_roots":["math"]`)))
			return string(bytes.ReplaceAll([]byte(value), []byte(`"ast_import_roots":["json"]`), []byte(`"ast_import_roots":["math"]`)))
		},
		"source": func(value string) string {
			return string(bytes.ReplaceAll([]byte(value), []byte(compatibility.SourceSHA256()), []byte("sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			validation := decodeSourceValidationFixture(t, mutate(base))
			if _, err := NewFrozenRunPlan(raw, request, compatibility, validation); err == nil {
				t.Fatal("mismatched evidence was accepted")
			}
		})
	}
	validation := decodeSourceValidationFixture(t, base)
	plan, err := NewFrozenRunPlan(raw, request, compatibility, validation)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan)
	tampered := bytes.Replace(encoded, []byte(`"profile":"base"`), []byte(`"profile":"numpy-core"`), 1)
	if bytes.Equal(tampered, encoded) {
		t.Fatal("missing tamper seam")
	}
	if _, err := DecodeFrozenRunPlan(tampered); err == nil {
		t.Fatal("tampered RunPlan digest accepted")
	}
}

func decodeSourceValidationFixture(t *testing.T, raw string) SourceValidationEvidence {
	t.Helper()
	result, err := DecodeSourceValidationEvidence([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustRunPlanJSON(t *testing.T, plan FrozenRunPlan) string {
	t.Helper()
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
