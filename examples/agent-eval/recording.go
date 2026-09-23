package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
)

const (
	privateRecordingVersion = "agent-eval-private/v1"
	privateRecordingWarning = "PRIVATE DEVELOPMENT RECORDING: contains provider bodies, prompts, generated code, tool arguments and results. Keep local."
	privateRecordMaxLine    = 16 << 20
	privateRecordMaxFile    = 256 << 20
)

type privateRecordingHeader struct {
	Kind              string           `json:"kind"`
	Version           string           `json:"version"`
	PrivacyWarning    string           `json:"privacy_warning"`
	FixtureVersion    string           `json:"fixture_version"`
	GuestSHA256       string           `json:"guest_sha256"`
	RequestedModel    string           `json:"requested_model"`
	Endpoint          string           `json:"endpoint,omitempty"`
	Runtime           string           `json:"runtime,omitempty"`
	Limits            map[string]int64 `json:"limits"`
	MaxTurns          int              `json:"max_turns"`
	MaxRequests       int              `json:"max_requests"`
	RecordingOverhead string           `json:"recording_overhead"`
	Network           string           `json:"network"`
}

// privateEpisodeRecording is intentionally separate from episodeRow. The row
// is the shareable score summary; this object retains the complete continuation
// and transport evidence for local development and offline re-execution.
type privateEpisodeRecording struct {
	Kind             string                    `json:"kind"`
	FixtureVersion   string                    `json:"fixture_version"`
	Task             string                    `json:"task"`
	Arm              string                    `json:"arm"`
	Repeat           int                       `json:"repeat"`
	Sequence         int                       `json:"sequence"`
	Seed             string                    `json:"seed"`
	MaxTurns         int                       `json:"max_turns"`
	ReserveRequests  int                       `json:"reserve_requests"`
	MaxTokens        int                       `json:"max_tokens"`
	Partial          bool                      `json:"partial,omitempty"`
	PartialReason    string                    `json:"partial_reason,omitempty"`
	RedactionApplied bool                      `json:"redaction_applied"`
	Row              episodeRow                `json:"row"`
	ProviderCalls    []privateProviderExchange `json:"provider_calls"`
	ModelToolCalls   []privateModelToolCall    `json:"model_tool_calls"`
	DomainBindings   []privateToolBinding      `json:"domain_bindings"`
	DomainCalls      []privateDomainCall       `json:"domain_calls"`
	Executions       []privateExecution        `json:"executions"`
	writer           *privateRecordingWriter
}

type privateToolBinding struct {
	Name        string          `json:"name"`
	PythonPath  string          `json:"python_path"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type privateProviderExchange struct {
	Completed         bool             `json:"completed"`
	Transport         string           `json:"transport"`
	RequestBody       []byte           `json:"request_body,omitempty"`
	ResponseBody      []byte           `json:"response_body,omitempty"`
	ErrorBody         []byte           `json:"error_body,omitempty"`
	StatusCode        int              `json:"status_code,omitempty"`
	ResponseTruncated bool             `json:"response_truncated,omitempty"`
	Messages          []Message        `json:"messages"`
	Tools             []ToolDefinition `json:"tools"`
	MaxTokens         int              `json:"max_tokens"`
	Response          ProviderResponse `json:"response"`
	Error             string           `json:"error,omitempty"`
	ErrorType         string           `json:"error_type,omitempty"`
	DiagnosticError   string           `json:"diagnostic_error,omitempty"`
}

type privateModelToolCall struct {
	Turn      int    `json:"turn"`
	CallID    string `json:"call_id"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Result    string `json:"result,omitempty"`
}

type privateDomainCall struct {
	Completed bool   `json:"completed"`
	Scope     string `json:"scope"` // direct or python
	Attempt   int    `json:"attempt,omitempty"`
	Sequence  int    `json:"sequence"`
	Tool      string `json:"tool"`
	Arguments []byte `json:"arguments"`
	Result    []byte `json:"result"`
}

type privateExecution struct {
	Completed   bool                `json:"completed"`
	Attempt     int                 `json:"attempt"`
	Source      string              `json:"source"`
	Inputs      []byte              `json:"inputs"`
	Value       []byte              `json:"value,omitempty"`
	Stdout      []byte              `json:"stdout,omitempty"`
	Stderr      []byte              `json:"stderr,omitempty"`
	Transformed string              `json:"transformed,omitempty"`
	Error       string              `json:"error,omitempty"`
	ToolCalls   []privateDomainCall `json:"tool_calls"`
}

