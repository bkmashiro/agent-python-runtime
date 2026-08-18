package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
	"github.com/bkmashiro/agent-python-runtime/research/releasereadiness"
)

type apiRequest struct {
	Model          string                         `json:"model"`
	Messages       []agenttrajectory.ModelMessage `json:"messages"`
	MaxTokens      int                            `json:"max_tokens"`
	Stream         bool                           `json:"stream"`
	StreamOptions  map[string]bool                `json:"stream_options"`
	ResponseFormat map[string]string              `json:"response_format"`
	Thinking       map[string]string              `json:"thinking"`
}
type apiChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage agenttrajectory.TokenUsage `json:"usage"`
}
type sample struct {
	SchemaVersion string                      `json:"schema_version"`
	RecordedAt    string                      `json:"recorded_at"`
	Platform      string                      `json:"platform"`
	Endpoint      string                      `json:"endpoint"`
	Model         string                      `json:"model"`
	RequestedRuns int                         `json:"requested_runs"`
	AcceptedRuns  int                         `json:"accepted_runs"`
	RejectedRuns  int                         `json:"rejected_runs"`
	Method        string                      `json:"method"`
	Runs          []releasereadiness.Evidence `json:"runs"`
	Summary       summary                     `json:"summary"`
}
type summary struct {
	MedianFirstResponseByteNS     uint64 `json:"median_first_response_byte_ns"`
	MedianFirstReasoningNS        uint64 `json:"median_first_reasoning_ns"`
	MedianFirstContentNS          uint64 `json:"median_first_content_ns"`
	MedianSourceCompleteNS        uint64 `json:"median_source_complete_ns"`
	MedianEligibleWindowNS        uint64 `json:"median_eligible_window_ns"`
	MedianSourceLineCount         uint64 `json:"median_source_line_count"`
	MedianSavingsVsSequentialNS   uint64 `json:"median_savings_vs_sequential_ns"`
	MedianSavingsVsParallelNS     uint64 `json:"median_savings_vs_parallel_ns"`
	MinSavingsVsParallelNS        uint64 `json:"min_savings_vs_parallel_ns"`
	MaxSavingsVsParallelNS        uint64 `json:"max_savings_vs_parallel_ns"`
	MedianSequentialReplayDriftNS int64  `json:"median_sequential_replay_drift_ns"`
	MedianParallelReplayDriftNS   int64  `json:"median_parallel_replay_drift_ns"`
	MedianPrefixReplayDriftNS     int64  `json:"median_prefix_replay_drift_ns"`
}

const systemPrompt = `You generate a complete Python release-readiness analysis program. Return one JSON object only. The program runs in a fresh isolated Guest where a read-only ops capability module is already available as the name ops. Do not import modules, access files, use network libraries, execute subprocesses, mutate external state, or invent observations. All external reads must be the four specified ops calls. The remaining program must be deterministic pure Python that transforms only their returned dictionaries.`

