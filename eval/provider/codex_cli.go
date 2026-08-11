package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const CodexCLIProtocol = "codex-cli-json-v1"

const (
	codexCLIMaxEvents      = 64
	codexCLIMaxOutputItems = 32
	codexCLISandbox        = "read-only"
	codexCLIReasoningFlag  = `model_reasoning_effort="xhigh"`
)

var codexCLIFunctionName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

type codexCommandRunner func(context.Context, string, []string, []byte, string, time.Duration) ([]byte, error)

type CodexCLI struct {
	executable string
	model      string
	workdir    string
	timeout    time.Duration
	runCommand codexCommandRunner
}

func NewCodexCLIAdapter(executablePath, model, workdir string, timeout time.Duration) (*CodexCLI, error) {
	return NewCodexCLIAdapterWithRunner(executablePath, model, workdir, timeout, runCodexCLICommand)
}

func NewCodexCLIAdapterWithRunner(executablePath, model, workdir string, timeout time.Duration, runCommand codexCommandRunner) (*CodexCLI, error) {
	if runCommand == nil || timeout <= 0 || !boundedModel.MatchString(model) {
		return nil, fmt.Errorf("%w: invalid Codex CLI adapter configuration", ErrExchange)
	}
	executable := strings.TrimSpace(executablePath)
	if executable == "" {
		var err error
		executable, err = exec.LookPath("codex")
		if err != nil {
			return nil, fmt.Errorf("%w: codex executable not found", ErrExchange)
		}
	}
	workdir = strings.TrimSpace(workdir)
	if !filepath.IsAbs(workdir) {
		return nil, fmt.Errorf("%w: invalid codex working directory", ErrExchange)
	}
	info, err := os.Stat(workdir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: invalid codex working directory", ErrExchange)
	}
	return &CodexCLI{executable: executable, model: model, workdir: workdir, timeout: timeout, runCommand: runCommand}, nil
}

func runCodexCLICommand(ctx context.Context, executable string, args []string, stdin []byte, workdir string, timeout time.Duration) ([]byte, error) {
	execCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(execCtx, executable, args...)
	cmd.Dir = workdir
	cmd.Stdin = bytes.NewReader(stdin)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	type captured struct {
		data []byte
		err  error
	}
	stdoutCh := make(chan captured, 1)
	stderrCh := make(chan captured, 1)
	go func() {
		output, readErr := readBoundedOutput(stdoutPipe)
		stdoutCh <- captured{data: output, err: readErr}
	}()
	go func() {
		output, readErr := readBoundedOutput(stderrPipe)
		stderrCh <- captured{data: output, err: readErr}
	}()
	stdoutResult := <-stdoutCh
	stderrResult := <-stderrCh
	waitErr := cmd.Wait()
	if stdoutResult.err != nil {
		return nil, stdoutResult.err
	}
	if stderrResult.err != nil {
		return nil, stderrResult.err
	}
	if waitErr != nil {
		if execCtx.Err() != nil {
			return nil, execCtx.Err()
		}
		return nil, waitErr
	}
	return stdoutResult.data, nil
}

func (*CodexCLI) Protocol() string { return CodexCLIProtocol }

func (adapter *CodexCLI) Exchange(ctx context.Context, request Request) (Response, error) {
	if adapter == nil || request.Model == "" || request.Model != adapter.model {
		return Response{}, fmt.Errorf("%w: invalid request", ErrExchange)
	}
	_, instructions, err := parseCodexCLIRequest(request)
	if err != nil {
		return Response{}, err
	}
	prompt := fmt.Sprintf(
		`Do not inspect files, run commands, use tools, or execute tool activity. You are a model endpoint.
	You must only return JSON proposals for function calls. Never execute tools.
	Return one strict JSON object with this exact schema:
	{"output":[ITEM,...]}
	where ITEM is either:
	{"type":"output_text","text":"nonempty"}
	{"type":"function_call","name":"bounded identifier","arguments":{...}}
	The output array must include 1 to %d items.
	Input request (bounded Responses payload):
	%s`,
		codexCLIMaxOutputItems, string(request.Payload),
	)
	args := []string{
		"--model", request.Model,
		"-c", codexCLIReasoningFlag,
		"--cd", adapter.workdir,
		"--sandbox", codexCLISandbox,
		"--ask-for-approval", "never",
		"exec", "--json", "-",
	}
	raw, err := adapter.runCommand(ctx, adapter.executable, args, []byte(prompt), adapter.workdir, adapter.timeout)
	if err != nil {
		return Response{}, fmt.Errorf("%w: command failed", ErrExchange)
	}
	threadID, output, usage, err := parseCodexCLIResponse(raw)
	if err != nil {
		return Response{}, err
	}
	body, err := buildCodexCLIResponseBody(request.Model, instructions, output, usage)
	if err != nil {
		return Response{}, err
	}
	return Response{
		Protocol:       CodexCLIProtocol,
		StatusCode:     200,
		Body:           body,
		RequestID:      threadID,
		RequestDigest:  digest(request.Payload),
		ResponseDigest: digest(body),
		Usage:          &usage,
	}, nil
}

