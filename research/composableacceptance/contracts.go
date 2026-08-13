// Package composableacceptance defines the body-free record boundary for
// repository-shaped composable-runtime acceptance treatments.
package composableacceptance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
)

const (
	CorpusSchemaVersion = "pysolate.spark-scenario-corpus.v1"
	ReportSchemaVersion = "pysolate.composable-acceptance-report.v2"
)

const (
	rowStatusPassed   = "passed"
	rowStatusRejected = "rejected"
	rowStatusSkipped  = "skipped"
)

const maxCount = 1_000_000_000
const maxEventCount = 1_000_000_000
const maxTraceEvents = 4096

// TraceEventType is a closed set of runtime events emitted during direct-replay
// treatment execution.
type TraceEventType string

const (
	TraceEventTypeRunStart       TraceEventType = "run_start"
	TraceEventTypeRunTerminal    TraceEventType = "run_terminal"
	TraceEventTypeObservation    TraceEventType = "observation"
	TraceEventTypeGuestLifecycle TraceEventType = "guest_lifecycle"
	TraceEventTypeStreaming      TraceEventType = "streaming"
	TraceEventTypeWorkspace      TraceEventType = "workspace"
	TraceEventTypeFanout         TraceEventType = "fanout"
	TraceEventTypeCache          TraceEventType = "cache"
	TraceEventTypeSingleFlight   TraceEventType = "single_flight"
	TraceEventTypeWaitResume     TraceEventType = "wait_resume"
	TraceEventTypePrepared       TraceEventType = "prepared"
	TraceEventTypeCOW            TraceEventType = "cow"
	TraceEventTypeCancellation   TraceEventType = "cancellation"
	TraceEventTypeOracle         TraceEventType = "oracle"
)

var validTraceEventTypes = map[TraceEventType]struct{}{
	TraceEventTypeRunStart:       {},
	TraceEventTypeRunTerminal:    {},
	TraceEventTypeObservation:    {},
	TraceEventTypeGuestLifecycle: {},
	TraceEventTypeStreaming:      {},
	TraceEventTypeWorkspace:      {},
	TraceEventTypeFanout:         {},
	TraceEventTypeCache:          {},
	TraceEventTypeSingleFlight:   {},
	TraceEventTypeWaitResume:     {},
	TraceEventTypePrepared:       {},
	TraceEventTypeCOW:            {},
	TraceEventTypeCancellation:   {},
	TraceEventTypeOracle:         {},
}

// TraceEventOutcome captures the semantic result of each trace event.
type TraceEventOutcome string

const (
	TraceEventOutcomeStarted   TraceEventOutcome = "started"
	TraceEventOutcomeOK        TraceEventOutcome = "ok"
	TraceEventOutcomeRejected  TraceEventOutcome = "rejected"
	TraceEventOutcomeSkipped   TraceEventOutcome = "skipped"
	TraceEventOutcomeError     TraceEventOutcome = "error"
	TraceEventOutcomeCancelled TraceEventOutcome = "cancelled"
	TraceEventOutcomeHit       TraceEventOutcome = "hit"
	TraceEventOutcomeMiss      TraceEventOutcome = "miss"
	TraceEventOutcomeLeader    TraceEventOutcome = "leader"
	TraceEventOutcomeFollower  TraceEventOutcome = "follower"
	TraceEventOutcomeConflict  TraceEventOutcome = "conflict"
	TraceEventOutcomeRecovered TraceEventOutcome = "recovered"
	TraceEventOutcomeSelected  TraceEventOutcome = "selected"
	TraceEventOutcomeDiscarded TraceEventOutcome = "discarded"
	TraceEventOutcomeConsumed  TraceEventOutcome = "consumed"
	TraceEventOutcomeMapped    TraceEventOutcome = "mapped"
	TraceEventOutcomeSealed    TraceEventOutcome = "sealed"
)