const userPrompt = `Create a realistic release-readiness and incident-snapshot program for service checkout.

Return exactly:
{"schema_version":"pysolate.release-readiness-program.v1","summary":"...","python_source":"..."}

The Python source must:
1. Be 80–140 nonblank physical lines of substantive, executable Python, with no artificial sleeps or padding comments.
2. Collect inputs near the beginning using each of these complete one-line statements exactly once:
metrics = ops.query_metrics(service="checkout", window="6h")
logs = ops.query_logs(service="checkout", severity="error", window="6h")
deployment = ops.latest_deployment(repository="shop/checkout")
config = ops.read_deployment(cluster="prod-eu", namespace="checkout")
3. After those reads, continue with substantial pure-Python normalization, validation, time-bucket aggregation, error grouping, deployment correlation, configuration-drift checks, threshold evaluation, evidence-table construction, confidence scoring, release-gate decisions, and a final structured report.
4. Do not use comprehensions or compound statements that hide many operations on one physical line. Prefer readable helper functions and intermediate variables so the source represents a realistic long generated program.
5. Assign the final JSON-serializable dictionary to result. Do not print it.
6. Treat missing keys conservatively and never fabricate observed values.`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	var output, model, replayPrivate string
	var runs int
	flag.StringVar(&output, "output", "", "new private output directory")
	flag.StringVar(&model, "model", "deepseek-v4-flash", "pinned model")
	flag.StringVar(&replayPrivate, "replay-private", "", "existing private raw stream directory; makes no provider calls")
	flag.IntVar(&runs, "runs", 5, "bounded live calls")
	flag.Parse()
	key := os.Getenv("DEEPSEEK_API_KEY")
	if output == "" || (replayPrivate == "" && key == "") || runs < 1 || (replayPrivate == "" && runs > 5) || (replayPrivate != "" && runs > 10) || (model != "deepseek-v4-flash" && model != "deepseek-v4-pro") {
		return errors.New("output, live DEEPSEEK_API_KEY or replay-private, bounded runs, and supported model required")
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		return err
	}
	client := &http.Client{Timeout: 180 * time.Second}
	accepted := make([]releasereadiness.Evidence, 0, runs)
	rejected := 0
	for index := 1; index <= runs; index++ {
		var raw agenttrajectory.RawCandidateStream
		var err error
		if replayPrivate == "" {
			raw, err = record(context.Background(), client, key, model)
		} else {
			raw, err = readRaw(filepath.Join(replayPrivate, fmt.Sprintf("run-%02d-private-stream.json", index)))
		}
		rawPath := filepath.Join(output, fmt.Sprintf("run-%02d-private-stream.json", index))
		if replayPrivate == "" && len(raw.Chunks) > 0 {
			if writeErr := writeJSON(rawPath, raw, 0o600); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			rejected++
			fmt.Printf("run=%d rejected=%v raw=%s\n", index, err, rawPath)
			continue
		}
		evidence, err := releasereadiness.Project(raw, index)
		if err != nil {
			rejected++
			fmt.Printf("run=%d rejected=%v raw=%s\n", index, err, rawPath)
			continue
		}
		if err := validatePythonSyntax(evidence.Content); err != nil {
			rejected++
			fmt.Printf("run=%d rejected=invalid_python_syntax:%v raw=%s\n", index, err, rawPath)
			continue
		}
		orders := [][]string{{"post_source_sequential", "post_source_parallel", "prefix_pre_dispatch"}, {"prefix_pre_dispatch", "post_source_parallel", "post_source_sequential"}, {"post_source_parallel", "prefix_pre_dispatch", "post_source_sequential"}}
		if err := releasereadiness.RunReplays(&evidence, orders[(index-1)%len(orders)]); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(output, fmt.Sprintf("run-%02d-evidence.json", index)), evidence, 0o600); err != nil {
			return err
		}
		accepted = append(accepted, evidence)
		fmt.Printf("run=%d lines=%d first_content=%.3fs source=%.3fs window=%.3fs saving_vs_parallel=%.3fs\n", index, len(evidence.Statements), seconds(evidence.FirstContentNS), seconds(evidence.SourceCompleteNS), seconds(evidence.EligibleWindowNS), seconds(evidence.Projection.SavingsVsParallelNS))
	}
	if len(accepted) == 0 {
		return errors.New("no accepted release-readiness streams")
	}
	result := sample{SchemaVersion: "pysolate.release-readiness-live-sample.v1", RecordedAt: time.Now().UTC().Format(time.RFC3339), Platform: runtime.GOOS + "/" + runtime.GOARCH, Endpoint: "api.deepseek.com", Model: model, RequestedRuns: runs, AcceptedRuns: len(accepted), RejectedRuns: rejected,
		Method: "Live provider SSE with no authored pause; complete reasoning/content/chunk timings retained privately. Four deterministic read-only capability latencies are replayed in sequential, parallel, and prefix schedules. Replay exercises real timers but not the Pysolate semantic/Guest runtime.", Runs: accepted, Summary: summarize(accepted)}
	return writeJSON(filepath.Join(output, "release-readiness-live-sample.json"), result, 0o600)
}

