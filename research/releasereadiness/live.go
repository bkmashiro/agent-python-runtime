package releasereadiness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

const (
	ProgramSchemaVersion  = "pysolate.release-readiness-program.v1"
	EvidenceSchemaVersion = "pysolate.release-readiness-stream-evidence.v1"
)

type ProgramResponse struct {
	SchemaVersion string `json:"schema_version"`
	Summary       string `json:"summary"`
	PythonSource  string `json:"python_source"`
}

type Statement struct {
	Index    int    `json:"index"`
	Source   string `json:"source"`
	ClosedNS uint64 `json:"closed_ns"`
}

type ToolCall struct {
	Capability string `json:"capability"`
	Statement  int    `json:"statement"`
	EligibleNS uint64 `json:"eligible_ns"`
	LatencyNS  uint64 `json:"latency_ns"`
	ReadyNS    uint64 `json:"ready_ns"`
}

type LaneProjection struct {
	SequentialReadyNS     uint64 `json:"post_source_sequential_ready_ns"`
	ParallelReadyNS       uint64 `json:"post_source_parallel_ready_ns"`
	PrefixReadyNS         uint64 `json:"prefix_pre_dispatch_ready_ns"`
	SavingsVsSequentialNS uint64 `json:"savings_vs_sequential_ns"`
	SavingsVsParallelNS   uint64 `json:"savings_vs_parallel_ns"`
}

type LaneReplay struct {
	Lane             string `json:"lane"`
	Order            int    `json:"order"`
	ProjectedReadyNS uint64 `json:"projected_ready_ns"`
	ObservedReadyNS  uint64 `json:"observed_ready_ns"`
	DriftNS          int64  `json:"drift_ns"`
}

type Evidence struct {
	SchemaVersion    string                               `json:"schema_version"`
	RunIndex         int                                  `json:"run_index"`
	Model            string                               `json:"model"`
	ResponseID       string                               `json:"response_id"`
	TraceSHA256      string                               `json:"trace_sha256"`
	StartedAt        string                               `json:"started_at"`
	Network          agenttrajectory.ObservedNetworkTrace `json:"network"`
	FirstSSEEventNS  uint64                               `json:"first_sse_event_ns"`
	FirstReasoningNS uint64                               `json:"first_reasoning_ns"`
	FirstContentNS   uint64                               `json:"first_content_ns"`
	SourceCompleteNS uint64                               `json:"source_complete_ns"`
	StreamCompleteNS uint64                               `json:"stream_complete_ns"`
	EligibleWindowNS uint64                               `json:"eligible_window_ns"`
	Reasoning        string                               `json:"reasoning"`
	Content          string                               `json:"content"`
	Usage            agenttrajectory.TokenUsage           `json:"usage"`
	Statements       []Statement                          `json:"statements"`
	ToolCalls        []ToolCall                           `json:"tool_calls"`
	Projection       LaneProjection                       `json:"projection"`
	Replays          []LaneReplay                         `json:"replays"`
}

var toolSpecs = []struct {
	name    string
	marker  string
	latency time.Duration
}{
	{"observability.query_metrics", "ops.query_metrics(", 2200 * time.Millisecond},
	{"observability.query_logs", "ops.query_logs(", 3100 * time.Millisecond},
	{"github.latest_deployment", "ops.latest_deployment(", 1000 * time.Millisecond},
	{"kubernetes.read_deployment", "ops.read_deployment(", 1400 * time.Millisecond},
}

