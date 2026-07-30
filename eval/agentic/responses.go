package agentic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

const maxResponseTextBytes = 64 * 1024

var ErrProviderIdentityMismatch = errors.New("provider response identity mismatch")
var ErrProviderOutputLimitExceeded = errors.New("provider response exceeded requested output token limit")

type ResponseCall struct {
	CallID        string          `json:"-"`
	ProviderName  string          `json:"-"`
	CanonicalName string          `json:"-"`
	Arguments     json.RawMessage `json:"-"`
	OutputItemSeq uint32          `json:"-"`
}

type ParsedResponse struct {
	Calls       []ResponseCall `json:"-"`
	HasMessage  bool           `json:"-"`
	Refused     bool           `json:"-"`
	TextDigest  string         `json:"-"`
	replayItems []any
}

type ResponsesSession struct {
	mu           sync.Mutex
	adapter      provider.Adapter
	model        string
	limits       TrialLimits
	closed       bool
	calls        uint32
	usage        provider.Usage
	evidence     []ExchangeEvidence
	seenIDs      map[string]bool
	captureRaw   bool
	rawExchanges []RawProviderExchange
}

type RawProviderExchange struct {
	Request    json.RawMessage `json:"request"`
	Response   json.RawMessage `json:"response,omitempty"`
	StatusCode int             `json:"status_code"`
	RequestID  string          `json:"request_id,omitempty"`
	Usage      *provider.Usage `json:"usage,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func NewResponsesSession(adapter provider.Adapter, model string, limits TrialLimits) (*ResponsesSession, error) {
	return newResponsesSession(adapter, model, limits, false)
}

func newResponsesSession(adapter provider.Adapter, model string, limits TrialLimits, captureRaw bool) (*ResponsesSession, error) {
	if adapter == nil || adapter.Protocol() != provider.LinkAPIResponsesProtocol || model == "" || !limits.valid() {
		return nil, ErrAgenticRun
	}
	return &ResponsesSession{adapter: adapter, model: model, limits: limits, seenIDs: map[string]bool{}, captureRaw: captureRaw}, nil
}

func (session *ResponsesSession) Exchange(
	ctx context.Context,
	input any,
	tools []map[string]any,
	toolChoice string,
	parallelToolCalls bool,
	providerToCanonical map[string]string,
) (ParsedResponse, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return ParsedResponse{}, ErrBudgetClosed
	}
	if session.calls >= session.limits.MaxProviderCalls || session.usage.OutputTokens >= session.limits.MaxOutputTokens || session.usage.TotalTokens >= session.limits.MaxTotalTokens {
		session.closed = true
		return ParsedResponse{}, ErrBudgetExceeded
	}
	if toolChoice != "auto" && toolChoice != "required" {
		return ParsedResponse{}, ErrAgenticRun
	}
	if len(tools) > maxFunctionCalls {
		return ParsedResponse{}, ErrAgenticRun
	}
	remainingOutput := session.limits.MaxOutputTokens - session.usage.OutputTokens
	remainingTotal := session.limits.MaxTotalTokens - session.usage.TotalTokens
	perCallOutput := session.limits.MaxOutputTokensPerCall
	if remainingOutput < perCallOutput {
		perCallOutput = remainingOutput
	}
	if remainingTotal < perCallOutput {
		perCallOutput = remainingTotal
	}
	wireInput, instructions, err := promoteResponseInstructions(input)
	if err != nil {
		return ParsedResponse{}, err
	}
	payloadDocument := map[string]any{
		"model": session.model, "input": wireInput, "max_output_tokens": perCallOutput,
		"stream": false, "background": false,
	}
	if instructions != "" {
		payloadDocument["instructions"] = instructions
	}
	if len(tools) > 0 {
		payloadDocument["tools"] = tools
		payloadDocument["tool_choice"] = toolChoice
		payloadDocument["parallel_tool_calls"] = parallelToolCalls
	}
	payload, err := json.Marshal(payloadDocument)
	if err != nil || len(payload) == 0 || len(payload) > 1024*1024 {
		return ParsedResponse{}, ErrAgenticRun
	}
	session.calls++
	response, exchangeErr := session.adapter.Exchange(ctx, provider.Request{Model: session.model, Payload: payload})
	if session.captureRaw {
		raw := RawProviderExchange{
			Request: append(json.RawMessage(nil), payload...), Response: append(json.RawMessage(nil), response.Body...),
			StatusCode: response.StatusCode, RequestID: response.RequestID, Usage: cloneUsage(response.Usage),
		}
		if exchangeErr != nil {
			raw.Error = exchangeErr.Error()
		}
		session.rawExchanges = append(session.rawExchanges, raw)
	}
	usageErr := session.admitResponse(response, perCallOutput)
	if exchangeErr != nil {
		session.markLatestProtocolInvalid()
		session.closed = true
		return ParsedResponse{}, fmt.Errorf("%w: provider exchange", ErrAgenticRun)
	}
	instructionsEcho, identityErr := validateResponseIdentity(response.Body, session.model, instructions)
	session.setLatestInstructionsEcho(instructionsEcho)
	if identityErr != nil {
		session.markLatestProtocolInvalid()
		session.closed = true
		return ParsedResponse{}, identityErr
	}
	parsed, err := ParseResponsesOutput(response.Body, providerToCanonical)
	if err != nil {
		session.markLatestProtocolInvalid()
		session.closed = true
		return ParsedResponse{}, err
	}
	if usageErr != nil {
		session.closed = true
		return ParsedResponse{}, usageErr
	}
	if session.usage.InputTokens > session.limits.MaxInputTokens || session.usage.OutputTokens > session.limits.MaxOutputTokens || session.usage.TotalTokens > session.limits.MaxTotalTokens {
		session.closed = true
		return ParsedResponse{}, ErrBudgetExceeded
	}
	for _, call := range parsed.Calls {
		if session.seenIDs[call.CallID] {
			session.closed = true
			return ParsedResponse{}, ErrAgenticRun
		}
		session.seenIDs[call.CallID] = true
	}
	return parsed, nil
}

func (session *ResponsesSession) setLatestInstructionsEcho(echo InstructionsEcho) {
	if len(session.evidence) > 0 {
		session.evidence[len(session.evidence)-1].InstructionsEcho = echo
	}
}

func (session *ResponsesSession) markLatestProtocolInvalid() {
	if len(session.evidence) > 0 {
		session.evidence[len(session.evidence)-1].ProtocolInvalid = true
	}
}

func promoteResponseInstructions(input any) (any, string, error) {
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return input, "", nil
	}
	wireInput := make([]any, 0, len(items))
	var instructions strings.Builder
	for _, item := range items {
		message, messageOK := item.(map[string]any)
		if !messageOK || message["role"] != "developer" {
			wireInput = append(wireInput, item)
			continue
		}
		content, contentOK := message["content"].(string)
		if !contentOK || content == "" || !utf8.ValidString(content) {
			return nil, "", ErrAgenticRun
		}
		separator := 0
		if instructions.Len() > 0 {
			separator = 2
		}
		if instructions.Len()+separator+len(content) > 64*1024 {
			return nil, "", ErrAgenticRun
		}
		if separator != 0 {
			instructions.WriteString("\n\n")
		}
		instructions.WriteString(content)
	}
	return wireInput, instructions.String(), nil
}

func validateResponseIdentity(body json.RawMessage, expectedModel, expectedInstructions string) (InstructionsEcho, error) {
	if expectedModel == "" || len(body) == 0 || len(body) > 1024*1024 || rejectDuplicateJSON(body) != nil {
		return InstructionsEchoInvalid, ErrProviderIdentityMismatch
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		return InstructionsEchoInvalid, ErrProviderIdentityMismatch
	}
	for key := range envelope {
		if (key != "model" && strings.EqualFold(key, "model")) || (key != "instructions" && strings.EqualFold(key, "instructions")) {
			return InstructionsEchoInvalid, ErrProviderIdentityMismatch
		}
	}
	var model string
	modelRaw, modelOK := envelope["model"]
	if !modelOK || json.Unmarshal(modelRaw, &model) != nil || model != expectedModel {
		return InstructionsEchoInvalid, ErrProviderIdentityMismatch
	}
	instructions := bytes.TrimSpace(envelope["instructions"])
	unavailable := len(instructions) == 0 || bytes.Equal(instructions, []byte("null"))
	if expectedInstructions == "" {
		if !unavailable {
			return InstructionsEchoInvalid, ErrProviderIdentityMismatch
		}
		return InstructionsEchoNotApplicable, nil
	}
	if unavailable {
		return InstructionsEchoUnavailable, nil
	}
	var echoed string
	if json.Unmarshal(instructions, &echoed) != nil || echoed != expectedInstructions {
		return InstructionsEchoInvalid, ErrProviderIdentityMismatch
	}
	return InstructionsEchoExact, nil
}

func (session *ResponsesSession) admitResponse(response provider.Response, requestedMaxOutput uint64) error {
	if response.Usage == nil {
		return ErrUsageMissing
	}
	declaredTotal, declaredOK := checkedAdd(response.Usage.InputTokens, response.Usage.OutputTokens)
	if !declaredOK || response.Usage.TotalTokens != declaredTotal {
		return ErrUsageMissing
	}
	input, inputOK := checkedAdd(session.usage.InputTokens, response.Usage.InputTokens)
	output, outputOK := checkedAdd(session.usage.OutputTokens, response.Usage.OutputTokens)
	total, totalOK := checkedAdd(session.usage.TotalTokens, response.Usage.TotalTokens)
	if !inputOK || !outputOK || !totalOK {
		return ErrBudgetExceeded
	}
	session.usage = provider.Usage{InputTokens: input, OutputTokens: output, TotalTokens: total}
	session.evidence = append(session.evidence, ExchangeEvidence{
		StatusCode: response.StatusCode, RequestDigest: response.RequestDigest,
		ResponseDigest: response.ResponseDigest, Usage: *cloneUsage(response.Usage), InstructionsEcho: InstructionsEchoInvalid,
	})
	if requestedMaxOutput == 0 || response.Usage.OutputTokens > requestedMaxOutput {
		return ErrProviderOutputLimitExceeded
	}
	return nil
}

func checkedAdd(left, right uint64) (uint64, bool) {
	if right > math.MaxUint64-left {
		return 0, false
	}
	return left + right, true
}

func (session *ResponsesSession) ProviderCalls() uint32 {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.calls
}

func (session *ResponsesSession) Usage() provider.Usage {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.usage
}

func (session *ResponsesSession) Evidence() []ExchangeEvidence {
	session.mu.Lock()
	defer session.mu.Unlock()
	result := make([]ExchangeEvidence, len(session.evidence))
	copy(result, session.evidence)
	return result
}

func (session *ResponsesSession) RawExchanges() []RawProviderExchange {
	session.mu.Lock()
	defer session.mu.Unlock()
	result := make([]RawProviderExchange, len(session.rawExchanges))
	for index, exchange := range session.rawExchanges {
		result[index] = RawProviderExchange{
			Request: append(json.RawMessage(nil), exchange.Request...), Response: append(json.RawMessage(nil), exchange.Response...),
			StatusCode: exchange.StatusCode, RequestID: exchange.RequestID, Usage: cloneUsage(exchange.Usage), Error: exchange.Error,
		}
	}
	return result
}

func ParseResponsesOutput(body json.RawMessage, providerToCanonical map[string]string) (ParsedResponse, error) {
	if len(body) == 0 || len(body) > 1024*1024 || rejectDuplicateJSON(body) != nil {
		return ParsedResponse{}, ErrAgenticRun
	}
	var envelope struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Status != "completed" || len(envelope.Output) == 0 || len(envelope.Output) > maxFunctionCalls*2 {
		return ParsedResponse{}, ErrAgenticRun
	}
	parsed := ParsedResponse{}
	seenCalls := map[string]bool{}
	var text bytes.Buffer
	for outputItemSeq, raw := range envelope.Output {
		var header map[string]json.RawMessage
		if json.Unmarshal(raw, &header) != nil || len(header) == 0 || len(header) > 32 {
			return ParsedResponse{}, ErrAgenticRun
		}
		var itemType string
		if json.Unmarshal(header["type"], &itemType) != nil {
			return ParsedResponse{}, ErrAgenticRun
		}
		switch itemType {
		case "reasoning":
			continue
		case "function_call":
			call, replay, err := parseFunctionCall(header, providerToCanonical)
			if err != nil || seenCalls[call.CallID] || len(parsed.Calls) >= maxFunctionCalls {
				return ParsedResponse{}, ErrAgenticRun
			}
			seenCalls[call.CallID] = true
			call.OutputItemSeq = uint32(outputItemSeq)
			parsed.Calls = append(parsed.Calls, call)
			parsed.replayItems = append(parsed.replayItems, replay)
		case "message":
			message, replay, refused, err := parseMessage(header, &text)
			if err != nil {
				return ParsedResponse{}, ErrAgenticRun
			}
			parsed.HasMessage = true
			parsed.Refused = parsed.Refused || refused
			parsed.replayItems = append(parsed.replayItems, replay)
			_ = message
		default:
			return ParsedResponse{}, ErrAgenticRun
		}
	}
	if len(parsed.Calls) == 0 && !parsed.HasMessage {
		return ParsedResponse{}, ErrAgenticRun
	}
	if text.Len() > 0 {
		parsed.TextDigest = digest(text.Bytes())
	}
	return parsed, nil
}

func parseFunctionCall(header map[string]json.RawMessage, mapping map[string]string) (ResponseCall, map[string]any, error) {
	var callID, name, arguments, status string
	if rawStatus, exists := header["status"]; exists && (json.Unmarshal(rawStatus, &status) != nil || status != "completed") {
		return ResponseCall{}, nil, ErrAgenticRun
	}
	if json.Unmarshal(header["call_id"], &callID) != nil || json.Unmarshal(header["name"], &name) != nil || json.Unmarshal(header["arguments"], &arguments) != nil ||
		!validProtocolID(callID) || !providerToolNamePattern.MatchString(name) || len(arguments) == 0 || len(arguments) > maxArgumentsBytes {
		return ResponseCall{}, nil, ErrAgenticRun
	}
	canonical, exists := mapping[name]
	if !exists || canonical == "" || rejectDuplicateJSON([]byte(arguments)) != nil {
		return ResponseCall{}, nil, ErrAgenticRun
	}
	if strings.TrimSpace(arguments) == "null" {
		arguments = "{}"
	}
	var object map[string]any
	if decodeUseNumber([]byte(arguments), &object) != nil || object == nil {
		return ResponseCall{}, nil, ErrAgenticRun
	}
	call := ResponseCall{CallID: callID, ProviderName: name, CanonicalName: canonical, Arguments: json.RawMessage(arguments)}
	replay := map[string]any{"type": "function_call", "status": "completed", "call_id": callID, "name": name, "arguments": arguments}
	return call, replay, nil
}

func parseMessage(header map[string]json.RawMessage, text *bytes.Buffer) (bool, map[string]any, bool, error) {
	var role string
	var contents []map[string]json.RawMessage
	if json.Unmarshal(header["role"], &role) != nil || role != "assistant" || json.Unmarshal(header["content"], &contents) != nil || len(contents) == 0 || len(contents) > 64 {
		return false, nil, false, ErrAgenticRun
	}
	replayContent := make([]map[string]any, 0, len(contents))
	refused := false
	for _, content := range contents {
		var kind, value string
		if json.Unmarshal(content["type"], &kind) != nil || (kind != "output_text" && kind != "refusal") {
			return false, nil, false, ErrAgenticRun
		}
		valueField := "text"
		if kind == "refusal" {
			valueField = "refusal"
		}
		rawValue, exists := content[valueField]
		if !exists || len(rawValue) == 0 || json.Unmarshal(rawValue, &value) != nil || !utf8.ValidString(value) {
			return false, nil, false, ErrAgenticRun
		}
		if text.Len()+len([]byte(value)) > maxResponseTextBytes {
			return false, nil, false, ErrAgenticRun
		}
		text.WriteString(value)
		refused = refused || kind == "refusal"
		replayContent = append(replayContent, map[string]any{"type": kind, valueField: value})
	}
	return true, map[string]any{"type": "message", "role": "assistant", "content": replayContent}, refused, nil
}

func validProtocolID(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrAgenticRun
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= 4096 {
		return ErrAgenticRun
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] {
				return ErrAgenticRun
			}
			seen[key] = true
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrAgenticRun
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrAgenticRun
		}
	default:
		return ErrAgenticRun
	}
	return nil
}
