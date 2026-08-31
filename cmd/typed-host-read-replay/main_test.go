package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReplayRequestValidate(t *testing.T) {
	valid := replayRequest{
		SchemaVersion: requestSchemaVersion,
		RunID:         "smoke-1",
		SourceChunks: []sourceChunk{
			{OffsetMilliseconds: 0, Text: "a = sources.read('a')\n"},
			{OffsetMilliseconds: 25, Text: "result = [a]\n"},
		},
		Inputs:         json.RawMessage(`{}`),
		ExpectedResult: json.RawMessage(`["alpha"]`),
		Provider: providerConfig{
			DelayMilliseconds: 10,
			Values:            map[string]string{"a": "alpha"},
			Errors:            map[string]string{},
			RequiredCalls:     1,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	cases := map[string]func(*replayRequest){
		"schema": func(request *replayRequest) { request.SchemaVersion = "" },
		"run id": func(request *replayRequest) { request.RunID = "" },
		"chunks": func(request *replayRequest) { request.SourceChunks = nil },
		"offset order": func(request *replayRequest) {
			request.SourceChunks[0].OffsetMilliseconds = 30
			request.SourceChunks[1].OffsetMilliseconds = 20
		},
		"inputs": func(request *replayRequest) { request.Inputs = json.RawMessage(`[]`) },
		"provider outcomes": func(request *replayRequest) {
			request.Provider.Values = nil
			request.Provider.Errors = nil
		},
		"overlapping provider outcome": func(request *replayRequest) {
			request.Provider.Errors = map[string]string{"a": "failed"}
		},
		"required calls": func(request *replayRequest) { request.Provider.RequiredCalls = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := valid
			request.SourceChunks = append([]sourceChunk(nil), valid.SourceChunks...)
			request.Provider.Values = map[string]string{"a": "alpha"}
			request.Provider.Errors = map[string]string{}
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestReplayRequestValidateExpectedError(t *testing.T) {
	request := replayRequest{
		SchemaVersion:        requestSchemaVersion,
		RunID:                "error-smoke",
		SourceChunks:         []sourceChunk{{OffsetMilliseconds: 0, Text: "result = sources.read('missing')\n"}},
		Inputs:               json.RawMessage(`{}`),
		ExpectedResult:       json.RawMessage(`null`),
		ExpectedErrorClass:   "RuntimeError",
		ExpectedErrorMessage: "fixture failure",
		Provider: providerConfig{
			Errors:        map[string]string{"missing": "fixture failure"},
			RequiredCalls: 1,
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid expected error rejected: %v", err)
	}
	request.ExpectedErrorMessage = ""
	if err := request.Validate(); err == nil {
		t.Fatal("partial expected error accepted")
	}
}

func TestFixedProviderReturnsConfiguredValueAndTrace(t *testing.T) {
	provider := newFixedProvider(providerConfig{
		Values:        map[string]string{"a": "alpha"},
		RequiredCalls: 1,
	})
	response, err := provider.Handle(context.Background(), json.RawMessage(`{"path":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != `{"body":"alpha"}` {
		t.Fatalf("response=%s", response)
	}
	trace := provider.Trace()
	if trace.Attempts != 1 || len(trace.Keys) != 1 || trace.Keys[0] != "a" || trace.ResultBytes != uint64(len(response)) {
		t.Fatalf("trace=%+v", trace)
	}
}

func TestFixedProviderRejectsUnknownKey(t *testing.T) {
	provider := newFixedProvider(providerConfig{
		Values:        map[string]string{"a": "alpha"},
		RequiredCalls: 1,
	})
	if _, err := provider.Handle(context.Background(), json.RawMessage(`{"path":"missing"}`)); err == nil {
		t.Fatal("unknown key accepted")
	}
	if trace := provider.Trace(); trace.Attempts != 1 || len(trace.Keys) != 1 || trace.Keys[0] != "missing" {
		t.Fatalf("trace=%+v", trace)
	}
}

func TestFixedProviderReturnsConfiguredErrorAndTrace(t *testing.T) {
	provider := newFixedProvider(providerConfig{
		Errors:        map[string]string{"missing": "fixture failure"},
		RequiredCalls: 1,
	})
	if _, err := provider.Handle(context.Background(), json.RawMessage(`{"path":"missing"}`)); err == nil || err.Error() != "fixture failure" {
		t.Fatalf("error=%v", err)
	}
	if trace := provider.Trace(); trace.Attempts != 1 || len(trace.Keys) != 1 || trace.Keys[0] != "missing" || trace.ResultBytes != 0 {
		t.Fatalf("trace=%+v", trace)
	}
}
