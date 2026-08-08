// Package replayfixture provides a fully local deterministic workflow used to
// qualify replay evidence. It has no provider, network, filesystem, or Runtime
// callback surface; every nondeterministic input is supplied by a sealed tape.
package replayfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"time"

	"github.com/bkmashiro/agent-python-runtime/claimmanifest"
)

const (
	Version         = "replay-fixture/v2"
	fixtureArtifact = "replayfixture.counter/v2\nstate:int64,applied[unique,max=2048],initial-applied-max=1024\ninputs-max=1024\noperations:add-checked-int64,set\nreceipt:step-id,time,nonce,value\n"
	maxSteps        = 1024
	maxApplied      = 2 * maxSteps
)

var (
	ErrArtifactMismatch   = errors.New("replay fixture: fixture artifact mismatch")
	ErrIncompleteTape     = errors.New("replay fixture: incomplete nondeterminism tape")
	ErrInvalidFixture     = errors.New("replay fixture: invalid fixture")
	ErrRecordingIntegrity = errors.New("replay fixture: recording integrity failed")
	ErrStateMismatch      = errors.New("replay fixture: final state oracle mismatch")
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Operation string

const (
	OperationAdd Operation = "add"
	OperationSet Operation = "set"
)

type State struct {
	Value   int64    `json:"value"`
	Applied []string `json:"applied"`
}

type Input struct {
	StepID    string    `json:"step_id"`
	Operation Operation `json:"operation"`
	Value     int64     `json:"value"`
}

type Config struct {
	FixtureID    string      `json:"fixture_id"`
	InitialState State       `json:"initial_state"`
	Inputs       []Input     `json:"inputs"`
	Clock        []time.Time `json:"clock"`
	Random       []uint64    `json:"random"`
}

type Receipt struct {
	StepID string    `json:"step_id"`
	At     time.Time `json:"at"`
	Nonce  uint64    `json:"nonce"`
	Value  int64     `json:"value"`
}

type Recording struct {
	Version          string      `json:"version"`
	ArtifactDigest   string      `json:"artifact_digest"`
	FixtureID        string      `json:"fixture_id"`
	InitialState     State       `json:"initial_state"`
	Inputs           []Input     `json:"inputs"`
	Clock            []time.Time `json:"clock"`
	Random           []uint64    `json:"random"`
	Receipts         []Receipt   `json:"receipts"`
	FinalState       State       `json:"final_state"`
	TranscriptDigest string      `json:"transcript_digest"`
	Digest           string      `json:"digest"`
}

type Report struct {
	Qualification            claimmanifest.Qualification `json:"qualification"`
	ArtifactDigest           string                      `json:"artifact_digest"`
	RecordingDigest          string                      `json:"recording_digest"`
	TranscriptDigest         string                      `json:"transcript_digest"`
	InitialStateDigest       string                      `json:"initial_state_digest"`
	FinalStateDigest         string                      `json:"final_state_digest,omitempty"`
	InputsInjected           bool                        `json:"inputs_injected"`
	ClockFrozen              bool                        `json:"clock_frozen"`
	RandomFrozen             bool                        `json:"random_frozen"`
	InitialStateRestored     bool                        `json:"initial_state_restored"`
	FinalStateOracleVerified bool                        `json:"final_state_oracle_verified"`
}

func Record(config Config) (Recording, error) {
	owned := cloneConfig(config)
	if err := validateConfig(owned); err != nil {
		return Recording{}, err
	}
	receipts, final, err := execute(owned.InitialState, owned.Inputs, owned.Clock, owned.Random)
	if err != nil {
		return Recording{}, err
	}
	transcriptDigest, err := digest(receipts)
	if err != nil {
		return Recording{}, err
	}
	recording := Recording{
		Version: Version, ArtifactDigest: rawDigest([]byte(fixtureArtifact)), FixtureID: owned.FixtureID, InitialState: owned.InitialState,
		Inputs: owned.Inputs, Clock: owned.Clock, Random: owned.Random,
		Receipts: receipts, FinalState: final, TranscriptDigest: transcriptDigest,
	}
	recording.Digest, err = digest(recording.body())
	if err != nil {
		return Recording{}, err
	}
	return recording, nil
}

func ReplayInputInjection(recording Recording) (Report, State, error) {
	if err := verifyRecording(recording); err != nil {
		return Report{}, State{}, err
	}
	receipts, final, err := execute(cloneState(recording.InitialState), cloneInputs(recording.Inputs), append([]time.Time(nil), recording.Clock...), append([]uint64(nil), recording.Random...))
	if err != nil {
		return Report{}, State{}, err
	}
	if !reflect.DeepEqual(receipts, recording.Receipts) || !reflect.DeepEqual(final, recording.FinalState) {
		return Report{}, State{}, ErrRecordingIntegrity
	}
	initialStateDigest, err := digest(recording.InitialState)
	if err != nil {
		return Report{}, State{}, err
	}
	return Report{
		Qualification:   claimmanifest.QualificationInputInjection,
		ArtifactDigest:  recording.ArtifactDigest,
		RecordingDigest: recording.Digest, TranscriptDigest: recording.TranscriptDigest,
		InitialStateDigest: initialStateDigest,
		InputsInjected:     true, ClockFrozen: true, RandomFrozen: true, InitialStateRestored: true,
	}, cloneState(final), nil
}

func ReplayStateEquivalent(recording Recording, authoritativeFinal State) (Report, State, error) {
	if err := validateState(authoritativeFinal, maxApplied); err != nil {
		return Report{}, State{}, err
	}
	report, final, err := ReplayInputInjection(recording)
	if err != nil {
		return Report{}, State{}, err
	}
	if !reflect.DeepEqual(final, authoritativeFinal) {
		return Report{}, State{}, ErrStateMismatch
	}
	finalDigest, err := digest(authoritativeFinal)
	if err != nil {
		return Report{}, State{}, err
	}
	report.Qualification = claimmanifest.QualificationStateEquivalent
	report.FinalStateDigest = finalDigest
	report.FinalStateOracleVerified = true
	return report, cloneState(final), nil
}

func verifyRecording(recording Recording) error {
	if recording.ArtifactDigest != rawDigest([]byte(fixtureArtifact)) {
		return ErrArtifactMismatch
	}
	if recording.Version != Version {
		return ErrRecordingIntegrity
	}
	config := Config{
		FixtureID: recording.FixtureID, InitialState: cloneState(recording.InitialState),
		Inputs: cloneInputs(recording.Inputs), Clock: append([]time.Time(nil), recording.Clock...),
		Random: append([]uint64(nil), recording.Random...),
	}
	if err := validateConfig(config); err != nil {
		return ErrRecordingIntegrity
	}
	if err := validateState(recording.FinalState, maxApplied); err != nil || len(recording.Receipts) != len(recording.Inputs) {
		return ErrRecordingIntegrity
	}
	wantTranscript, err := digest(recording.Receipts)
	if err != nil || wantTranscript != recording.TranscriptDigest {
		return ErrRecordingIntegrity
	}
	wantDigest, err := digest(recording.body())
	if err != nil || wantDigest != recording.Digest {
		return ErrRecordingIntegrity
	}
	return nil
}

func validateConfig(config Config) error {
	if !identifierPattern.MatchString(config.FixtureID) || len(config.Inputs) == 0 || len(config.Inputs) > maxSteps {
		return ErrInvalidFixture
	}
	if err := validateState(config.InitialState, maxSteps); err != nil || len(config.InitialState.Applied)+len(config.Inputs) > maxApplied {
		return ErrInvalidFixture
	}
	if len(config.Clock) != len(config.Inputs) || len(config.Random) != len(config.Inputs) {
		return ErrIncompleteTape
	}
	seen := make(map[string]struct{}, len(config.InitialState.Applied)+len(config.Inputs))
	for _, stepID := range config.InitialState.Applied {
		seen[stepID] = struct{}{}
	}
	for index, input := range config.Inputs {
		if !identifierPattern.MatchString(input.StepID) {
			return fmt.Errorf("%w: invalid step id", ErrInvalidFixture)
		}
		if _, exists := seen[input.StepID]; exists {
			return fmt.Errorf("%w: duplicate step id", ErrInvalidFixture)
		}
		seen[input.StepID] = struct{}{}
		if input.Operation != OperationAdd && input.Operation != OperationSet {
			return fmt.Errorf("%w: unsupported operation", ErrInvalidFixture)
		}
		if config.Clock[index].Location() != time.UTC {
			return fmt.Errorf("%w: clock must be UTC", ErrInvalidFixture)
		}
		if index > 0 && config.Clock[index].Before(config.Clock[index-1]) {
			return fmt.Errorf("%w: clock must be monotonic", ErrInvalidFixture)
		}
	}
	return nil
}

func validateState(state State, limit int) error {
	if len(state.Applied) > limit {
		return ErrInvalidFixture
	}
	seen := make(map[string]struct{}, len(state.Applied))
	for _, stepID := range state.Applied {
		if !identifierPattern.MatchString(stepID) {
			return ErrInvalidFixture
		}
		if _, duplicate := seen[stepID]; duplicate {
			return ErrInvalidFixture
		}
		seen[stepID] = struct{}{}
	}
	return nil
}

func execute(initial State, inputs []Input, clock []time.Time, random []uint64) ([]Receipt, State, error) {
	if len(inputs) != len(clock) || len(inputs) != len(random) {
		return nil, State{}, ErrIncompleteTape
	}
	state := cloneState(initial)
	receipts := make([]Receipt, 0, len(inputs))
	for index, input := range inputs {
		switch input.Operation {
		case OperationAdd:
			if (input.Value > 0 && state.Value > math.MaxInt64-input.Value) ||
				(input.Value < 0 && state.Value < math.MinInt64-input.Value) {
				return nil, State{}, fmt.Errorf("%w: state overflow", ErrInvalidFixture)
			}
			state.Value += input.Value
		case OperationSet:
			state.Value = input.Value
		default:
			return nil, State{}, ErrInvalidFixture
		}
		state.Applied = append(state.Applied, input.StepID)
		receipts = append(receipts, Receipt{StepID: input.StepID, At: clock[index], Nonce: random[index], Value: state.Value})
	}
	return receipts, state, nil
}

func (recording Recording) body() any {
	return struct {
		Version          string      `json:"version"`
		ArtifactDigest   string      `json:"artifact_digest"`
		FixtureID        string      `json:"fixture_id"`
		InitialState     State       `json:"initial_state"`
		Inputs           []Input     `json:"inputs"`
		Clock            []time.Time `json:"clock"`
		Random           []uint64    `json:"random"`
		Receipts         []Receipt   `json:"receipts"`
		FinalState       State       `json:"final_state"`
		TranscriptDigest string      `json:"transcript_digest"`
	}{
		Version: recording.Version, ArtifactDigest: recording.ArtifactDigest, FixtureID: recording.FixtureID,
		InitialState: recording.InitialState, Inputs: recording.Inputs, Clock: recording.Clock, Random: recording.Random,
		Receipts: recording.Receipts, FinalState: recording.FinalState, TranscriptDigest: recording.TranscriptDigest,
	}
}

func rawDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func cloneConfig(config Config) Config {
	return Config{
		FixtureID: config.FixtureID, InitialState: cloneState(config.InitialState),
		Inputs: cloneInputs(config.Inputs), Clock: append([]time.Time(nil), config.Clock...),
		Random: append([]uint64(nil), config.Random...),
	}
}

func cloneState(state State) State {
	state.Applied = append([]string(nil), state.Applied...)
	return state
}

func cloneInputs(inputs []Input) []Input {
	return append([]Input(nil), inputs...)
}
