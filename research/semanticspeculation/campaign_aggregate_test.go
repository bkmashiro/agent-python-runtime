package semanticspeculation

import "testing"

func TestAggregateMatchedTrialsRequiresBoundEquivalentLanesAndAnalysisOnlyOracle(t *testing.T) {
	base := validTrialRecord()
	base.CaseID = "external_read_valid_suffix"
	base.FinalProgramOutcome = "success"
	base.FinalPythonStarted = true
	base.ResultSHA256 = "sha256:6666666666666666666666666666666666666666666666666666666666666666"
	base.ErrorClass = ""
	base.LogicalCalls = 1
	base.AuthorityDisposition = "read_consumed"
	base.WorkspaceDisposition = "published"
	base.StartedNanos = 100
	base.FinalizedNanos = 300
	base.EndedNanos = 500
	base.ReadyBeforeFinalize = 0

	serial := base
	serial.Treatment = "serial_whole_file"
	serial.PhysicalAttempts = 1
	serial.PhysicalDispositions = PhysicalDispositions{Consumed: 1}

	eager := base
	eager.Treatment = "eager_style_gate"
	eager.ComparatorContractSHA256 = EagerStyleGateV1Identity
	eager.StartedNanos = 110
	eager.FinalizedNanos = 310
	eager.EndedNanos = 510
	eager.PhysicalAttempts = 1
	eager.PhysicalDispositions = PhysicalDispositions{Consumed: 1}

	semantic := base
	semantic.Treatment = "semantic_pre_dispatch"
	semantic.StartedNanos = 120
	semantic.FinalizedNanos = 250
	semantic.EndedNanos = 400
	semantic.PhysicalAttempts = 1
	semantic.ReadyBeforeFinalize = 1
	semantic.PhysicalDispositions = PhysicalDispositions{Consumed: 1}

	seal := func(value TrialRecord) TrialRecord {
		sealed, err := SealTrialRecord(value)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	serial, eager, semantic = seal(serial), seal(eager), seal(semantic)
	oracle, err := NewPerfectEffectOracleEstimate(serial, 200)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := AggregateMatchedTrials([]TrialRecord{semantic, serial, eager}, oracle)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CaseID != base.CaseID || aggregate.TrialIndex != base.TrialIndex || aggregate.FalseConservativeNanos != 120 || aggregate.SemanticVersusSerialNanos != 120 || aggregate.OracleElapsedNanos != 200 || !aggregate.OracleExcludedFromAchievedSpeedup {
		t.Fatalf("aggregate=%+v", aggregate)
	}

	badOracle := oracle
	badOracle.SourceScheduleSHA256 = "sha256:9999999999999999999999999999999999999999999999999999999999999999"
	if _, err := AggregateMatchedTrials([]TrialRecord{semantic, serial, eager}, badOracle); err == nil {
		t.Fatal("oracle binding mismatch accepted")
	}
	mismatch := eager
	mismatch.ResultSHA256 = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
	mismatch.Identity = ""
	mismatch = seal(mismatch)
	if _, err := AggregateMatchedTrials([]TrialRecord{semantic, serial, mismatch}, oracle); err == nil {
		t.Fatal("semantic mismatch accepted")
	}
	if _, err := AggregateMatchedTrials([]TrialRecord{serial, eager}, oracle); err == nil {
		t.Fatal("missing achieved lane accepted")
	}
}
