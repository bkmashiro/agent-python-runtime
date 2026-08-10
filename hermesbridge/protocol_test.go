package hermesbridge

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func validExecuteRequest() ExecuteRequest {
	return ExecuteRequest{
		Version:   ProtocolVersion,
		Operation: OperationExecute,
		RequestID: "request-1",
		Invocation: InvocationCoordinates{
			AgentRunID: "agent-run-1", TurnSeq: 2, OutputItemSeq: 3, SegmentSeq: 1,
			InvocationID: "tool-call-1", InvocationAttempt: 1,
		},
		Code:         "result = inputs['left'] + inputs['right']",
		Inputs:       json.RawMessage(`{"left":19,"right":23}`),
		OutputSchema: json.RawMessage(`{"type":"integer"}`),
	}
}

func TestDecodeExecuteRequestAcceptsCanonicalEnvelope(t *testing.T) {
	payload, err := json.Marshal(validExecuteRequest())
	if err != nil {
		t.Fatal(err)
	}
	request, err := DecodeExecuteRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if request.RequestID != "request-1" || request.Invocation.InvocationID != "tool-call-1" {
		t.Fatalf("unexpected request: %#v", request)
	}
}

func TestExecuteRequestAcceptsTypedRequirementsAndRejectsUnknownFeature(t *testing.T) {
	request := validExecuteRequest()
	request.Requirements = []runtimeconfig.RequiredFeature{runtimeconfig.RequiredFeaturePOSIX}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeExecuteRequest(payload)
	if err != nil || len(decoded.Requirements) != 1 || decoded.Requirements[0] != runtimeconfig.RequiredFeaturePOSIX {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	request.Requirements = []runtimeconfig.RequiredFeature{"gpu"}
	if request.Validate() == nil {
		t.Fatal("unknown compatibility requirement accepted")
	}
}

func TestExecuteRequestAcceptsCompatibilityManifest(t *testing.T) {
	request := validExecuteRequest()
	request.Compatibility = &runtimeconfig.CompatibilityDeclaration{Profile: "base", Imports: []string{"json"}}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeExecuteRequest(payload)
	if err != nil || decoded.Compatibility == nil || decoded.Compatibility.Profile != "base" {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	request.Compatibility = &runtimeconfig.CompatibilityDeclaration{Profile: "base", Imports: []string{"json", "json"}}
	if request.Validate() == nil {
		t.Fatal("duplicate declared import accepted")
	}
}

func TestDecodeExecuteRequestRejectsUnknownAndTrailingFields(t *testing.T) {
	for name, payload := range map[string]string{
		"unknown top-level":  `{"version":"hermes-python-runtime-bridge/v1","operation":"execute","request_id":"r","invocation":{"agent_run_id":"a","turn_seq":1,"output_item_seq":1,"segment_seq":1,"invocation_id":"i","invocation_attempt":1},"code":"result=1","inputs":{},"extra":true}`,
		"unknown invocation": `{"version":"hermes-python-runtime-bridge/v1","operation":"execute","request_id":"r","invocation":{"agent_run_id":"a","turn_seq":1,"output_item_seq":1,"segment_seq":1,"invocation_id":"i","invocation_attempt":1,"execution_id":"guest-forged"},"code":"result=1","inputs":{}}`,
		"trailing JSON":      `{"version":"hermes-python-runtime-bridge/v1","operation":"execute","request_id":"r","invocation":{"agent_run_id":"a","turn_seq":1,"output_item_seq":1,"segment_seq":1,"invocation_id":"i","invocation_attempt":1},"code":"result=1","inputs":{}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeExecuteRequest([]byte(payload)); err == nil {
				t.Fatal("invalid request was accepted")
			}
		})
	}
}

func TestExecuteRequestRejectsHostAuthorityAndBoundsViolations(t *testing.T) {
	for name, mutate := range map[string]func(*ExecuteRequest){
		"wrong version":      func(r *ExecuteRequest) { r.Version = "v0" },
		"wrong operation":    func(r *ExecuteRequest) { r.Operation = "status" },
		"empty code":         func(r *ExecuteRequest) { r.Code = "  " },
		"nul code":           func(r *ExecuteRequest) { r.Code = "result=1\x00" },
		"oversize code":      func(r *ExecuteRequest) { r.Code = strings.Repeat("x", MaxCodeBytes+1) },
		"zero attempt":       func(r *ExecuteRequest) { r.Invocation.InvocationAttempt = 0 },
		"invalid identifier": func(r *ExecuteRequest) { r.RequestID = "has space" },
		"missing inputs":     func(r *ExecuteRequest) { r.Inputs = nil },
		"invalid schema":     func(r *ExecuteRequest) { r.OutputSchema = json.RawMessage(`[]`) },
	} {
		t.Run(name, func(t *testing.T) {
			request := validExecuteRequest()
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("invalid request was accepted")
			}
		})
	}
}

func TestFrameRoundTripAndBounds(t *testing.T) {
	payload := []byte(`{"status":"ok"}`)
	var wire bytes.Buffer
	if err := WriteFrame(&wire, payload, MaxFrameBytes); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&wire, MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q want %q", got, payload)
	}

	var oversized bytes.Buffer
	if err := binary.Write(&oversized, binary.BigEndian, uint32(MaxFrameBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFrame(&oversized, MaxFrameBytes); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v, want ErrFrameTooLarge", err)
	}
	if err := WriteFrame(&bytes.Buffer{}, make([]byte, MaxFrameBytes+1), MaxFrameBytes); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v, want ErrFrameTooLarge", err)
	}
}
