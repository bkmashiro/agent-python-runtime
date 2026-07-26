package agentic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

type stubAdapter struct {
	request provider.Request
	body    json.RawMessage
}

func (*stubAdapter) Protocol() string { return provider.LinkAPIResponsesProtocol }
func (adapter *stubAdapter) Exchange(_ context.Context, request provider.Request) (provider.Response, error) {
	adapter.request = request
	return provider.Response{
		StatusCode: 200, Body: adapter.body,
		RequestDigest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ResponseDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Usage:          &provider.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

func TestRunDirectStatelessScoresAndRedacts(t *testing.T) {
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range dataset.Tasks {
		if candidate.Source.SourceID == "parallel_multiple_112" {
			task = candidate
			break
		}
	}
	adapter := &stubAdapter{body: json.RawMessage(`{
		"id":"resp_private",
		"status":"completed",
		"output":[
			{"type":"function_call","status":"completed","call_id":"call_private_1","name":"library_search_book","arguments":"{\"title\":\"1984\",\"author\":\"George Orwell\",\"platform\":\"British Library\"}"},
			{"type":"function_call","status":"completed","call_id":"call_private_2","name":"art_auction_fetch_artwork_price","arguments":"{\"artwork_name\":\"The Scream\",\"artist\":\"Edvard Munch\",\"platform\":\"Christie\"}"},
			{"type":"function_call","status":"completed","call_id":"call_private_3","name":"library_search_book","arguments":"{\"title\":\"To Kill a Mockingbird\",\"author\":\"Harper Lee\",\"platform\":\"New York Public Library\"}"},
			{"type":"function_call","status":"completed","call_id":"call_private_4","name":"art_auction_fetch_artwork_price","arguments":"{\"artwork_name\":\"Starry Night\",\"artist\":\"Vincent Van Gogh\",\"platform\":\"Sotheby\"}"}
		],
		"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
	}`)}
	result, err := RunDirectStateless(context.Background(), adapter, task, "gpt-5.4-mini", 512)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.CallCount != 4 || result.Usage == nil || result.Usage.TotalTokens != 15 {
		t.Fatalf("result=%+v", result)
	}
	var payload map[string]any
	if json.Unmarshal(adapter.request.Payload, &payload) != nil {
		t.Fatal("request payload invalid")
	}
	tools := payload["tools"].([]any)
	for _, value := range tools {
		name := value.(map[string]any)["name"].(string)
		if strings.Contains(name, ".") {
			t.Fatalf("canonical dotted name leaked to provider: %s", name)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"resp_private", "call_private", `\"radius\":5`} {
		if containsBytes(encoded, []byte(forbidden)) {
			t.Fatalf("serialized result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunDirectStatelessRejectsEvaluationAndMalformedArguments(t *testing.T) {
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var dev, evaluation Task
	for _, task := range dataset.Tasks {
		if task.Track != "stateless_function_calling" {
			continue
		}
		if task.Split == "dev" && dev.ID == "" {
			dev = task
		}
		if task.Split == "evaluation" && evaluation.ID == "" {
			evaluation = task
		}
	}
	adapter := &stubAdapter{body: json.RawMessage(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"c1","name":"bad","arguments":"[]"}]}`)}
	if _, err := RunDirectStateless(context.Background(), adapter, evaluation, "gpt-5.4-mini", 128); err == nil {
		t.Fatal("expected evaluation split rejection")
	}
	if _, err := RunDirectStateless(context.Background(), adapter, dev, "gpt-5.4-mini", 128); err == nil {
		t.Fatal("expected malformed arguments rejection")
	}
}

func containsBytes(haystack, needle []byte) bool {
	for len(needle) <= len(haystack) {
		if string(haystack[:len(needle)]) == string(needle) {
			return true
		}
		haystack = haystack[1:]
	}
	return len(needle) == 0
}