var validTraceEventOutcomes = map[TraceEventOutcome]struct{}{
	TraceEventOutcomeStarted:   {},
	TraceEventOutcomeOK:        {},
	TraceEventOutcomeRejected:  {},
	TraceEventOutcomeSkipped:   {},
	TraceEventOutcomeError:     {},
	TraceEventOutcomeCancelled: {},
	TraceEventOutcomeHit:       {},
	TraceEventOutcomeMiss:      {},
	TraceEventOutcomeLeader:    {},
	TraceEventOutcomeFollower:  {},
	TraceEventOutcomeConflict:  {},
	TraceEventOutcomeRecovered: {},
	TraceEventOutcomeSelected:  {},
	TraceEventOutcomeDiscarded: {},
	TraceEventOutcomeConsumed:  {},
	TraceEventOutcomeMapped:    {},
	TraceEventOutcomeSealed:    {},
}

type terminalExpectation struct {
	outcome TraceEventOutcome
}

var terminalByRowStatus = map[string]terminalExpectation{
	rowStatusPassed:   {outcome: TraceEventOutcomeOK},
	rowStatusRejected: {outcome: TraceEventOutcomeRejected},
	rowStatusSkipped:  {outcome: TraceEventOutcomeSkipped},
}

type TraceEvent struct {
	Sequence              uint32            `json:"sequence"`
	ParentSequence        *uint32           `json:"parent_sequence,omitempty"`
	Type                  TraceEventType    `json:"type"`
	Action                string            `json:"action"`
	Outcome               TraceEventOutcome `json:"outcome"`
	CheckpointSHA256      string            `json:"checkpoint_sha256"`
	CheckpointStatus      string            `json:"checkpoint_status"`
	InputSHA256           string            `json:"input_sha256,omitempty"`
	OutputSHA256          string            `json:"output_sha256,omitempty"`
	Count                 uint64            `json:"count"`
	RelativeElapsedMillis float64           `json:"relative_elapsed_millis"`
	TerminalDisposition   string            `json:"terminal_disposition,omitempty"`
}

type Treatment string

const (
	TreatmentFresh           Treatment = "fresh"
	TreatmentStreaming       Treatment = "streaming"
	TreatmentFanout          Treatment = "fanout"
	TreatmentCacheOff        Treatment = "cache_off"
	TreatmentCacheOn         Treatment = "cache_on"
	TreatmentSingleFlightOff Treatment = "single_flight_off"
	TreatmentSingleFlightOn  Treatment = "single_flight_on"
	TreatmentReevaluationOff Treatment = "reevaluation_off"
	TreatmentReevaluationOn  Treatment = "reevaluation_on"
	TreatmentPrepared        Treatment = "prepared"
	TreatmentCOW             Treatment = "cow"
	TreatmentAll             Treatment = "all"
	TreatmentInvalidParent   Treatment = "invalid_parent"
	TreatmentInvalidChild    Treatment = "invalid_child"
	TreatmentChangedObserve  Treatment = "changed_observation"
	TreatmentBranchConflict  Treatment = "branch_conflict"
	TreatmentCacheCorruption Treatment = "cache_corruption"
	TreatmentCancellation    Treatment = "cancellation"
)

var TreatmentOrder = []Treatment{
	TreatmentFresh, TreatmentStreaming, TreatmentFanout,
	TreatmentCacheOff, TreatmentCacheOn,
	TreatmentSingleFlightOff, TreatmentSingleFlightOn,
	TreatmentReevaluationOff, TreatmentReevaluationOn,
	TreatmentPrepared, TreatmentCOW, TreatmentAll,
	TreatmentInvalidParent, TreatmentInvalidChild, TreatmentChangedObserve,
	TreatmentBranchConflict, TreatmentCacheCorruption, TreatmentCancellation,
}

