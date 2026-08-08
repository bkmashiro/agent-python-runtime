package replayfixture_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/claimmanifest"
	"github.com/bkmashiro/agent-python-runtime/verification/replayfixture"
)

func TestInputInjectionReplayUsesSealedClockRandomAndInputs(t *testing.T) {
	config := fixtureConfig()
	recording, err := replayfixture.Record(config)
	if err != nil {
		t.Fatal(err)
	}

	// A recording owns its tape; caller mutation after Record must not change it.
	config.Inputs[0].Value = 999
	config.Clock[0] = time.Unix(999, 0).UTC()
	config.Random[0] = 999

	report, state, err := replayfixture.ReplayInputInjection(recording)
	if err != nil {
		t.Fatal(err)
	}
	if report.Qualification != claimmanifest.QualificationInputInjection {
		t.Fatalf("qualification=%q", report.Qualification)
	}
	if !report.InputsInjected || !report.ClockFrozen || !report.RandomFrozen || !report.InitialStateRestored {
		t.Fatalf("report=%+v", report)
	}
	if report.InitialStateDigest == "" {
		t.Fatalf("missing initial-state snapshot digest: %+v", report)
	}
	if state.Value != 14 || len(state.Applied) != 3 {
		t.Fatalf("state=%+v", state)
	}
	if report.RecordingDigest != recording.Digest || report.TranscriptDigest == "" ||
		report.ArtifactDigest != recording.ArtifactDigest {
		t.Fatalf("report=%+v", report)
	}
}

func TestReplayRejectsFixtureArtifactDriftBeforeExecution(t *testing.T) {
	recording, err := replayfixture.Record(fixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	recording.ArtifactDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, _, err := replayfixture.ReplayInputInjection(recording); !errors.Is(err, replayfixture.ErrArtifactMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestInputInjectionReplayRejectsTamperedRecordingBeforeExecution(t *testing.T) {
	recording, err := replayfixture.Record(fixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	recording.Inputs[0].Value = 1000

	if _, _, err := replayfixture.ReplayInputInjection(recording); !errors.Is(err, replayfixture.ErrRecordingIntegrity) {
		t.Fatalf("err=%v", err)
	}
}

func TestStateEquivalentReplayRequiresAuthoritativeFinalStateOracle(t *testing.T) {
	recording, err := replayfixture.Record(fixtureConfig())
	if err != nil {
		t.Fatal(err)
	}

	report, state, err := replayfixture.ReplayStateEquivalent(recording, replayfixture.State{
		Value: 14, Applied: []string{"step-add", "step-set", "step-add-final"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Qualification != claimmanifest.QualificationStateEquivalent || !report.FinalStateOracleVerified {
		t.Fatalf("report=%+v", report)
	}
	if state.Value != 14 {
		t.Fatalf("state=%+v", state)
	}

	if _, _, err := replayfixture.ReplayStateEquivalent(recording, replayfixture.State{Value: 15}); !errors.Is(err, replayfixture.ErrStateMismatch) {
		t.Fatalf("mismatched oracle err=%v", err)
	}
}

func TestRecordRejectsIncompleteNondeterminismTape(t *testing.T) {
	config := fixtureConfig()
	config.Random = config.Random[:2]
	if _, err := replayfixture.Record(config); !errors.Is(err, replayfixture.ErrIncompleteTape) {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordRejectsStateOverflow(t *testing.T) {
	config := fixtureConfig()
	config.InitialState.Value = math.MaxInt64
	config.Inputs = config.Inputs[:1]
	config.Inputs[0].Value = 1
	config.Clock = config.Clock[:1]
	config.Random = config.Random[:1]
	if _, err := replayfixture.Record(config); !errors.Is(err, replayfixture.ErrInvalidFixture) {
		t.Fatalf("err=%v", err)
	}
}

func fixtureConfig() replayfixture.Config {
	return replayfixture.Config{
		FixtureID:    "counter-workflow-v1",
		InitialState: replayfixture.State{Value: 2},
		Inputs: []replayfixture.Input{
			{StepID: "step-add", Operation: replayfixture.OperationAdd, Value: 3},
			{StepID: "step-set", Operation: replayfixture.OperationSet, Value: 10},
			{StepID: "step-add-final", Operation: replayfixture.OperationAdd, Value: 4},
		},
		Clock: []time.Time{
			time.Unix(100, 1).UTC(), time.Unix(101, 2).UTC(), time.Unix(102, 3).UTC(),
		},
		Random: []uint64{11, 22, 33},
	}
}
