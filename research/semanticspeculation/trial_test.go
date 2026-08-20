package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"testing"
)

func validTrialRecord() TrialRecord {
	return TrialRecord{
		SchemaVersion:          TrialSchemaVersion,
		StudyID:                "semantic-speculation-v1",
		PreregistrationSHA256:  "sha256:5c0ec80ded86f07784d51d74aa503108fbd4a587918bc483bd564b35bdc18a47",
		CaseID:                 "later_syntax_error",
		Treatment:              "semantic_pre_dispatch",
		TrialIndex:             1,
		SourceSHA256:           "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ArtifactSHA256:         "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		CapabilityPlanSHA256:   "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		PrivacySHA256:          "sha256:4444444444444444444444444444444444444444444444444444444444444444",
		FinalProgramOutcome:    "syntax_error",
		FinalPythonStarted:     false,
		PrefixPythonExecutions: 0,
		ResultSHA256:           "",
		ErrorClass:             "syntax_error",
		LogicalCalls:           0,
		PhysicalAttempts:       1,
		PhysicalResultBytes:    128,
		ProviderCostUnits:      1,
		ReadyBeforeFinalize:    1,
		PhysicalDispositions:   PhysicalDispositions{Orphaned: 1},
		AuthorityDisposition:   "unchanged",
		WorkspaceDisposition:   "untouched",
		StartedNanos:           1,
		EndedNanos:             10,
	}
}

func TestTrialRecordRoundTripAndSeal(t *testing.T) {
	sealed, err := SealTrialRecord(validTrialRecord())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeTrialRecord(sealed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTrialRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeTrialRecord(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) || decoded.Identity == "" {
		t.Fatal("trial record did not round trip canonically")
	}
}

func TestTrialRecordRejectsImpossibleLifecycle(t *testing.T) {
	for name, mutate := range map[string]func(*TrialRecord){
		"syntax final started":          func(value *TrialRecord) { value.FinalPythonStarted = true },
		"syntax logical call":           func(value *TrialRecord) { value.LogicalCalls = 1 },
		"physical disposition mismatch": func(value *TrialRecord) { value.PhysicalDispositions.Orphaned = 0 },
		"result on syntax error": func(value *TrialRecord) {
			value.ResultSHA256 = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
		},
		"time reversed":       func(value *TrialRecord) { value.EndedNanos = value.StartedNanos - 1 },
		"too many ready":      func(value *TrialRecord) { value.ReadyBeforeFinalize = value.PhysicalAttempts + 1 },
		"authority published": func(value *TrialRecord) { value.AuthorityDisposition = "write_committed" },
	} {
		t.Run(name, func(t *testing.T) {
			value := validTrialRecord()
			mutate(&value)
			if _, err := SealTrialRecord(value); err == nil {
				t.Fatal("invalid trial accepted")
			}
		})
	}
}

func TestTrialRecordRejectsEveryBoundIdentityMutation(t *testing.T) {
	sealed, err := SealTrialRecord(validTrialRecord())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeTrialRecord(sealed)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]any{
		"study_id":                 "semantic-speculation-other",
		"preregistration_identity": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"case_id":                  "other_case",
		"treatment":                "eager_style_gate",
		"trial_index":              float64(2),
		"source_sha256":            "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"artifact_sha256":          "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"manifest_sha256":          "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"import_inventory_sha256":  "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		"execution_profile_sha256": "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"capability_plan_sha256":   "sha256:abababababababababababababababababababababababababababababababab",
	}
	for field, replacement := range mutations {
		t.Run(field, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			document[field] = replacement
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeTrialRecord(mutated); err == nil {
				t.Fatal("identity mutation accepted")
			}
		})
	}
}

func TestTrialRecordRejectsMutationUnknownFieldsAndBodies(t *testing.T) {
	sealed, err := SealTrialRecord(validTrialRecord())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeTrialRecord(sealed)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	document["provider_cost_units"] = float64(2)
	mutated, _ := json.Marshal(document)
	if _, err := DecodeTrialRecord(mutated); err == nil {
		t.Fatal("mutated trial accepted")
	}
	unknown := append([]byte(nil), encoded[:len(encoded)-1]...)
	unknown = append(unknown, []byte(`,"result_body":"secret"}`)...)
	if _, err := DecodeTrialRecord(unknown); err == nil {
		t.Fatal("unknown body field accepted")
	}
}

func TestTrialRecordAcceptsEveryPhysicalTerminalDisposition(t *testing.T) {
	values := map[string]PhysicalDispositions{
		"consumed":  {Consumed: 1},
		"orphaned":  {Orphaned: 1},
		"cancelled": {Cancelled: 1},
		"failed":    {Failed: 1},
		"late":      {Late: 1},
		"timed_out": {TimedOut: 1},
		"fallback":  {Fallback: 1},
	}
	for name, dispositions := range values {
		t.Run(name, func(t *testing.T) {
			value := validTrialRecord()
			value.PhysicalDispositions = dispositions
			if _, err := SealTrialRecord(value); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTrialRecordAcceptsSerialSuccessAndEagerPrefixFailure(t *testing.T) {
	serial := validTrialRecord()
	serial.CaseID = "external_read_valid_suffix"
	serial.Treatment = "serial_whole_file"
	serial.FinalProgramOutcome = "success"
	serial.FinalPythonStarted = true
	serial.ResultSHA256 = "sha256:6666666666666666666666666666666666666666666666666666666666666666"
	serial.ErrorClass = ""
	serial.LogicalCalls = 1
	serial.PhysicalAttempts = 1
	serial.PhysicalResultBytes = 128
	serial.ProviderCostUnits = 1
	serial.ReadyBeforeFinalize = 0
	serial.PhysicalDispositions = PhysicalDispositions{Consumed: 1}
	serial.AuthorityDisposition = "read_consumed"
	serial.WorkspaceDisposition = "published"
	if _, err := SealTrialRecord(serial); err != nil {
		t.Fatal(err)
	}

	eager := validTrialRecord()
	eager.Treatment = "eager_style_gate"
	eager.PhysicalAttempts = 0
	eager.PhysicalResultBytes = 0
	eager.ProviderCostUnits = 0
	eager.ReadyBeforeFinalize = 0
	eager.PhysicalDispositions = PhysicalDispositions{}
	eager.PrefixPythonExecutions = 1
	if _, err := SealTrialRecord(eager); err != nil {
		t.Fatal(err)
	}
}