var (
	ErrInvalid         = errors.New("invalid composable acceptance record")
	digestRE           = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitRE           = regexp.MustCompile(`^[0-9a-f]{40}$`)
	idRE               = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	modelRE            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,127}$`)
	actionRE           = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	checkpointStatusRE = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	rowDispositionRE   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

type Corpus struct {
	SchemaVersion string     `json:"schema_version"`
	SourceCommit  string     `json:"source_commit"`
	Model         string     `json:"model"`
	Scenarios     []Scenario `json:"scenarios"`
}

type Scenario struct {
	ID                     string   `json:"id"`
	Task                   string   `json:"task"`
	Files                  []string `json:"files"`
	ChildAnalyses          []string `json:"child_analyses"`
	RepeatedTransformation string   `json:"repeated_transformation"`
	WaitBoundary           string   `json:"wait_boundary"`
	Observation            string   `json:"observation"`
	SelectedChild          int      `json:"selected_child"`
	ExpectedArtifact       string   `json:"expected_artifact"`
	ProhibitedOutputs      []string `json:"prohibited_outputs"`
}

type Report struct {
	SchemaVersion       string `json:"schema_version"`
	SourceCommit        string `json:"source_commit"`
	GuestArtifactSHA256 string `json:"guest_artifact_sha256"`
	CorpusSHA256        string `json:"corpus_sha256"`
	Model               string `json:"model"`
	Rows                []Row  `json:"rows"`
}

type Row struct {
	ScenarioID            string       `json:"scenario_id"`
	ScenarioSHA256        string       `json:"scenario_sha256"`
	Treatment             Treatment    `json:"treatment"`
	Status                string       `json:"status"`
	OracleSHA256          string       `json:"oracle_sha256"`
	SelectedRootSHA256    string       `json:"selected_root_sha256,omitempty"`
	GuestCreated          uint64       `json:"guest_created"`
	GuestDestroyed        uint64       `json:"guest_destroyed"`
	CacheHits             uint64       `json:"cache_hits"`
	FlightFollowers       uint64       `json:"flight_followers"`
	ChangedBytes          uint64       `json:"changed_bytes"`
	MaterializedBytes     uint64       `json:"materialized_bytes"`
	RelativeElapsedMillis float64      `json:"relative_elapsed_millis"`
	EvidenceScope         string       `json:"evidence_scope"`
	ConformanceSHA256     string       `json:"conformance_sha256"`
	TerminalDisposition   string       `json:"terminal_disposition"`
	EvidenceComplete      bool         `json:"evidence_complete"`
	Trace                 []TraceEvent `json:"trace"`
}

func EncodeCorpus(value Corpus) ([]byte, string, error) {
	if value.Validate() != nil {
		return nil, "", ErrInvalid
	}
	data, err := encodeCanonical(value)
	if err != nil {
		return nil, "", err
	}
	return data, digest(data), nil
}

func DecodeCorpus(data []byte) (Corpus, string, error) {
	var value Corpus
	if err := decodeStrict(data, &value); err != nil || value.Validate() != nil {
		return Corpus{}, "", ErrInvalid
	}
	canonical, err := encodeCanonical(value)
	if err != nil || !bytes.Equal(data, canonical) {
		return Corpus{}, "", ErrInvalid
	}
	return value, digest(canonical), nil
}

func EncodeReport(value Report) ([]byte, string, error) {
	if err := value.Validate(); err != nil {
		return nil, "", err
	}
	data, err := encodeCanonical(value)
	if err != nil {
		return nil, "", err
	}
	return data, digest(data), nil
}

func DecodeReport(data []byte, destination *Report) error {
	if destination == nil || decodeStrict(data, destination) != nil || destination.Validate() != nil {
		return ErrInvalid
	}
	canonical, err := encodeCanonical(*destination)
	if err != nil || !bytes.Equal(data, canonical) {
		return ErrInvalid
	}
	return nil
}

func (value Corpus) Validate() error {
	if value.SchemaVersion != CorpusSchemaVersion || !commitRE.MatchString(value.SourceCommit) || !modelRE.MatchString(value.Model) || len(value.Scenarios) == 0 || len(value.Scenarios) > 100 {
		return ErrInvalid
	}
	seen := map[string]struct{}{}
	for _, scenario := range value.Scenarios {
		if err := scenario.validate(); err != nil {
			return err
		}
		if _, exists := seen[scenario.ID]; exists {
			return ErrInvalid
		}
		seen[scenario.ID] = struct{}{}
	}
	return nil
}

func (scenario Scenario) validate() error {
	if !idRE.MatchString(scenario.ID) || len(scenario.Task) < 20 || len(scenario.Files) < 2 || len(scenario.ChildAnalyses) != 2 || scenario.SelectedChild < 0 || scenario.SelectedChild > 1 || scenario.ExpectedArtifact == "" || scenario.RepeatedTransformation == "" || scenario.WaitBoundary == "" || scenario.Observation == "" {
		return ErrInvalid
	}
	for _, path := range scenario.Files {
		if path == "" || path[0] == '/' || bytes.Contains([]byte(path), []byte("..")) {
			return ErrInvalid
		}
	}
	return nil
}

func (value Report) Validate() error {
	if value.SchemaVersion != ReportSchemaVersion || !commitRE.MatchString(value.SourceCommit) || !digestRE.MatchString(value.GuestArtifactSHA256) || !digestRE.MatchString(value.CorpusSHA256) || !modelRE.MatchString(value.Model) || len(value.Rows) == 0 {
		return ErrInvalid
	}
	order := make(map[Treatment]int, len(TreatmentOrder))
	for index, treatment := range TreatmentOrder {
		order[treatment] = index
	}
	seen := map[string]struct{}{}
	for index, row := range value.Rows {
		position, known := order[row.Treatment]
		if !known || !idRE.MatchString(row.ScenarioID) || !digestRE.MatchString(row.ScenarioSHA256) || !digestRE.MatchString(row.OracleSHA256) || !digestRE.MatchString(row.ConformanceSHA256) || row.EvidenceScope != "direct_replay" || row.RelativeElapsedMillis < 0 || row.GuestDestroyed > row.GuestCreated {
			return ErrInvalid
		}
		if !isFiniteNonNegativeFloat(row.RelativeElapsedMillis) {
			return ErrInvalid
		}
		if row.GuestCreated > maxCount || row.GuestDestroyed > maxCount || row.CacheHits > maxCount || row.FlightFollowers > maxCount {
			return ErrInvalid
		}
		if err := row.validate(); err != nil {
			return ErrInvalid
		}
		key := fmt.Sprintf("%s/%s", row.ScenarioID, row.Treatment)
		if _, exists := seen[key]; exists {
			return ErrInvalid
		}
		seen[key] = struct{}{}
		if index > 0 {
			previous := value.Rows[index-1]
			if previous.ScenarioID > row.ScenarioID || (previous.ScenarioID == row.ScenarioID && order[previous.Treatment] > position) {
				return ErrInvalid
			}
		}
		if row.Status == "passed" && (!row.EvidenceComplete || row.GuestCreated != row.GuestDestroyed) {
			return ErrInvalid
		}
	}
	return nil
}

func (value Row) validate() error {
	if !idRE.MatchString(value.ScenarioID) || !digestRE.MatchString(value.ScenarioSHA256) || !digestRE.MatchString(value.OracleSHA256) || !digestRE.MatchString(value.ConformanceSHA256) || value.EvidenceScope != "direct_replay" || value.GuestDestroyed > value.GuestCreated || !isValidRowStatus(value.Status) || !isRowTerminalDispositionValid(value.TerminalDisposition) {
		return ErrInvalid
	}
	expectation, found := terminalByRowStatus[value.Status]
	if !found {
		return ErrInvalid
	}
	if value.Status == rowStatusPassed && (!value.EvidenceComplete || value.GuestCreated != value.GuestDestroyed) {
		return ErrInvalid
	}
	return value.validateTrace(expectation)
}

func (value Row) validateTrace(expectation terminalExpectation) error {
	if len(value.Trace) < 2 || len(value.Trace) > maxTraceEvents {
		return invalidTrace("invalid trace event count")
	}
	if value.Status != rowStatusSkipped && len(value.Trace) < 3 {
		return invalidTrace("missing operation trace")
	}
	if value.Trace[0].Sequence != 1 || value.Trace[0].Type != TraceEventTypeRunStart || value.Trace[0].Outcome != TraceEventOutcomeStarted || value.Trace[0].Action != "run.start" || value.Trace[0].ParentSequence != nil {
		return invalidTrace("invalid trace start")
	}
	if value.Trace[len(value.Trace)-1].Type != TraceEventTypeRunTerminal {
		return invalidTrace("missing trace terminal")
	}
	var previousElapsed float64
	wasPreviousSet := false
	for index, event := range value.Trace {
		if !isTraceEventTypeValid(event.Type) || !isTraceEventOutcomeValid(event.Outcome) || !isTraceEventActionValid(event.Action) {
			return invalidTrace("invalid trace enum")
		}
		expectedSequence := uint32(index + 1)
		if event.Sequence != expectedSequence {
			return invalidTrace("non-sequential trace")
		}
		if event.CheckpointSHA256 == "" != (event.CheckpointStatus == "") {
			return invalidTrace("invalid checkpoint metadata")
		}
		if event.CheckpointSHA256 != "" && (!digestRE.MatchString(event.CheckpointSHA256) || !checkpointStatusRE.MatchString(event.CheckpointStatus)) {
			return invalidTrace("invalid checkpoint metadata")
		}
		if !isFiniteNonNegativeFloat(event.RelativeElapsedMillis) || (wasPreviousSet && event.RelativeElapsedMillis < previousElapsed) {
			return invalidTrace("invalid elapsed millis")
		}
		if event.InputSHA256 != "" && !digestRE.MatchString(event.InputSHA256) {
			return invalidTrace("invalid input digest")
		}
		if event.OutputSHA256 != "" && !digestRE.MatchString(event.OutputSHA256) {
			return invalidTrace("invalid output digest")
		}
		if event.Count > maxEventCount {
			return invalidTrace("invalid event metrics")
		}
		if index == 0 {
			if event.ParentSequence != nil {
				return invalidTrace("unexpected parent sequence")
			}
		} else if index == len(value.Trace)-1 {
			if event.Outcome != expectation.outcome {
				return invalidTrace("terminal outcome/disposition mismatch")
			}
			if event.Action != "run.terminal" || event.TerminalDisposition != value.TerminalDisposition {
				return invalidTrace("terminal outcome/disposition mismatch")
			}
		} else {
			if event.Type == TraceEventTypeRunTerminal || event.TerminalDisposition != "" {
				return invalidTrace("unexpected terminal event layout")
			}
		}
		if index > 0 && event.ParentSequence == nil {
			return invalidTrace("missing parent sequence")
		}
		if event.ParentSequence != nil {
			if *event.ParentSequence == 0 || *event.ParentSequence >= event.Sequence {
				return invalidTrace("invalid parent sequence")
			}
			if int(*event.ParentSequence) > index {
				return invalidTrace("parent sequence after current")
			}
		}
		wasPreviousSet = true
		previousElapsed = event.RelativeElapsedMillis
	}
	if !isFiniteNonNegativeFloat(value.RelativeElapsedMillis) || value.Trace[len(value.Trace)-1].RelativeElapsedMillis > value.RelativeElapsedMillis {
		return invalidTrace("invalid trace timing")
	}
	return nil
}

func invalidTrace(context string) error {
	return fmt.Errorf("trace validation failed: %s: %w", context, ErrInvalid)
}

func isTraceEventTypeValid(value TraceEventType) bool {
	_, found := validTraceEventTypes[value]
	return found
}

func isTraceEventOutcomeValid(value TraceEventOutcome) bool {
	_, found := validTraceEventOutcomes[value]
	return found
}

func isTraceEventActionValid(value string) bool {
	return actionRE.MatchString(value)
}

func isRowTerminalDispositionValid(value string) bool {
	return rowDispositionRE.MatchString(value)
}

func isValidRowStatus(value string) bool {
	_, found := terminalByRowStatus[value]
	return found
}

func isFiniteNonNegativeFloat(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func ArtifactIdentity(value string) string {
	return digest([]byte(value))
}

func ScenarioIdentity(value Scenario) (string, error) {
	if err := value.validate(); err != nil {
		return "", err
	}
	data, err := encodeCanonical(value)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func SortRows(rows []Row) {
	order := make(map[Treatment]int, len(TreatmentOrder))
	for index, treatment := range TreatmentOrder {
		order[treatment] = index
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ScenarioID != rows[j].ScenarioID {
			return rows[i].ScenarioID < rows[j].ScenarioID
		}
		return order[rows[i].Treatment] < order[rows[j].Treatment]
	})
}

func encodeCanonical(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
