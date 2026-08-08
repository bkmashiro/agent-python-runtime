package replayfixture_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/verification/replayfixture"
)

func TestStateHistoryAndAuthoritativeOracleAreBounded(t *testing.T) {
	config := fixtureConfig()
	config.InitialState.Applied = make([]string, 1025)
	for index := range config.InitialState.Applied {
		config.InitialState.Applied[index] = fmt.Sprintf("step-%d", index)
	}
	if _, err := replayfixture.Record(config); !errors.Is(err, replayfixture.ErrInvalidFixture) {
		t.Fatalf("initial state err=%v", err)
	}

	recording, err := replayfixture.Record(fixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	legacy := recording
	legacy.Version = "replay-fixture/v1"
	if _, _, err = replayfixture.ReplayInputInjection(legacy); !errors.Is(err, replayfixture.ErrRecordingIntegrity) {
		t.Fatalf("legacy version err=%v", err)
	}
	oracle := recording.FinalState
	oracle.Applied = make([]string, 2049)
	for index := range oracle.Applied {
		oracle.Applied[index] = fmt.Sprintf("oracle-%d", index)
	}
	if _, _, err = replayfixture.ReplayStateEquivalent(recording, oracle); !errors.Is(err, replayfixture.ErrInvalidFixture) {
		t.Fatalf("oracle err=%v", err)
	}
}
