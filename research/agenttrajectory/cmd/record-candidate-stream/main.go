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
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

type requestEnvelope struct {
	Model          string                         `json:"model"`
	Messages       []agenttrajectory.ModelMessage `json:"messages"`
	ResponseFormat map[string]string              `json:"response_format"`
	Temperature    int                            `json:"temperature"`
	Stream         bool                           `json:"stream"`
	StreamOptions  map[string]bool                `json:"stream_options"`
}

type streamEnvelope struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage agenttrajectory.TokenUsage `json:"usage"`
}

type publicCampaign struct {
	SchemaVersion string                                   `json:"schema_version"`
	ObservedAt    string                                   `json:"observed_at"`
	Platform      string                                   `json:"platform"`
	Endpoint      string                                   `json:"endpoint"`
	FixtureSHA256 string                                   `json:"fixture_sha256"`
	Method        string                                   `json:"method"`
	Candidates    []agenttrajectory.ObservedStreamEvidence `json:"candidates"`
	Aggregate     aggregateOpportunity                     `json:"simultaneous_replay_opportunity"`
}

type aggregateOpportunity struct {
	NativeSequentialReadyNS  uint64 `json:"native_sequential_ready_ns"`
	NativeParallelReadyNS    uint64 `json:"native_parallel_ready_ns"`
	PrefixPreDispatchReadyNS uint64 `json:"prefix_pre_dispatch_ready_ns"`
	SavingsVsSequentialNS    uint64 `json:"savings_vs_sequential_ns"`
	SavingsVsParallelNS      uint64 `json:"savings_vs_parallel_ns"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var output, fixtureRoot, model, replayPrivate string
	flag.StringVar(&output, "output", "", "new output directory")
	flag.StringVar(&fixtureRoot, "fixture", "research/agenttrajectory/testdata/day-trip-planning", "public fixture")
	flag.StringVar(&model, "model", "deepseek-v4-flash", "pinned provider model")
	flag.StringVar(&replayPrivate, "replay-private", "", "existing private raw stream directory; makes no provider calls")
	flag.Parse()
	key := os.Getenv("DEEPSEEK_API_KEY")
	if output == "" || (replayPrivate == "" && key == "") || (model != "deepseek-v4-flash" && model != "deepseek-v4-pro") {
		return errors.New("output, live DEEPSEEK_API_KEY or replay-private, and supported pinned model are required")
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		return err
	}
	fixture, err := agenttrajectory.LoadFixture(fixtureRoot)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 90 * time.Second}
	observed := make([]agenttrajectory.ObservedStreamEvidence, 0, 2)
	for _, candidateID := range []string{agenttrajectory.CandidateBrighton, agenttrajectory.CandidateOxford} {
		request, requestErr := agenttrajectory.NewCandidateModelRequest(fixture, candidateID)
		if requestErr != nil {
			return requestErr
		}
		var raw agenttrajectory.RawCandidateStream
		var streamErr error
		if replayPrivate == "" {
			raw, streamErr = recordStream(context.Background(), client, key, model, request)
		} else {
			raw, streamErr = readRaw(filepath.Join(replayPrivate, candidateID+"-private-stream.json"))
		}
		if streamErr != nil {
			return fmt.Errorf("record %s: %w", candidateID, streamErr)
		}
		if replayPrivate == "" {
			if err := writeJSON(filepath.Join(output, candidateID+"-private-stream.json"), raw, 0o600); err != nil {
				return err
			}
		}
		latencies := map[string]time.Duration{
			"weather":     time.Duration(fixture.Workspace.API.APILatencyMS["weather"]) * time.Millisecond,
			"rail":        time.Duration(fixture.Workspace.API.APILatencyMS["rail"]) * time.Millisecond,
			"attractions": time.Duration(fixture.Workspace.API.APILatencyMS["attractions"]) * time.Millisecond,
		}
		projection, projectionErr := agenttrajectory.ProjectObservedCandidateStream(raw, latencies)
		if projectionErr != nil {
			return fmt.Errorf("project %s: %w", candidateID, projectionErr)
		}
		observed = append(observed, projection)
		fmt.Printf("%s first_content=%.3fs source=%.3fs window=%.3fs saving_vs_parallel=%.3fs reused=%t\n", candidateID,
			float64(projection.FirstContentNS)/1e9, float64(projection.SourceCompleteNS)/1e9, float64(projection.EligibleWindowNS)/1e9,
			float64(projection.SavingsVsParallelNS)/1e9, projection.Network.ConnectionReused)
	}
	aggregate := aggregateObserved(observed)
	observedAt := time.Now().UTC().Format(time.RFC3339)
	if replayPrivate != "" {
		observedAt = earliestObservation(replayPrivate)
	}
	campaign := publicCampaign{SchemaVersion: "pysolate.observed-stream-campaign.v1", ObservedAt: observedAt, Platform: runtime.GOOS + "/" + runtime.GOARCH, Endpoint: "api.deepseek.com", FixtureSHA256: fixture.AggregateSHA256,
		Method: "Two live provider streams were recorded sequentially with one reused HTTP client. Lane readiness is a simultaneous timestamp replay using deterministic fixture tool latencies; it excludes runtime mechanism overhead.", Candidates: observed, Aggregate: aggregate}
	return writeJSON(filepath.Join(output, "observed-stream-opportunity.json"), campaign, 0o644)
}

func aggregateObserved(candidates []agenttrajectory.ObservedStreamEvidence) aggregateOpportunity {
	var result aggregateOpportunity
	for _, candidate := range candidates {
		if candidate.NativeSequentialReadyNS > result.NativeSequentialReadyNS {
			result.NativeSequentialReadyNS = candidate.NativeSequentialReadyNS
		}
		if candidate.NativeParallelReadyNS > result.NativeParallelReadyNS {
			result.NativeParallelReadyNS = candidate.NativeParallelReadyNS
		}
		if candidate.PrefixPreDispatchReadyNS > result.PrefixPreDispatchReadyNS {
			result.PrefixPreDispatchReadyNS = candidate.PrefixPreDispatchReadyNS
		}
	}
	result.SavingsVsSequentialNS = result.NativeSequentialReadyNS - result.PrefixPreDispatchReadyNS
	result.SavingsVsParallelNS = result.NativeParallelReadyNS - result.PrefixPreDispatchReadyNS
	return result
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

func earliestObservation(root string) string {
	for _, candidate := range []string{agenttrajectory.CandidateBrighton, agenttrajectory.CandidateOxford} {
		raw, err := readRaw(filepath.Join(root, candidate+"-private-stream.json"))
		if err == nil && raw.StartedAt != "" {
			return raw.StartedAt
		}
	}
	return ""
}

func recordStream(ctx context.Context, client *http.Client, key, model string, request agenttrajectory.ModelRequest) (agenttrajectory.RawCandidateStream, error) {
	payload := requestEnvelope{Model: model, Messages: request.Messages, ResponseFormat: map[string]string{"type": "json_object"}, Temperature: 0, Stream: true, StreamOptions: map[string]bool{"include_usage": true}}
	rawRequest, err := json.Marshal(payload)
	if err != nil {
		return agenttrajectory.RawCandidateStream{}, err
	}
	started := time.Now()
	elapsed := func() uint64 { return uint64(time.Since(started)) }
	network := agenttrajectory.ObservedNetworkTrace{}
	trace := &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { network.DNSStartNS = elapsed() },
		DNSDone:           func(httptrace.DNSDoneInfo) { network.DNSDoneNS = elapsed() },
		ConnectStart:      func(_, _ string) { network.ConnectStartNS = elapsed() },
		ConnectDone:       func(_, _ string, _ error) { network.ConnectDoneNS = elapsed() },
		TLSHandshakeStart: func() { network.TLSStartNS = elapsed() },
		TLSHandshakeDone:  func(_ tls.ConnectionState, _ error) { network.TLSDoneNS = elapsed() },
		GotConn: func(info httptrace.GotConnInfo) {
			network.GotConnNS = elapsed()
			network.ConnectionReused = info.Reused
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { network.WroteRequestNS = elapsed() },
		GotFirstResponseByte: func() { network.FirstResponseByteNS = elapsed() },
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.deepseek.com/chat/completions", bytes.NewReader(rawRequest))
	if err != nil {
		return agenttrajectory.RawCandidateStream{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+key)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest = httpRequest.WithContext(httptrace.WithClientTrace(httpRequest.Context(), trace))
	response, err := client.Do(httpRequest)
	if err != nil {
		return agenttrajectory.RawCandidateStream{}, err
	}
	defer response.Body.Close()
	headersElapsed := elapsed()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return agenttrajectory.RawCandidateStream{}, fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 1<<20))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	chunks := make([]agenttrajectory.StreamChunk, 0, 128)
	var content, responseID, responseModel strings.Builder
	var usage agenttrajectory.TokenUsage
	var doneNS uint64
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(data, []byte("[DONE]")) {
			doneNS = elapsed()
			break
		}
		var envelope streamEnvelope
		if json.Unmarshal(data, &envelope) != nil {
			return agenttrajectory.RawCandidateStream{}, errors.New("invalid provider SSE envelope")
		}
		if envelope.ID != "" {
			responseID.Reset()
			responseID.WriteString(envelope.ID)
		}
		if envelope.Model != "" {
			responseModel.Reset()
			responseModel.WriteString(envelope.Model)
		}
		chunk := agenttrajectory.StreamChunk{ElapsedNS: elapsed(), ProviderPayload: append([]byte(nil), data...)}
		if len(envelope.Choices) == 1 {
			chunk.Content = envelope.Choices[0].Delta.Content
			chunk.Reasoning = envelope.Choices[0].Delta.ReasoningContent
			chunk.FinishReason = envelope.Choices[0].FinishReason
			content.WriteString(chunk.Content)
		}
		if envelope.Usage.TotalTokens > 0 {
			usage = envelope.Usage
		}
		chunks = append(chunks, chunk)
	}
	if err := scanner.Err(); err != nil {
		return agenttrajectory.RawCandidateStream{}, err
	}
	if doneNS == 0 {
		doneNS = elapsed()
	}
	if responseID.Len() == 0 || responseModel.Len() == 0 || content.Len() == 0 || len(chunks) == 0 {
		return agenttrajectory.RawCandidateStream{}, errors.New("incomplete provider stream")
	}
	return agenttrajectory.RawCandidateStream{
		SchemaVersion: agenttrajectory.RawCandidateStreamSchemaVersion, CandidateID: request.ActorID, Model: responseModel.String(), ResponseID: responseID.String(),
		StartedAt: started.UTC().Format(time.RFC3339Nano), HeadersElapsedNS: headersElapsed, DoneElapsedNS: doneNS, Chunks: chunks,
		Content: content.String(), Usage: usage, RawRequest: rawRequest, Network: network,
	}, nil
}

func writeJSON(path string, value any, mode os.FileMode) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(body, '\n')); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