// recordingProvider records the provider boundary before runEpisode strips
// reasoning content from public traces. OpenAI bodies come from the transport
// implementation and remain lossless []byte values (base64 in JSONL).
type recordingProvider struct {
	inner    ChatProvider
	episode  *privateEpisodeRecording
	fatalErr error
}

func (p *recordingProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition, maxTokens int) (ProviderResponse, error) {
	if p == nil || p.episode == nil {
		return ProviderResponse{}, errors.New("private recording episode is missing")
	}
	if p.fatalErr != nil {
		return ProviderResponse{}, p.fatalErr
	}
	model := ""
	if detailed, ok := p.inner.(*OpenAIProvider); ok && detailed != nil {
		model = detailed.Model
	}
	rawRequest, err := encodeProviderRequestBody(model, messages, tools, maxTokens)
	if err != nil {
		return ProviderResponse{}, p.recordingFailure(fmt.Errorf("private recording request encoding: %w", err))
	}
	exchange := privateProviderExchange{
		RequestBody: append([]byte(nil), rawRequest...),
		Messages:    cloneMessages(messages), Tools: cloneTools(tools), MaxTokens: maxTokens,
	}
	if _, ok := p.inner.(*OpenAIProvider); ok {
		exchange.Transport = "http"
	} else {
		exchange.Transport = "test"
	}
	p.episode.ProviderCalls = append(p.episode.ProviderCalls, exchange)
	callIndex := len(p.episode.ProviderCalls) - 1
	if err := p.episode.checkpoint(); err != nil {
		return ProviderResponse{}, p.recordingFailure(err)
	}

	var response ProviderResponse
	var completeErr error
	if detailed, ok := p.inner.(*OpenAIProvider); ok {
		var details providerCallDetails
		response, details, completeErr = detailed.completeWithDetails(ctx, messages, tools, maxTokens)
		if len(details.RequestBody) != 0 {
			p.episode.ProviderCalls[callIndex].RequestBody = append([]byte(nil), details.RequestBody...)
		}
		p.episode.ProviderCalls[callIndex].ResponseBody = append([]byte(nil), details.ResponseBody...)
		p.episode.ProviderCalls[callIndex].StatusCode = details.StatusCode
		p.episode.ProviderCalls[callIndex].DiagnosticError = details.DiagnosticError
		p.episode.ProviderCalls[callIndex].ResponseTruncated = details.ResponseTruncated
	} else {
		response, completeErr = p.inner.Complete(ctx, messages, tools, maxTokens)
	}
	call := &p.episode.ProviderCalls[callIndex]
	call.Response = cloneProviderResponse(response)
	if len(call.ResponseBody) == 0 && len(response.RawBody) != 0 {
		call.ResponseBody = append([]byte(nil), response.RawBody...)
	}
	if completeErr != nil {
		call.Error = completeErr.Error()
		call.ErrorType = fmt.Sprintf("%T", completeErr)
		call.ErrorBody = append([]byte(nil), call.ResponseBody...)
	}
	call.Completed = true
	if checkpointErr := p.episode.checkpoint(); checkpointErr != nil {
		return response, p.recordingFailure(errors.Join(completeErr, checkpointErr))
	}
	return response, completeErr
}

func (p *recordingProvider) recordingFailure(err error) error {
	if err == nil {
		return nil
	}
	if p.fatalErr == nil {
		p.fatalErr = err
	}
	return p.fatalErr
}

func (p *recordingProvider) recordingError() error {
	if p == nil {
		return errors.New("private recording provider is missing")
	}
	return p.fatalErr
}

// playbackProvider has no network-capable delegate. It validates the exact
// logical request and returns the previously recorded parsed response/error.
type playbackProvider struct {
	episode *privateEpisodeRecording
	index   int
	model   string
	failure error
}

func (p *playbackProvider) recordingEpisode() *privateEpisodeRecording  { return p.episode }
func (p *recordingProvider) recordingEpisode() *privateEpisodeRecording { return p.episode }

