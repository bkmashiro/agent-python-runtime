package codexmcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/hermesbridge"
)

const (
	ProtocolVersion = "2025-06-18"
	ToolName        = "python_runtime"
	MaxMessageBytes = 1 << 20
	// MCP carries the Runtime result both as text content and structured content.
	// Keep request authority at 1 MiB while allowing bounded envelope expansion.
	MaxResponseMessageBytes = 4 << 20
)

var ErrInvalidServer = errors.New("invalid Codex MCP server")
var ErrMessageTooLarge = errors.New("Codex MCP message exceeds configured bound")

type Executor interface {
	Execute(context.Context, hermesbridge.ExecuteRequest) hermesbridge.ExecuteResponse
}

type IDSource func() (string, error)

type Server struct {
	executor   Executor
	agentRunID string
	ids        IDSource

	mu       sync.Mutex
	sequence uint32
}

func NewServer(executor Executor, agentRunID string, ids IDSource) (*Server, error) {
	if executor == nil || ids == nil || !boundedIdentifier(agentRunID, 128) {
		return nil, ErrInvalidServer
	}
	return &Server{executor: executor, agentRunID: agentRunID, ids: ids}, nil
}

type requestEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type responseEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Title   string `json:"title,omitempty"`
		Version string `json:"version"`
	} `json:"clientInfo"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

type metadataOnlyParams struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type toolArguments struct {
	Code         string          `json:"code"`
	Inputs       json.RawMessage `json:"inputs,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

func (server *Server) Handle(ctx context.Context, payload []byte) ([]byte, bool) {
	if server == nil || len(payload) == 0 || len(payload) > MaxMessageBytes || rejectDuplicateJSON(payload) != nil {
		return marshalProtocolError(nil, -32700, "invalid JSON-RPC message"), true
	}
	var request requestEnvelope
	if strictDecode(payload, &request) != nil || request.JSONRPC != "2.0" || request.Method == "" || !validRequestID(request.ID) {
		return marshalProtocolError(validResponseID(request.ID), -32600, "invalid JSON-RPC request"), true
	}
	notification := len(request.ID) == 0
	if notification {
		return nil, false
	}

	switch request.Method {
	case "initialize":
		var params initializeParams
		if strictDecodeRequiredObject(request.Params, &params) != nil || params.ProtocolVersion == "" || len(params.Capabilities) == 0 ||
			!jsonObject(params.Capabilities) || !optionalObject(params.Meta) {
			return marshalProtocolError(request.ID, -32602, "invalid initialize parameters"), true
		}
		return marshalResult(request.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "agent-python-runtime", "version": "0.1.0"},
		}), true
	case "ping":
		if !metadataOnlyObject(request.Params) {
			return marshalProtocolError(request.ID, -32602, "invalid ping parameters"), true
		}
		return marshalResult(request.ID, map[string]any{}), true
	case "tools/list":
		if !metadataOnlyObject(request.Params) {
			return marshalProtocolError(request.ID, -32602, "invalid tools/list parameters"), true
		}
		return marshalResult(request.ID, map[string]any{"tools": []any{pythonRuntimeTool()}}), true
	case "tools/call":
		return server.handleToolCall(ctx, request.ID, request.Params), true
	default:
		return marshalProtocolError(request.ID, -32601, "method not found"), true
	}
}

func (server *Server) handleToolCall(ctx context.Context, id, rawParams json.RawMessage) []byte {
	var params callParams
	if strictDecodeRequiredObject(rawParams, &params) != nil || params.Name != ToolName || len(params.Arguments) == 0 ||
		rejectDuplicateJSON(params.Arguments) != nil || !optionalObject(params.Meta) {
		return marshalProtocolError(id, -32602, "invalid tools/call parameters")
	}
	var arguments toolArguments
	if strictDecodeRequiredObject(params.Arguments, &arguments) != nil {
		return marshalProtocolError(id, -32602, "invalid python_runtime arguments")
	}
	if len(arguments.Inputs) == 0 {
		arguments.Inputs = json.RawMessage(`{}`)
	}

	server.mu.Lock()
	if server.sequence == ^uint32(0) {
		server.mu.Unlock()
		return marshalProtocolError(id, -32603, "Host invocation sequence unavailable")
	}
	server.sequence++
	sequence := server.sequence
	requestID, requestErr := server.ids()
	invocationID, invocationErr := server.ids()
	server.mu.Unlock()
	if requestErr != nil || invocationErr != nil {
		return marshalProtocolError(id, -32603, "Host invocation identity unavailable")
	}

	request := hermesbridge.ExecuteRequest{
		Version: hermesbridge.ProtocolVersion, Operation: hermesbridge.OperationExecute, RequestID: requestID,
		Invocation: hermesbridge.InvocationCoordinates{
			AgentRunID: server.agentRunID, TurnSeq: sequence, OutputItemSeq: 1, SegmentSeq: 1,
			InvocationID: invocationID, InvocationAttempt: 1,
		},
		Code: arguments.Code, Inputs: append(json.RawMessage(nil), arguments.Inputs...),
		OutputSchema: append(json.RawMessage(nil), arguments.OutputSchema...),
	}
	if request.Validate() != nil {
		return marshalProtocolError(id, -32602, "invalid python_runtime arguments")
	}
	return marshalResult(id, toolResult(server.executor.Execute(ctx, request)))
}

