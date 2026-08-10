package runtime

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestImportReceiptEvidenceBindsPlanAndIsImmutable(t *testing.T) {
	plan := frozenRunPlanFixture(t)
	guest, err := DecodeGuestImportReceiptEvidence([]byte(`{"schema_version":1,"collector":"cpython-pre-cache-import-gate-v1","events":[{"sequence":0,"module_name":"json.decoder","decision":"admitted"},{"sequence":1,"module_name":"fractions","decision":"denied"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindImportReceiptEvidence(plan, guest)
	if err != nil || bound.Validate() != nil || bound.PlanSHA256() != plan.PlanSHA256() {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	events := bound.Events()
	events[0].ModuleName = "mutated"
	if got := bound.Events()[0].ModuleName; got != "json.decoder" {
		t.Fatalf("mutable events=%q", got)
	}
	encoded, err := json.Marshal(bound)
	if err != nil || len(encoded) == 0 || bound.EvidenceSHA256() == "" {
		t.Fatalf("encoded=%s err=%v", encoded, err)
	}
}

func TestImportReceiptEvidenceRejectsNoncanonicalEvents(t *testing.T) {
	for name, value := range map[string]string{
		"schema":   `{"schema_version":0,"collector":"cpython-pre-cache-import-gate-v1","events":[]}`,
		"sequence": `{"schema_version":1,"collector":"cpython-pre-cache-import-gate-v1","events":[{"sequence":1,"module_name":"json","decision":"admitted"}]}`,
		"decision": `{"schema_version":1,"collector":"cpython-pre-cache-import-gate-v1","events":[{"sequence":0,"module_name":"json","decision":"unknown"}]}`,
		"module":   `{"schema_version":1,"collector":"cpython-pre-cache-import-gate-v1","events":[{"sequence":0,"module_name":".json","decision":"denied"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeGuestImportReceiptEvidence([]byte(value)); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func frozenRunPlanFixture(t *testing.T) FrozenRunPlan {
	t.Helper()
	request := sourceCompatibilityRequest(t, "import json\nresult = 1", []string{"json"})
	profile := sourceCompatibilityProfile(t, "json", "sys")
	compatibility, err := EvaluateRunCompatibility(request, &profile)
	if err != nil {
		t.Fatal(err)
	}
	validation := decodeSourceValidationFixture(t, `{"schema_version":1,"validator":"exact-guest-static-imports-v1","status":"ready","source_sha256":"`+compatibility.SourceSHA256()+`","profile":"base","declared_import_roots":["json"],"ast_import_roots":["json"],"bytecode_checked":true,"baseline_modules":["sys"],"entry_closure_modules":["json"],"sealed_modules":["json","sys"]}`)
	raw, _ := json.Marshal(request)
	plan, err := NewFrozenRunPlan(raw, request, compatibility, validation)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.EntryClosureModules(), []string{"json"}) {
		t.Fatal("fixture drift")
	}
	return plan
}