func Project(raw agenttrajectory.RawCandidateStream, runIndex int) (Evidence, error) {
	if raw.SchemaVersion != agenttrajectory.RawCandidateStreamSchemaVersion || runIndex < 1 || raw.Model == "" || raw.ResponseID == "" || len(raw.Chunks) == 0 || strings.TrimSpace(raw.Content) == "" {
		return Evidence{}, errors.New("invalid raw release-readiness stream")
	}
	var response ProgramResponse
	if json.Unmarshal([]byte(raw.Content), &response) != nil || response.SchemaVersion != ProgramSchemaVersion || strings.TrimSpace(response.Summary) == "" {
		return Evidence{}, errors.New("invalid release-readiness response")
	}
	source := strings.TrimSuffix(response.PythonSource, "\n")
	lines := strings.Split(source, "\n")
	if len(lines) < 60 || len(lines) > 450 || strings.Contains(source, "open(") || strings.Contains(source, "subprocess") || strings.Contains(source, "requests.") {
		return Evidence{}, errors.New("release-readiness source outside contract")
	}
	encodedSource := []byte(strconv.Quote(response.PythonSource))
	sourceOffset := bytes.Index([]byte(raw.Content), encodedSource)
	htmlEscaped := false
	if sourceOffset < 0 {
		encodedSource, _ = json.Marshal(response.PythonSource)
		sourceOffset = bytes.Index([]byte(raw.Content), encodedSource)
		htmlEscaped = true
	}
	if sourceOffset < 0 {
		return Evidence{}, errors.New("source not found in streamed content")
	}
	statements := make([]Statement, 0, len(lines))
	calls := make([]ToolCall, 0, len(toolSpecs))
	seen := map[string]int{}
	for index, line := range lines {
		prefix := strings.Join(lines[:index+1], "\n")
		if index == len(lines)-1 && strings.HasSuffix(response.PythonSource, "\n") {
			prefix += "\n"
		}
		encodedPrefix := []byte(strconv.Quote(prefix))
		if htmlEscaped {
			encodedPrefix, _ = json.Marshal(prefix)
		}
		end := sourceOffset + len(encodedPrefix) - 1
		closed, ok := contentArrival(raw.Chunks, end)
		if !ok {
			return Evidence{}, errors.New("statement closure not found")
		}
		statements = append(statements, Statement{Index: index + 1, Source: line, ClosedNS: closed})
		for _, spec := range toolSpecs {
			if strings.Contains(line, spec.marker) {
				seen[spec.name]++
				calls = append(calls, ToolCall{Capability: spec.name, Statement: index + 1, EligibleNS: closed, LatencyNS: uint64(spec.latency), ReadyNS: closed + uint64(spec.latency)})
			}
		}
	}
	if len(calls) != len(toolSpecs) || !strings.Contains(source, "result =") {
		return Evidence{}, errors.New("required release-readiness operations missing")
	}
	for _, spec := range toolSpecs {
		if seen[spec.name] != 1 {
			return Evidence{}, errors.New("operation multiplicity mismatch")
		}
	}
	firstEvent, firstReasoning, firstContent := uint64(0), uint64(0), uint64(0)
	reasoning := strings.Builder{}
	for _, chunk := range raw.Chunks {
		if firstEvent == 0 {
			firstEvent = chunk.ElapsedNS
		}
		if chunk.Reasoning != "" {
			if firstReasoning == 0 {
				firstReasoning = chunk.ElapsedNS
			}
			reasoning.WriteString(chunk.Reasoning)
		}
		if firstContent == 0 && chunk.Content != "" {
			firstContent = chunk.ElapsedNS
		}
	}
	sourceComplete := statements[len(statements)-1].ClosedNS
	firstEligible := calls[0].EligibleNS
	var sum, maxLatency, prefixReady uint64
	for _, call := range calls {
		sum += call.LatencyNS
		if call.LatencyNS > maxLatency {
			maxLatency = call.LatencyNS
		}
		if call.ReadyNS > prefixReady {
			prefixReady = call.ReadyNS
		}
		if call.EligibleNS < firstEligible {
			firstEligible = call.EligibleNS
		}
	}
	if prefixReady < sourceComplete {
		prefixReady = sourceComplete
	}
	sequential := sourceComplete + sum
	parallel := sourceComplete + maxLatency
	if prefixReady > parallel {
		prefixReady = parallel
	}
	encodedRaw, _ := json.Marshal(raw)
	projection := LaneProjection{SequentialReadyNS: sequential, ParallelReadyNS: parallel, PrefixReadyNS: prefixReady, SavingsVsSequentialNS: sequential - prefixReady, SavingsVsParallelNS: parallel - prefixReady}
	return Evidence{SchemaVersion: EvidenceSchemaVersion, RunIndex: runIndex, Model: raw.Model, ResponseID: raw.ResponseID, TraceSHA256: digest(encodedRaw), StartedAt: raw.StartedAt, Network: raw.Network,
		FirstSSEEventNS: firstEvent, FirstReasoningNS: firstReasoning, FirstContentNS: firstContent, SourceCompleteNS: sourceComplete, StreamCompleteNS: raw.DoneElapsedNS, EligibleWindowNS: sourceComplete - firstEligible,
		Reasoning: reasoning.String(), Content: raw.Content, Usage: raw.Usage, Statements: statements, ToolCalls: calls, Projection: projection}, nil
}

func RunReplays(evidence *Evidence, order []string) error {
	if evidence == nil || len(order) != 3 {
		return errors.New("invalid replay")
	}
	projected := map[string]uint64{"post_source_sequential": evidence.Projection.SequentialReadyNS, "post_source_parallel": evidence.Projection.ParallelReadyNS, "prefix_pre_dispatch": evidence.Projection.PrefixReadyNS}
	for index, lane := range order {
		expected, ok := projected[lane]
		if !ok {
			return errors.New("unknown lane")
		}
		start := time.Now()
		switch lane {
		case "post_source_sequential":
			time.Sleep(time.Duration(evidence.SourceCompleteNS))
			for _, call := range evidence.ToolCalls {
				time.Sleep(time.Duration(call.LatencyNS))
			}
		case "post_source_parallel":
			time.Sleep(time.Duration(evidence.SourceCompleteNS))
			var wg sync.WaitGroup
			for _, call := range evidence.ToolCalls {
				wg.Add(1)
				go func(ns uint64) { defer wg.Done(); time.Sleep(time.Duration(ns)) }(call.LatencyNS)
			}
			wg.Wait()
		case "prefix_pre_dispatch":
			var wg sync.WaitGroup
			for _, call := range evidence.ToolCalls {
				wg.Add(1)
				go func(eligible, latency uint64) {
					defer wg.Done()
					time.Sleep(time.Duration(eligible))
					time.Sleep(time.Duration(latency))
				}(call.EligibleNS, call.LatencyNS)
			}
			time.Sleep(time.Duration(evidence.SourceCompleteNS))
			wg.Wait()
		}
		observed := uint64(time.Since(start))
		evidence.Replays = append(evidence.Replays, LaneReplay{Lane: lane, Order: index + 1, ProjectedReadyNS: expected, ObservedReadyNS: observed, DriftNS: int64(observed) - int64(expected)})
	}
	return nil
}

func contentArrival(chunks []agenttrajectory.StreamChunk, contentEnd int) (uint64, bool) {
	seen := 0
	for _, chunk := range chunks {
		seen += len([]byte(chunk.Content))
		if seen >= contentEnd {
			return chunk.ElapsedNS, true
		}
	}
	return 0, false
}
func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