func (p *playbackProvider) Complete(_ context.Context, messages []Message, tools []ToolDefinition, maxTokens int) (ProviderResponse, error) {
	if p.failure != nil {
		return ProviderResponse{}, p.failure
	}
	if p.episode == nil {
		return ProviderResponse{}, p.playbackFailure(errors.New("offline provider replay episode is missing"))
	}
	if p.index >= len(p.episode.ProviderCalls) {
		return ProviderResponse{}, p.playbackFailure(errors.New("offline provider replay exhausted"))
	}
	call := p.episode.ProviderCalls[p.index]
	p.index++
	if !reflect.DeepEqual(call.Messages, messages) || !reflect.DeepEqual(call.Tools, tools) || call.MaxTokens != maxTokens {
		return ProviderResponse{}, p.playbackFailure(errors.New("offline provider replay request mismatch"))
	}
	if err := validateProviderExchange(call, p.model); err != nil {
		return ProviderResponse{}, p.playbackFailure(err)
	}
	response := cloneProviderResponse(call.Response)
	response.RawBody = append([]byte(nil), call.ResponseBody...)
	if call.Error != "" {
		return response, &ProviderError{Message: call.Error, Model: response.Model, Usage: cloneUsage(response.Usage)}
	}
	return response, nil
}

func (p *playbackProvider) playbackFailure(err error) error {
	if p.failure == nil {
		p.failure = err
	}
	return p.failure
}

func recordingEpisodeOf(provider ChatProvider) *privateEpisodeRecording {
	if carrier, ok := provider.(interface {
		recordingEpisode() *privateEpisodeRecording
	}); ok {
		return carrier.recordingEpisode()
	}
	return nil
}

func cloneMessages(in []Message) []Message {
	out := make([]Message, len(in))
	for i, msg := range in {
		out[i] = msg
		out[i].ToolCalls = append([]ToolCall(nil), msg.ToolCalls...)
	}
	return out
}
func cloneTools(in []ToolDefinition) []ToolDefinition {
	out := make([]ToolDefinition, len(in))
	for i, tool := range in {
		out[i] = tool
		out[i].Function.Parameters = append(json.RawMessage(nil), tool.Function.Parameters...)
	}
	return out
}
func cloneUsage(in *ProviderUsage) *ProviderUsage {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}
func cloneProviderResponse(in ProviderResponse) ProviderResponse {
	in.RawBody = append([]byte(nil), in.RawBody...)
	in.Message.ToolCalls = append([]ToolCall(nil), in.Message.ToolCalls...)
	in.Usage = cloneUsage(in.Usage)
	return in
}

func appendModelToolCall(ep *privateEpisodeRecording, turn int, call ToolCall, result string) {
	if ep == nil {
		return
	}
	ep.ModelToolCalls = append(ep.ModelToolCalls, privateModelToolCall{Turn: turn, CallID: call.ID, Tool: call.Function.Name, Arguments: call.Function.Arguments, Result: result})
}

// recordingToolJournal records the exact wire result delivered by the Pysolate
// bridge, not a re-marshaled domain value.
type recordingToolJournal struct {
	ep        *privateEpisodeRecording
	attempt   int
	calls     []privateDomainCall
	execution *privateExecution
}

func (j *recordingToolJournal) Call(ctx context.Context, tool string, args json.RawMessage, next func(context.Context) []byte) ([]byte, error) {
	if j == nil || j.ep == nil {
		return nil, errors.New("private recording journal is missing")
	}
	if j.execution != nil {
		j.execution.ToolCalls = append(j.execution.ToolCalls, privateDomainCall{Scope: "python", Attempt: j.attempt, Sequence: len(j.calls), Tool: tool, Arguments: append([]byte(nil), args...)})
		j.calls = j.execution.ToolCalls
	} else {
		j.calls = append(j.calls, privateDomainCall{Scope: "python", Attempt: j.attempt, Sequence: len(j.calls), Tool: tool, Arguments: append([]byte(nil), args...)})
	}
	if err := j.ep.checkpoint(); err != nil {
		return nil, err
	}
	result := next(ctx)
	call := &j.calls[len(j.calls)-1]
	call.Result = append([]byte(nil), result...)
	call.Completed = true
	if j.execution != nil {
		j.execution.ToolCalls = j.calls
	}
	if err := j.ep.checkpoint(); err != nil {
		return result, err
	}
	return result, nil
}