func record(ctx context.Context, client *http.Client, key, model string) (agenttrajectory.RawCandidateStream, error) {
	payload := apiRequest{Model: model, Messages: []agenttrajectory.ModelMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}, MaxTokens: 12000, Stream: true, StreamOptions: map[string]bool{"include_usage": true}, ResponseFormat: map[string]string{"type": "json_object"}, Thinking: map[string]string{"type": "disabled"}}
	rawRequest, _ := json.Marshal(payload)
	start := time.Now()
	elapsed := func() uint64 { return uint64(time.Since(start)) }
	network := agenttrajectory.ObservedNetworkTrace{}
	trace := &httptrace.ClientTrace{DNSStart: func(httptrace.DNSStartInfo) { network.DNSStartNS = elapsed() }, DNSDone: func(httptrace.DNSDoneInfo) { network.DNSDoneNS = elapsed() }, ConnectStart: func(_, _ string) { network.ConnectStartNS = elapsed() }, ConnectDone: func(_, _ string, _ error) { network.ConnectDoneNS = elapsed() }, TLSHandshakeStart: func() { network.TLSStartNS = elapsed() }, TLSHandshakeDone: func(_ tls.ConnectionState, _ error) { network.TLSDoneNS = elapsed() }, GotConn: func(info httptrace.GotConnInfo) {
		network.GotConnNS = elapsed()
		network.ConnectionReused = info.Reused
	}, WroteRequest: func(httptrace.WroteRequestInfo) { network.WroteRequestNS = elapsed() }, GotFirstResponseByte: func() { network.FirstResponseByteNS = elapsed() }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.deepseek.com/chat/completions", bytes.NewReader(rawRequest))
	if err != nil {
		return agenttrajectory.RawCandidateStream{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := client.Do(req)
	if err != nil {
		return agenttrajectory.RawCandidateStream{}, err
	}
	defer resp.Body.Close()
	headers := elapsed()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return agenttrajectory.RawCandidateStream{}, fmt.Errorf("provider status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	raw := agenttrajectory.RawCandidateStream{SchemaVersion: agenttrajectory.RawCandidateStreamSchemaVersion, CandidateID: "release-readiness", Model: model, StartedAt: start.UTC().Format(time.RFC3339Nano), HeadersElapsedNS: headers, RawRequest: rawRequest, Network: network, Chunks: []agenttrajectory.StreamChunk{}}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	var content strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			raw.DoneElapsedNS = elapsed()
			break
		}
		var envelope apiChunk
		if json.Unmarshal([]byte(data), &envelope) != nil {
			return raw, errors.New("invalid SSE envelope")
		}
		if raw.ResponseID == "" && envelope.ID != "" {
			raw.ResponseID = envelope.ID
		}
		if envelope.Model != "" {
			raw.Model = envelope.Model
		}
		chunk := agenttrajectory.StreamChunk{ElapsedNS: elapsed(), ProviderPayload: append([]byte(nil), data...)}
		if len(envelope.Choices) > 0 {
			chunk.Content = envelope.Choices[0].Delta.Content
			chunk.Reasoning = envelope.Choices[0].Delta.Reasoning
			chunk.FinishReason = envelope.Choices[0].FinishReason
			content.WriteString(chunk.Content)
		}
		if envelope.Usage.TotalTokens > 0 {
			raw.Usage = envelope.Usage
		}
		raw.Chunks = append(raw.Chunks, chunk)
	}
	if err := scanner.Err(); err != nil {
		return raw, err
	}
	if raw.DoneElapsedNS == 0 {
		raw.DoneElapsedNS = elapsed()
	}
	raw.Content = content.String()
	if raw.ResponseID == "" || raw.Content == "" {
		return raw, errors.New("incomplete provider stream")
	}
	return raw, nil
}

func summarize(runs []releasereadiness.Evidence) summary {
	var firstByte, reasoning, content, source, window, lines, seq, parallel []uint64
	drifts := map[string][]int64{}
	for _, run := range runs {
		firstByte = append(firstByte, run.Network.FirstResponseByteNS)
		reasoning = append(reasoning, run.FirstReasoningNS)
		content = append(content, run.FirstContentNS)
		source = append(source, run.SourceCompleteNS)
		window = append(window, run.EligibleWindowNS)
		lines = append(lines, uint64(len(run.Statements)))
		seq = append(seq, run.Projection.SavingsVsSequentialNS)
		parallel = append(parallel, run.Projection.SavingsVsParallelNS)
		for _, replay := range run.Replays {
			drifts[replay.Lane] = append(drifts[replay.Lane], replay.DriftNS)
		}
	}
	return summary{MedianFirstResponseByteNS: median(firstByte), MedianFirstReasoningNS: median(reasoning), MedianFirstContentNS: median(content), MedianSourceCompleteNS: median(source), MedianEligibleWindowNS: median(window), MedianSourceLineCount: median(lines), MedianSavingsVsSequentialNS: median(seq), MedianSavingsVsParallelNS: median(parallel), MinSavingsVsParallelNS: min(parallel), MaxSavingsVsParallelNS: max(parallel), MedianSequentialReplayDriftNS: medianSigned(drifts["post_source_sequential"]), MedianParallelReplayDriftNS: medianSigned(drifts["post_source_parallel"]), MedianPrefixReplayDriftNS: medianSigned(drifts["prefix_pre_dispatch"])}
}
func median(v []uint64) uint64 {
	c := append([]uint64(nil), v...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}
func medianSigned(v []int64) int64 {
	c := append([]int64(nil), v...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}
func min(v []uint64) uint64 {
	x := v[0]
	for _, n := range v[1:] {
		if n < x {
			x = n
		}
	}
	return x
}
func max(v []uint64) uint64 {
	x := v[0]
	for _, n := range v[1:] {
		if n > x {
			x = n
		}
	}
	return x
}
func seconds(ns uint64) float64 { return float64(ns) / 1e9 }
func writeJSON(path string, value any, mode os.FileMode) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(body, '\n')); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func readRaw(path string) (agenttrajectory.RawCandidateStream, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return agenttrajectory.RawCandidateStream{}, err
	}
	var raw agenttrajectory.RawCandidateStream
	if json.Unmarshal(body, &raw) != nil {
		return raw, errors.New("invalid private stream")
	}
	return raw, nil
}

func validatePythonSyntax(content string) error {
	var response releasereadiness.ProgramResponse
	if json.Unmarshal([]byte(content), &response) != nil {
		return errors.New("invalid response JSON")
	}
	command := exec.Command("python3", "-c", "import ast,sys; ast.parse(sys.stdin.read())")
	command.Stdin = strings.NewReader(response.PythonSource)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	return nil
}