func parseCodexCLIRequest(request Request) (map[string]json.RawMessage, string, error) {
	if !boundedModel.MatchString(request.Model) || len(request.Payload) == 0 || len(request.Payload) > maxExchangeBytes || !json.Valid(request.Payload) {
		return nil, "", fmt.Errorf("%w: invalid request", ErrExchange)
	}
	var envelope map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(request.Payload))
	decoder.UseNumber()
	if decoder.Decode(&envelope) != nil || len(envelope) == 0 || len(envelope) > 64 {
		return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	var model string
	if err := json.Unmarshal(envelope["model"], &model); err != nil || model != request.Model {
		return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	_, hasInput := envelope["input"]
	if !hasInput {
		return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	rawMaxOutput, hasMaxOutputTokens := envelope["max_output_tokens"]
	if !hasMaxOutputTokens {
		return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	var maxOutputTokens json.Number
	if err := json.Unmarshal(rawMaxOutput, &maxOutputTokens); err != nil {
		return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	parsedMaxOutput, maxOutputErr := parseUint(maxOutputTokens)
	if maxOutputErr != nil || parsedMaxOutput == 0 {
		return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	if rawStream, hasStream := envelope["stream"]; hasStream {
		var stream bool
		if err := json.Unmarshal(rawStream, &stream); err != nil || stream {
			return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
		}
	}
	if rawBackground, hasBackground := envelope["background"]; hasBackground {
		var background bool
		if err := json.Unmarshal(rawBackground, &background); err != nil || background {
			return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
		}
	}
	if _, hasMessages := envelope["messages"]; hasMessages {
		return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	if _, hasChatMax := envelope["max_tokens"]; hasChatMax {
		return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	var instructions string
	if rawInstructions, hasInstructions := envelope["instructions"]; hasInstructions {
		if err := json.Unmarshal(rawInstructions, &instructions); err != nil {
			return nil, "", fmt.Errorf("%w: invalid Responses payload", ErrExchange)
		}
	}
	return envelope, instructions, nil
}

func parseCodexCLIResponse(raw []byte) (string, []json.RawMessage, Usage, error) {
	if len(raw) == 0 || len(raw) > maxExchangeBytes {
		return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	lines := bytes.Split(raw, []byte{'\n'})
	if len(lines) == 0 {
		return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 || len(lines) > codexCLIMaxEvents {
		return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}

	const (
		needThread = iota
		needTurn
		needItemCompleted
		needTurnCompleted
		done
	)
	state := needThread
	var threadID string
	var output []json.RawMessage
	var usage Usage
	for _, rawLine := range lines {
		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		if rejectDuplicateJSON(line) != nil {
			return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		var event map[string]json.RawMessage
		if json.Unmarshal(line, &event) != nil || len(event) == 0 || len(event) > 32 {
			return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		rawType, hasType := event["type"]
		if !hasType {
			return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		var kind string
		if err := json.Unmarshal(rawType, &kind); err != nil {
			return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		switch kind {
		case "thread.started":
			if state != needThread {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			if len(event) != 2 {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			thread, err := parseCodexStringField(event, "thread_id")
			if err != nil || !boundedIdentity.MatchString(thread) {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			threadID = thread
			state = needTurn
		case "turn.started":
			if state != needTurn || len(event) != 1 {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			state = needItemCompleted
		case "item.completed":
			if state != needItemCompleted {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			rawItem, hasItem := event["item"]
			if !hasItem || len(event) != 2 {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			parsed, err := parseCodexCLIItemCompleted(rawItem, threadID)
			if err != nil {
				return "", nil, Usage{}, err
			}
			if len(parsed) == 0 || len(parsed) > codexCLIMaxOutputItems {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			output = append([]json.RawMessage(nil), parsed...)
			state = needTurnCompleted
		case "turn.completed":
			if state != needTurnCompleted || len(event) != 2 {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			rawUsage, hasUsage := event["usage"]
			if !hasUsage {
				return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
			}
			parsed, err := parseCodexCLIUsage(rawUsage)
			if err != nil {
				return "", nil, Usage{}, err
			}
			usage = parsed
			state = done
		default:
			return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
	}
	if state != done || usage.TotalTokens != usage.InputTokens+usage.OutputTokens || threadID == "" || len(output) == 0 {
		return "", nil, Usage{}, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	return threadID, output, usage, nil
}

func parseCodexCLIItemCompleted(raw json.RawMessage, threadID string) ([]json.RawMessage, error) {
	if len(raw) == 0 || rejectDuplicateJSON(raw) != nil {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil || len(item) == 0 || len(item) > 12 {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	rawType, hasType := item["type"]
	if !hasType {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var kind string
	if err := json.Unmarshal(rawType, &kind); err != nil || kind != "agent_message" {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	if _, hasID := item["id"]; !hasID || len(item) != 3 {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	rawText, hasText := item["text"]
	if !hasText {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var text string
	if err := json.Unmarshal(rawText, &text); err != nil || text == "" {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	return parseCodexCLIResponseText(text, threadID)
}

func parseCodexCLIResponseText(raw, threadID string) ([]json.RawMessage, error) {
	rawEnvelope := []byte(raw)
	if len(rawEnvelope) == 0 || len(rawEnvelope) > maxExchangeBytes || rejectDuplicateJSON(rawEnvelope) != nil {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(rawEnvelope, &envelope) != nil || len(envelope) != 1 {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	rawOutput, hasOutput := envelope["output"]
	if !hasOutput {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var output []json.RawMessage
	if json.Unmarshal(rawOutput, &output) != nil || len(output) == 0 || len(output) > codexCLIMaxOutputItems {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	callIndex := 1
	parsed := make([]json.RawMessage, 0, len(output))
	for _, rawItem := range output {
		item, kind, err := parseCodexCLIOutputItem(rawItem, threadID, callIndex)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, item)
		if kind == "function_call" {
			callIndex++
		}
	}
	return parsed, nil
}

func parseCodexCLIOutputItem(raw json.RawMessage, threadID string, callIndex int) (json.RawMessage, string, error) {
	if len(raw) == 0 || len(raw) > maxExchangeBytes || rejectDuplicateJSON(raw) != nil {
		return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil || len(item) == 0 || len(item) > 8 {
		return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var kind string
	if err := json.Unmarshal(item["type"], &kind); err != nil || kind == "" {
		return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	switch kind {
	case "output_text":
		if len(item) != 2 {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		if _, hasCallID := item["call_id"]; hasCallID {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		rawText, hasText := item["text"]
		if !hasText {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		var text string
		if err := json.Unmarshal(rawText, &text); err != nil || strings.TrimSpace(text) == "" {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		if len(text) > maxExchangeBytes {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		message := map[string]any{
			"type":    "message",
			"role":    "assistant",
			"content": []map[string]any{{"type": "output_text", "text": text}},
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return nil, "output_text", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		return encoded, "output_text", nil
	case "function_call":
		if len(item) != 3 {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		if _, hasCallID := item["call_id"]; hasCallID {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		var name string
		if err := json.Unmarshal(item["name"], &name); err != nil || !codexCLIFunctionName.MatchString(name) {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		rawArguments, hasArguments := item["arguments"]
		if !hasArguments {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		arguments, err := parseCodexFunctionCallArguments(rawArguments)
		if err != nil {
			return nil, "", err
		}
		if !boundedIdentity.MatchString(threadID) {
			return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		message := map[string]any{
			"type":      "function_call",
			"status":    "completed",
			"call_id":   fmt.Sprintf("call_%s_%d", strings.ReplaceAll(threadID, "-", ""), callIndex),
			"name":      name,
			"arguments": string(arguments),
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return nil, "function_call", fmt.Errorf("%w: malformed cli output", ErrExchange)
		}
		return encoded, "function_call", nil
	default:
		return nil, "", fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
}

func parseCodexStringField(event map[string]json.RawMessage, key string) (string, error) {
	raw, hasValue := event[key]
	if !hasValue {
		return "", fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	return value, nil
}

func parseCodexFunctionCallArguments(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxExchangeBytes || rejectDuplicateJSON(raw) != nil {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	if len(normalized) > maxExchangeBytes {
		return nil, fmt.Errorf("%w: malformed cli output", ErrExchange)
	}
	return normalized, nil
}

func parseCodexCLIUsage(raw json.RawMessage) (Usage, error) {
	if len(raw) == 0 || rejectDuplicateJSON(raw) != nil {
		return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || len(envelope) == 0 || len(envelope) > 6 {
		return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
	}
	rawInput, hasInput := envelope["input_tokens"]
	rawOutput, hasOutput := envelope["output_tokens"]
	if !hasInput || !hasOutput {
		return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
	}
	input, inputErr := parseUintJSON(rawInput)
	output, outputErr := parseUintJSON(rawOutput)
	if inputErr != nil || outputErr != nil {
		return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
	}
	if rawTotal, hasTotal := envelope["total_tokens"]; hasTotal {
		total, totalErr := parseUintJSON(rawTotal)
		if totalErr != nil {
			return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
		}
		if total != input+output {
			return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
		}
	}
	if rawCachedInput, hasCachedInput := envelope["cached_input_tokens"]; hasCachedInput {
		if _, err := parseUintJSON(rawCachedInput); err != nil {
			return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
		}
	}
	if rawCacheWriteInput, hasCacheWriteInput := envelope["cache_write_input_tokens"]; hasCacheWriteInput {
		if _, err := parseUintJSON(rawCacheWriteInput); err != nil {
			return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
		}
	}
	if rawReasoning, hasReasoning := envelope["reasoning_output_tokens"]; hasReasoning {
		if _, err := parseUintJSON(rawReasoning); err != nil {
			return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
		}
	}
	for key := range envelope {
		switch key {
		case "input_tokens", "output_tokens", "total_tokens", "cached_input_tokens", "cache_write_input_tokens", "reasoning_output_tokens":
		default:
			return Usage{}, fmt.Errorf("%w: malformed cli usage", ErrExchange)
		}
	}
	return Usage{InputTokens: input, OutputTokens: output, TotalTokens: input + output}, nil
}

func buildCodexCLIResponseBody(model, instructions string, output []json.RawMessage, usage Usage) ([]byte, error) {
	document := map[string]any{
		"model":  model,
		"status": "completed",
		"output": output,
		"usage": map[string]uint64{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
			"total_tokens":  usage.TotalTokens,
		},
	}
	if instructions != "" {
		document["instructions"] = instructions
	}
	body, err := json.Marshal(document)
	if err != nil || len(body) == 0 || len(body) > maxExchangeBytes {
		return nil, fmt.Errorf("%w: invalid response", ErrExchange)
	}
	return body, nil
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrExchange
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= 4096 {
		return ErrExchange
	}
	(*nodes)++
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
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return ErrExchange
			}
			seen[key] = true
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrExchange
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrExchange
		}
	default:
		return ErrExchange
	}
	return nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (bounded *boundedBuffer) Write(data []byte) (int, error) {
	if bounded.overflow {
		return len(data), nil
	}
	remaining := bounded.limit - bounded.buffer.Len()
	if remaining <= 0 {
		bounded.overflow = true
		return len(data), nil
	}
	if len(data) > remaining {
		if _, err := bounded.buffer.Write(data[:remaining]); err != nil {
			return 0, err
		}
		bounded.overflow = true
		return len(data), nil
	}
	return bounded.buffer.Write(data)
}

func readBoundedOutput(reader io.Reader) ([]byte, error) {
	buffer := &boundedBuffer{limit: maxExchangeBytes}
	if _, err := io.Copy(buffer, reader); err != nil {
		return nil, err
	}
	if buffer.overflow {
		return nil, fmt.Errorf("%w: command output overflow", ErrExchange)
	}
	return append([]byte(nil), buffer.buffer.Bytes()...), nil
}

func parseUintJSON(raw json.RawMessage) (uint64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, err
	}
	return parseUint(number)
}