func (j *recordingToolJournal) attachExecution(execution *privateExecution) {
	if j == nil {
		return
	}
	j.execution = execution
	if execution != nil {
		execution.ToolCalls = append(execution.ToolCalls[:0], j.calls...)
		j.calls = execution.ToolCalls
	}
}

type playbackToolJournal struct {
	runtime *episodeRuntime
	calls   []privateDomainCall
	index   int
}

func (j *playbackToolJournal) Call(_ context.Context, tool string, args json.RawMessage, _ func(context.Context) []byte) ([]byte, error) {
	if j == nil || j.runtime == nil {
		return nil, errors.New("offline Python tool replay journal is missing")
	}
	if j.index >= len(j.calls) {
		return nil, j.replayFailure(errors.New("offline Python tool replay exhausted"))
	}
	call := j.calls[j.index]
	if !call.Completed || call.Tool != tool || !bytes.Equal(call.Arguments, args) {
		return nil, j.replayFailure(errors.New("offline Python tool replay mismatch"))
	}
	j.index++
	if !j.runtime.noteRecordedDomain() {
		if j.runtime.playback {
			return append([]byte(nil), call.Result...), nil
		}
		return nil, errors.New("Host-call budget exhausted")
	}
	return append([]byte(nil), call.Result...), nil
}

func (j *playbackToolJournal) replayFailure(err error) error {
	if j != nil && j.runtime != nil && j.runtime.replayErr == nil {
		j.runtime.replayErr = err
	}
	return err
}

func (e *episodeRuntime) noteRecordedDomain() bool {
	if e.hostCalls >= maxHostCalls {
		e.toolLimit = true
		return false
	}
	e.hostCalls++
	e.domainCalls++
	return true
}

// privateRecordingWriter appends metadata and complete episodes as JSONL. The
// JSON encoder's handling of []byte is intentional: it is base64, so whitespace,
// invalid JSON, HTML escaping, and provider error bodies survive exactly.
type privateRecordingWriter struct {
	mu       sync.Mutex
	file     *os.File
	enc      *json.Encoder
	secrets  []string
	finished bool
}

type privateRecordingFooter struct {
	Kind             string `json:"kind"`
	ExpectedEpisodes int    `json:"expected_episodes"`
	EpisodeCount     int    `json:"episode_count"`
}

type recordingSink struct {
	file    *os.File
	written int64
}

func (s *recordingSink) Write(data []byte) (int, error) {
	if len(data) > privateRecordMaxLine || s.written+int64(len(data)) > privateRecordMaxFile {
		return 0, errors.New("private recording exceeds capture limit")
	}
	n, err := s.file.Write(data)
	s.written += int64(n)
	return n, err
}

