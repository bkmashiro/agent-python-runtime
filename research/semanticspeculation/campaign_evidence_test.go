package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestMatchedCaseEvidenceCanonicalRoundTrip(t *testing.T) {
	campaign := matchedCampaignFixture(t)
	sealed, err := SealMatchedCaseEvidence(campaign)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeMatchedCaseEvidence(sealed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMatchedCaseEvidence(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeMatchedCaseEvidence(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Identity == "" || !bytes.Equal(encoded, reencoded) || decoded.Identity != sealed.Identity {
		t.Fatalf("sealed=%+v decoded=%+v", sealed, decoded)
	}
}

func TestMatchedCaseEvidenceRejectsTamperingAndUnknownFields(t *testing.T) {
	sealed, err := SealMatchedCaseEvidence(matchedCampaignFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed.ExecutionOrder[0], sealed.ExecutionOrder[1] = sealed.ExecutionOrder[1], sealed.ExecutionOrder[0]
	if _, err := EncodeMatchedCaseEvidence(sealed); err == nil {
		t.Fatal("tampered execution order was accepted")
	}
	sealed, err = SealMatchedCaseEvidence(matchedCampaignFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed.Aggregate.SerialElapsedNanos++
	if _, err := EncodeMatchedCaseEvidence(sealed); err == nil {
		t.Fatal("tampered aggregate was accepted")
	}
	valid, err := SealMatchedCaseEvidence(matchedCampaignFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeMatchedCaseEvidence(valid)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(encoded, &document) != nil {
		t.Fatal("decode fixture")
	}
	document["unexpected"] = true
	mutated, _ := json.Marshal(document)
	if _, err := DecodeMatchedCaseEvidence(mutated); err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func matchedCampaignFixture(t *testing.T) MatchedCampaignResult {
	t.Helper()
	fixture := Phase3SyntheticCases()[5]
	bindings := matchedTestBindings()
	resultSHA := syntheticDigest([]byte("result"))
	records := make([]TrialRecord, 0, 3)
	for index, treatment := range matchedTreatmentOrder(fixture.ID, 1) {
		outcome := TreatmentOutcome{
			FinalProgramOutcome: "success", FinalPythonStarted: true, ResultSHA256: resultSHA,
			AuthorityDisposition: "unchanged", WorkspaceDisposition: "published",
		}
		if treatment == "eager_style_gate" {
			outcome.PrefixPythonExecutions = 1
		}
		record, err := BuildScheduledTrialRecord(fixture, treatment, 1, bindings, ScheduledTreatmentResult{
			StartedNanos: uint64(10 + index*10), FinalizeNanos: uint64(12 + index*10), EndedNanos: uint64(19 + index*10), Outcome: outcome,
		})
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	oracle, err := NewPerfectEffectOracleEstimate(records[0], 7)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := AggregateMatchedTrials(records, oracle)
	if err != nil {
		t.Fatal(err)
	}
	return MatchedCampaignResult{Records: records, Oracle: oracle, Aggregate: aggregate}
}
