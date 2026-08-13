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
	"regexp"
	"sort"
)

const (
	CorpusSchemaVersion = "pysolate.spark-scenario-corpus.v1"
	ReportSchemaVersion = "pysolate.composable-acceptance-report.v1"
)

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
	ErrInvalid = errors.New("invalid composable acceptance record")
	digestRE   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitRE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	idRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
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
	ScenarioID            string    `json:"scenario_id"`
	ScenarioSHA256        string    `json:"scenario_sha256"`
	Treatment             Treatment `json:"treatment"`
	Status                string    `json:"status"`
	OracleSHA256          string    `json:"oracle_sha256"`
	SelectedRootSHA256    string    `json:"selected_root_sha256,omitempty"`
	GuestCreated          uint64    `json:"guest_created"`
	GuestDestroyed        uint64    `json:"guest_destroyed"`
	CacheHits             uint64    `json:"cache_hits"`
	FlightFollowers       uint64    `json:"flight_followers"`
	ChangedBytes          uint64    `json:"changed_bytes"`
	MaterializedBytes     uint64    `json:"materialized_bytes"`
	RelativeElapsedMillis float64   `json:"relative_elapsed_millis"`
	TerminalDisposition   string    `json:"terminal_disposition"`
	EvidenceComplete      bool      `json:"evidence_complete"`
}

func DecodeCorpus(data []byte) (Corpus, string, error) {
	var value Corpus
	if err := decodeStrict(data, &value); err != nil || value.Validate() != nil {
		return Corpus{}, "", ErrInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(data, canonical) {
		return Corpus{}, "", ErrInvalid
	}
	return value, digest(canonical), nil
}

func EncodeReport(value Report) ([]byte, string, error) {
	if err := value.Validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	return data, digest(data), nil
}

func (value Corpus) Validate() error {
	if value.SchemaVersion != CorpusSchemaVersion || !commitRE.MatchString(value.SourceCommit) || value.Model != "gpt-5.3-codex-spark" || len(value.Scenarios) != 3 {
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
	if value.SchemaVersion != ReportSchemaVersion || !commitRE.MatchString(value.SourceCommit) || !digestRE.MatchString(value.GuestArtifactSHA256) || !digestRE.MatchString(value.CorpusSHA256) || value.Model != "gpt-5.3-codex-spark" || len(value.Rows) == 0 {
		return ErrInvalid
	}
	order := make(map[Treatment]int, len(TreatmentOrder))
	for index, treatment := range TreatmentOrder {
		order[treatment] = index
	}
	seen := map[string]struct{}{}
	for index, row := range value.Rows {
		position, known := order[row.Treatment]
		if !known || !idRE.MatchString(row.ScenarioID) || !digestRE.MatchString(row.ScenarioSHA256) || !digestRE.MatchString(row.OracleSHA256) || row.RelativeElapsedMillis < 0 || row.GuestDestroyed > row.GuestCreated || (row.Status != "passed" && row.Status != "rejected" && row.Status != "skipped") || row.TerminalDisposition == "" {
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

func ArtifactIdentity(value string) string {
	return digest([]byte(value))
}

func ScenarioIdentity(value Scenario) (string, error) {
	if err := value.validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
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