func newPrivateRecordingWriter(path string, header privateRecordingHeader, knownSecrets ...string) (*privateRecordingWriter, error) {
	if path == "" {
		return nil, errors.New("private recording path is empty")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	if header.Limits == nil {
		header.Limits = recordingLimits()
	}
	secrets := normalizeRecordingSecrets(knownSecrets)
	writer := &privateRecordingWriter{file: file, enc: json.NewEncoder(&recordingSink{file: file}), secrets: secrets}
	writer.enc.SetEscapeHTML(false)
	redactRecordingValue(reflect.ValueOf(&header).Elem(), secrets)
	if err := writer.enc.Encode(header); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	return writer, nil
}

func normalizeRecordingSecrets(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	secrets := make([]string, 0, len(in))
	for _, secret := range in {
		if secret == "" {
			continue
		}
		if _, ok := seen[secret]; ok {
			continue
		}
		seen[secret] = struct{}{}
		secrets = append(secrets, secret)
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

func (ep *privateEpisodeRecording) checkpoint() error {
	if ep == nil || ep.writer == nil {
		return nil
	}
	return ep.writer.AppendProgress(*ep)
}

func (w *privateRecordingWriter) encodeEpisode(ep privateEpisodeRecording) error {
	clone, err := cloneRecordingEpisode(ep)
	if err != nil {
		return err
	}
	redactRecordingValue(reflect.ValueOf(&clone).Elem(), w.secrets)
	clone.RedactionApplied = true
	clone.writer = nil
	return w.enc.Encode(clone)
}

func (w *privateRecordingWriter) safeRow(row episodeRow) (episodeRow, error) {
	clone, err := cloneRecordingEpisode(privateEpisodeRecording{Row: row})
	if err != nil {
		return episodeRow{}, err
	}
	redactRecordingValue(reflect.ValueOf(&clone).Elem(), w.secrets)
	return clone.Row, nil
}

func (w *privateRecordingWriter) Append(ep privateEpisodeRecording) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil || w.finished {
		return errors.New("private recording writer is closed")
	}
	if err := w.encodeEpisode(ep); err != nil {
		return err
	}
	return w.file.Sync()
}

func (w *privateRecordingWriter) AppendProgress(ep privateEpisodeRecording) error {
	ep.Kind = "episode_progress"
	ep.Partial = true
	if ep.PartialReason == "" {
		ep.PartialReason = "in-progress snapshot"
	}
	return w.Append(ep)
}

func (w *privateRecordingWriter) Finish(expectedEpisodes int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil || w.finished {
		return errors.New("private recording writer is closed")
	}
	if expectedEpisodes < 0 {
		return errors.New("private recording expected episode count is negative")
	}
	footer := privateRecordingFooter{Kind: "footer", ExpectedEpisodes: expectedEpisodes, EpisodeCount: expectedEpisodes}
	if err := w.enc.Encode(footer); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	w.finished = true
	return nil
}

func (w *privateRecordingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func cloneRecordingEpisode(ep privateEpisodeRecording) (privateEpisodeRecording, error) {
	data, err := json.Marshal(ep)
	if err != nil {
		return privateEpisodeRecording{}, err
	}
	var clone privateEpisodeRecording
	if err := json.Unmarshal(data, &clone); err != nil {
		return privateEpisodeRecording{}, err
	}
	return clone, nil
}

func redactRecordingValue(value reflect.Value, secrets []string) {
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Pointer {
		if !value.IsNil() {
			redactRecordingValue(value.Elem(), secrets)
		}
		return
	}
	if value.Kind() == reflect.String && value.CanSet() {
		text := value.String()
		for _, secret := range secrets {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
		value.SetString(text)
		return
	}
	if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 && value.CanSet() {
		data := append([]byte(nil), value.Bytes()...)
		for _, secret := range secrets {
			data = bytes.ReplaceAll(data, []byte(secret), []byte("[REDACTED]"))
		}
		value.SetBytes(data)
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Field(i).CanInterface() {
				redactRecordingValue(value.Field(i), secrets)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			redactRecordingValue(value.Index(i), secrets)
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			item := value.MapIndex(key)
			if item.CanSet() {
				redactRecordingValue(item, secrets)
			}
		}
	}
}

type privateRecording struct {
	Header   privateRecordingHeader
	Episodes []privateEpisodeRecording
}

func readPrivateRecording(path string) (privateRecording, error) {
	var result privateRecording
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return result, err
	}
	if !linkInfo.Mode().IsRegular() {
		return result, errors.New("private recording input is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("private recording input is not a regular file")
	}
	if info.Size() > privateRecordMaxFile {
		return result, errors.New("private recording exceeds size limit")
	}
	scanner := bufio.NewScanner(io.LimitReader(file, int64(privateRecordMaxLine)*1024))
	scanner.Buffer(make([]byte, 64<<10), privateRecordMaxLine)
	line := 0
	headerSeen := false
	footerSeen := false
	finalKeys := make(map[string]struct{})
	progressKeys := make(map[string]struct{})
	progressIndex := make(map[string]int)
	var footer privateRecordingFooter
	for scanner.Scan() {
		line++
		if footerSeen {
			return result, fmt.Errorf("private recording line %d follows completion footer", line)
		}
		var kind struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &kind); err != nil {
			return result, fmt.Errorf("private recording line %d: %w", line, err)
		}
		if line == 1 {
			if kind.Kind != "header" {
				return result, errors.New("private recording header is missing")
			}
			if err := json.Unmarshal(scanner.Bytes(), &result.Header); err != nil {
				return result, err
			}
			if result.Header.Version != privateRecordingVersion || result.Header.PrivacyWarning != privateRecordingWarning {
				return result, errors.New("unsupported private recording")
			}
			headerSeen = true
			continue
		}
		switch kind.Kind {
		case "episode_progress", "episode_start":
			var episode privateEpisodeRecording
			if err := json.Unmarshal(scanner.Bytes(), &episode); err != nil {
				return result, fmt.Errorf("private recording line %d: %w", line, err)
			}
			if err := validateEpisodeIdentity(episode); err != nil {
				return result, fmt.Errorf("private recording line %d: %w", line, err)
			}
			if kind.Kind == "episode_start" {
				episode.Partial = true
			}
			if !episode.Partial {
				return result, fmt.Errorf("private recording line %d progress is not partial", line)
			}
			key := episodeKey(episode)
			if _, ok := finalKeys[key]; ok {
				return result, fmt.Errorf("private recording line %d has progress after final episode", line)
			}
			progressKeys[key] = struct{}{}
			if index, ok := progressIndex[key]; ok {
				result.Episodes[index] = episode
			} else {
				progressIndex[key] = len(result.Episodes)
				result.Episodes = append(result.Episodes, episode)
			}
		case "episode":
			var episode privateEpisodeRecording
			if err := json.Unmarshal(scanner.Bytes(), &episode); err != nil {
				return result, fmt.Errorf("private recording line %d: %w", line, err)
			}
			if err := validateEpisodeIdentity(episode); err != nil {
				return result, fmt.Errorf("private recording line %d: %w", line, err)
			}
			if episode.Partial {
				return result, fmt.Errorf("private recording line %d final episode is partial", line)
			}
			if err := validateCompleteEpisode(episode); err != nil {
				return result, fmt.Errorf("private recording line %d: %w", line, err)
			}
			key := episodeKey(episode)
			if _, ok := finalKeys[key]; ok {
				return result, fmt.Errorf("private recording line %d duplicates final episode", line)
			}
			finalKeys[key] = struct{}{}
			if index, ok := progressIndex[key]; ok {
				result.Episodes[index] = episode
			} else {
				result.Episodes = append(result.Episodes, episode)
			}
		case "footer":
			if err := json.Unmarshal(scanner.Bytes(), &footer); err != nil {
				return result, fmt.Errorf("private recording line %d: %w", line, err)
			}
			if footer.ExpectedEpisodes < 0 || footer.EpisodeCount != footer.ExpectedEpisodes {
				return result, fmt.Errorf("private recording line %d has invalid completion footer", line)
			}
			if footer.EpisodeCount != len(finalKeys) {
				return result, fmt.Errorf("private recording completion footer count=%d, final episodes=%d", footer.EpisodeCount, len(finalKeys))
			}
			for key := range progressKeys {
				if _, ok := finalKeys[key]; !ok {
					return result, errors.New("private recording has an unresolved partial snapshot")
				}
			}
			footerSeen = true
		default:
			return result, fmt.Errorf("private recording line %d has unknown record kind", line)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if line == 0 {
		return result, errors.New("private recording is empty")
	}
	if !headerSeen {
		return result, errors.New("private recording header is missing")
	}
	if !footerSeen {
		return result, errors.New("private recording completion footer is missing")
	}
	return result, nil
}

func validateEpisodeIdentity(episode privateEpisodeRecording) error {
	if episode.Task == "" || episode.Arm == "" || episode.Seed == "" {
		return errors.New("private recording episode is incomplete")
	}
	return nil
}

func validateCompleteEpisode(episode privateEpisodeRecording) error {
	for _, call := range episode.ProviderCalls {
		if call.Transport != "http" && call.Transport != "test" {
			return errors.New("private recording has unknown provider transport")
		}
		if !call.Completed {
			return errors.New("private recording has incomplete provider exchange")
		}
	}
	for _, call := range episode.DomainCalls {
		if !call.Completed {
			return errors.New("private recording has incomplete domain call")
		}
	}
	for _, execution := range episode.Executions {
		if !execution.Completed {
			return errors.New("private recording has incomplete execution")
		}
		for _, call := range execution.ToolCalls {
			if !call.Completed {
				return errors.New("private recording has incomplete execution wire call")
			}
		}
	}
	return nil
}

func episodeKey(ep privateEpisodeRecording) string {
	return fmt.Sprintf("%d/%d/%s/%s", ep.Sequence, ep.Repeat, ep.Task, ep.Arm)
}

func defaultPrivateRecordingPath(out string) string {
	if out == "" {
		return ""
	}
	return strings.TrimSuffix(out, ".jsonl") + ".private.jsonl"
}