func (server *Server) Serve(ctx context.Context, reader io.Reader, writer io.Writer) error {
	if server == nil || reader == nil || writer == nil {
		return ErrInvalidServer
	}
	type scanResult struct {
		payload []byte
		err     error
		done    bool
	}
	results := make(chan scanResult, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), MaxMessageBytes+1)
		for scanner.Scan() {
			result := scanResult{payload: append([]byte(nil), scanner.Bytes()...)}
			select {
			case results <- result:
			case <-ctx.Done():
				return
			}
		}
		err := scanner.Err()
		if errors.Is(err, bufio.ErrTooLong) {
			err = ErrMessageTooLarge
		}
		select {
		case results <- scanResult{err: err, done: true}:
		case <-ctx.Done():
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-results:
			if result.done {
				return result.err
			}
			payload, respond := server.Handle(ctx, result.payload)
			if !respond {
				continue
			}
			if len(payload) == 0 || len(payload) > MaxResponseMessageBytes {
				return ErrMessageTooLarge
			}
			if _, err := writer.Write(append(payload, '\n')); err != nil {
				return err
			}
		}
	}
}

func pythonRuntimeTool() map[string]any {
	return map[string]any{
		"name":        ToolName,
		"description": "Execute bounded Python in the pinned Agent Python Runtime. Assign the JSON-serializable answer to `result`. No host filesystem, shell, package installation, credentials, or network access is granted.",
		"inputSchema": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"code"},
			"properties": map[string]any{
				"code":          map[string]any{"type": "string", "minLength": 1, "maxLength": hermesbridge.MaxCodeBytes},
				"inputs":        map[string]any{"description": "JSON inputs exposed to Python as `inputs`"},
				"output_schema": map[string]any{"type": "object", "description": "Optional JSON Schema for `result`"},
			},
		},
		"annotations": map[string]any{
			"title": "Agent Python Runtime", "readOnlyHint": true, "destructiveHint": false,
			"idempotentHint": false, "openWorldHint": false,
		},
	}
}

func toolResult(response hermesbridge.ExecuteResponse) map[string]any {
	structured := map[string]any{"status": response.Status}
	isError := response.Status != hermesbridge.ResponseStatusOK
	if len(response.Result) != 0 {
		var result any
		if json.Unmarshal(response.Result, &result) == nil {
			structured["result"] = result
		}
	}
	if response.Error != nil {
		structured["error"] = response.Error
	}
	if response.ExecutionRef != nil {
		structured["execution_ref"] = response.ExecutionRef
	}
	if response.Metrics != nil {
		structured["metrics"] = response.Metrics
	}
	text, err := json.Marshal(structured)
	if err != nil {
		text = []byte(`{"status":"error","error":{"code":"encode_error","message":"encode runtime evidence"}}`)
		isError = true
	}
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(text)}},
		"structuredContent": structured,
		"isError":           isError,
	}
}

func marshalResult(id json.RawMessage, result any) []byte {
	payload, _ := json.Marshal(responseEnvelope{JSONRPC: "2.0", ID: id, Result: result})
	return payload
}

func marshalProtocolError(id json.RawMessage, code int, message string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	payload, _ := json.Marshal(responseEnvelope{JSONRPC: "2.0", ID: id, Error: &responseError{Code: code, Message: message}})
	return payload
}

func validResponseID(id json.RawMessage) json.RawMessage {
	if validRequestID(id) && len(id) != 0 {
		return id
	}
	return nil
}

func validRequestID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return boundedIdentifier(typed, 128)
	case json.Number:
		return len(typed.String()) <= 32
	default:
		return false
	}
}

func strictDecode(payload []byte, destination any) error {
	if len(payload) == 0 || rejectDuplicateJSON(payload) != nil {
		return errors.New("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func strictDecodeRequiredObject(payload []byte, destination any) error {
	if !jsonObject(payload) {
		return errors.New("object required")
	}
	return strictDecode(payload, destination)
}

func jsonObject(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' && json.Valid(trimmed)
}

func metadataOnlyObject(payload []byte) bool {
	if len(payload) == 0 {
		return true
	}
	var params metadataOnlyParams
	return strictDecodeRequiredObject(payload, &params) == nil && optionalObject(params.Meta)
}

func optionalObject(payload []byte) bool {
	return len(payload) == 0 || jsonObject(payload)
}

func boundedIdentifier(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}

func rejectDuplicateJSON(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= 4096 {
		return errors.New("JSON bound exceeded")
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
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return errors.New("invalid JSON object")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate JSON key")
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}
