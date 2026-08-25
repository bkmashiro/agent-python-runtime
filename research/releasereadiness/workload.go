package releasereadiness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const RecordedRunOneSourceSHA256 = "sha256:e2c66fe01cb1ffc98476972a38b82ecf36cfc9636ef91d6f002c3d56acf4f2a1"

const ExpectedFixtureResultSHA256 = "sha256:e115a948c10891a054ce386a709849bf9f765a15a1e11eab7672bc4e1859a423"

type RecordedStatement struct {
	Index    int    `json:"index"`
	Source   string `json:"source"`
	ClosedNS int64  `json:"closed_ns"`
}

type RecordedToolCall struct {
	Capability string `json:"capability"`
	Statement  int    `json:"statement"`
	EligibleNS int64  `json:"eligible_ns"`
	LatencyNS  int64  `json:"latency_ns"`
	ReadyNS    int64  `json:"ready_ns"`
}

type RecordedWorkload struct {
	SchemaVersion             string              `json:"schema_version"`
	SourceEvidenceSchema      string              `json:"source_evidence_schema"`
	SourceEvidenceTraceSHA256 string              `json:"source_evidence_trace_sha256"`
	RunIndex                  int                 `json:"run_index"`
	Model                     string              `json:"model"`
	ResponseID                string              `json:"response_id"`
	SourceSHA256              string              `json:"source_sha256"`
	SourceCompleteNS          int64               `json:"source_complete_ns"`
	EligibleWindowNS          int64               `json:"eligible_window_ns"`
	Source                    string              `json:"source"`
	Statements                []RecordedStatement `json:"statements"`
	ToolCalls                 []RecordedToolCall  `json:"tool_calls"`
}

func LoadRecordedWorkload(path string) (RecordedWorkload, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return RecordedWorkload{}, err
	}
	var workload RecordedWorkload
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&workload) != nil {
		return RecordedWorkload{}, errors.New("invalid recorded release-readiness workload")
	}
	if err := workload.Validate(); err != nil {
		return RecordedWorkload{}, err
	}
	return workload, nil
}

func (workload RecordedWorkload) Validate() error {
	if workload.SchemaVersion != "pysolate.release-readiness-recorded-workload.v1" || workload.RunIndex != 1 ||
		workload.SourceSHA256 != RecordedRunOneSourceSHA256 || workload.SourceCompleteNS <= 0 || workload.EligibleWindowNS <= 0 ||
		len(workload.Statements) != 379 || len(workload.ToolCalls) != 4 || workload.Source == "" || !strings.HasSuffix(workload.Source, "\n") {
		return errors.New("recorded release-readiness workload identity drift")
	}
	if digestBytes([]byte(workload.Source)) != workload.SourceSHA256 {
		return errors.New("recorded release-readiness source digest mismatch")
	}
	var source strings.Builder
	var previous int64
	for index, statement := range workload.Statements {
		if statement.Index != index+1 || statement.Source == "" && index == len(workload.Statements)-1 || statement.ClosedNS <= previous {
			return errors.New("recorded release-readiness statement schedule is invalid")
		}
		previous = statement.ClosedNS
		source.WriteString(statement.Source)
		source.WriteByte('\n')
	}
	if source.String() != workload.Source || previous != workload.SourceCompleteNS {
		return errors.New("recorded release-readiness source reconstruction mismatch")
	}
	want := []string{"observability.query_metrics", "observability.query_logs", "github.latest_deployment", "kubernetes.read_deployment"}
	for index, call := range workload.ToolCalls {
		if call.Capability != want[index] || call.Statement < 1 || call.Statement > len(workload.Statements) ||
			call.EligibleNS != workload.Statements[call.Statement-1].ClosedNS || call.LatencyNS <= 0 || call.ReadyNS != call.EligibleNS+call.LatencyNS {
			return fmt.Errorf("recorded release-readiness tool call %d is invalid", index)
		}
	}
	if workload.SourceCompleteNS-workload.ToolCalls[0].EligibleNS != workload.EligibleWindowNS {
		return errors.New("recorded release-readiness eligible window mismatch")
	}
	for _, forbidden := range []string{"datetime.now(", "datetime.utcnow(", "time.time("} {
		if strings.Contains(workload.Source, forbidden) {
			return errors.New("recorded release-readiness source contains a wall-clock dependency")
		}
	}
	return nil
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func digestJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return digestBytes(body)
}
