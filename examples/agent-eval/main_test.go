package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeProvider struct {
	arm       string
	task      string
	wrong     bool
	shape     bool
	step      int
	calls     int
	seenTools [][]ToolDefinition
	seen      [][]Message
}

func (f *fakeProvider) Complete(_ context.Context, messages []Message, tools []ToolDefinition, _ int) (ProviderResponse, error) {
	f.calls++
	f.seen = append(f.seen, append([]Message(nil), messages...))
	f.seenTools = append(f.seenTools, append([]ToolDefinition(nil), tools...))
	f.step++
	response := ProviderResponse{Model: "fake-model", Usage: &ProviderUsage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}}
	call := func(id, name, args string) ToolCall {
		return ToolCall{ID: id, Type: "function", Function: ToolFunction{Name: name, Arguments: args}}
	}
	if f.arm == "direct" {
		switch f.task {
		case "lookup":
			if f.step == 1 {
				response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("p1", "catalog_price", `{"sku":"SKU-3"}`), call("p2", "catalog_price", `{"sku":"SKU-3"}`)}}
			} else {
				answer := `{"price_cents":3199}`
				if f.wrong {
					answer = `{"price_cents":0}`
				}
				if f.shape {
					answer = `{"price_cents":3199.5}`
				}
				response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("a", "submit_answer", answer)}}
			}
		case "transient":
			if f.step < 3 {
				response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("lookup", "transient_price", `{"sku":"SKU-2"}`)}}
			} else {
				response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("answer", "submit_answer", `{"price_cents":2075}`)}}
			}
		default:
			response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("answer", "submit_answer", `{"record_count":0,"sum_cents":0}`)}}
		}
		return response, nil
	}
	// Code responses deliberately use the real Guest in the integration test.
	if f.task == "lookup" && f.step == 1 {
		response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("py1", "execute_python", `{"source":"result = catalog.price(sku='SKU-3')"}`)}}
		return response, nil
	}
	if f.task == "transient" && f.step == 1 {
		response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("py1", "execute_python", `{"source":"result = catalog.transient_price(sku='SKU-2')"}`)}}
		return response, nil
	}
	if f.task == "transient" && f.step == 2 {
		if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "transient") {
			panic("fake provider did not receive the transient Python error")
		}
		response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("py2", "execute_python", `{"source":"result = catalog.transient_price(sku='SKU-2')"}`)}}
		return response, nil
	}
	answer := `{"price_cents":3199}`
	if f.task == "transient" {
		answer = `{"price_cents":2075}`
	}
	if f.wrong {
		answer = `{"price_cents":0}`
	}
	response.Message = Message{Role: "assistant", ToolCalls: []ToolCall{call("answer", "submit_answer", answer)}}
	return response, nil
}

func TestFixtureOraclesAreExactAndIntegerOnly(t *testing.T) {
	tasks := allTasks()
	checks := []struct {
		name    string
		correct string
		wrong   string
	}{
		{"lookup", `{"price_cents":3199}`, `{"price_cents":3199.5}`},
		{"paginated_sum", `{"record_count":20,"sum_cents":46050}`, `{"record_count":20,"sum_cents":46050.5}`},
		{"join", `{"category_totals":{"alpha":43240,"beta":22825,"gamma":43290}}`, `{"category_totals":[1,2,3]}`},
		{"transient", `{"price_cents":2075}`, `{"price_cents":2075.5}`},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			ep := tasks[check.name].NewEpisode()
			ok, err := ep.CheckAnswer(json.RawMessage(check.correct))
			if err != nil || !ok {
				t.Fatalf("correct answer rejected: ok=%v err=%v", ok, err)
			}
			if ok, err := ep.CheckAnswer(json.RawMessage(check.wrong)); err == nil || ok {
				t.Fatalf("non-integer answer accepted: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestDirectSupportsMultipleCallsAndGradesWrongAnswerAfterSchemaValidation(t *testing.T) {
	task := allTasks()["lookup"]
	budget := &requestBudget{limit: 8}
	provider := &fakeProvider{arm: "direct", task: "lookup", wrong: true}
	row := runEpisode(context.Background(), provider, budget, task, 1, 1, "direct", nil, 0, &episodeHost{}, "fake-requested", 4, 0)
	if row.CompletionStatus != "wrong_answer" || row.Correctness == nil || *row.Correctness {
		t.Fatalf("row=%+v", row)
	}
	if row.DomainToolCalls != 2 || row.ModelToolCalls != 3 || row.ModelRequests != 2 {
		t.Fatalf("multiple-call accounting row=%+v", row)
	}
	if len(provider.seenTools[0]) != 2 || provider.seenTools[0][0].Function.Name != "catalog_price" {
		t.Fatalf("unexpected direct tools: %+v", provider.seenTools[0])
	}
}

func TestDirectTransientErrorIsEpisodeLocalAndRecoverable(t *testing.T) {
	task := allTasks()["transient"]
	budget := &requestBudget{limit: 8}
	provider := &fakeProvider{arm: "direct", task: "transient"}
	row := runEpisode(context.Background(), provider, budget, task, 1, 1, "direct", nil, 0, &episodeHost{}, "fake-requested", 5, 0)
	if row.CompletionStatus != "completed" || row.Correctness == nil || !*row.Correctness {
		t.Fatalf("row=%+v", row)
	}
	if row.DomainToolCalls != 2 {
		t.Fatalf("transient calls=%d, want 2", row.DomainToolCalls)
	}
	if len(provider.seen) < 2 || !strings.Contains(provider.seen[1][len(provider.seen[1])-1].Content, "transient") {
		t.Fatal("transient tool error was not returned in continuation")
	}
}

func TestSchemaShapeFailureIsNotWrongAnswer(t *testing.T) {
	task := allTasks()["lookup"]
	provider := &fakeProvider{arm: "direct", task: "lookup", shape: true}
	row := runEpisode(context.Background(), provider, &requestBudget{limit: 8}, task, 1, 1, "direct", nil, 0, &episodeHost{}, "fake-requested", 2, 0)
	if row.CompletionStatus != "protocol_error" || row.Correctness != nil {
		t.Fatalf("shape failure row=%+v", row)
	}
}

func testGuestPath(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("PYSOLATE_GUEST"); path != "" {
		return path
	}
	path := filepath.Join("..", "..", "dist", "pysolate.wasm")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("genuine Guest artifact is not available")
	}
	return path
}

func TestCodeGenuineGuestAndNegativeWrongAnswer(t *testing.T) {
	guestPath := testGuestPath(t)
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		t.Fatal(err)
	}
	task := allTasks()["lookup"]
	host := &episodeHost{}
	runner, setupNS, err := prepareRunner(context.Background(), wasm, task, host)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	provider := &fakeProvider{arm: "code", task: "lookup", wrong: true}
	row := runEpisode(context.Background(), provider, &requestBudget{limit: 8}, task, 1, 1, "code", runner, setupNS, host, "fake-requested", 4, 0)
	if row.CompletionStatus != "wrong_answer" || row.Correctness == nil || *row.Correctness {
		t.Fatalf("row=%+v", row)
	}
	if row.PysolateNS <= 0 || row.SetupNS <= 0 {
		t.Fatalf("missing code timing row=%+v", row)
	}
	if len(provider.seen) < 2 || !strings.Contains(provider.seen[1][len(provider.seen[1])-1].Content, `"price_cents"`) {
		t.Fatal("Python result was not returned to model")
	}
}

func TestCodeGenuineGuestReturnsTransientPythonErrorToModel(t *testing.T) {
	guestPath := testGuestPath(t)
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		t.Fatal(err)
	}
	task := allTasks()["transient"]
	host := &episodeHost{}
	runner, setupNS, err := prepareRunner(context.Background(), wasm, task, host)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	provider := &fakeProvider{arm: "code", task: "transient"}
	row := runEpisode(context.Background(), provider, &requestBudget{limit: 8}, task, 1, 1, "code", runner, setupNS, host, "fake-requested", 5, 0)
	if row.CompletionStatus != "completed" || row.Correctness == nil || !*row.Correctness {
		t.Fatalf("row=%+v", row)
	}
	if row.DomainToolCalls != 2 {
		t.Fatalf("domain calls=%d, want 2", row.DomainToolCalls)
	}
}

func TestOutputPathIsNotOverwrittenByConvention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		t.Fatal("O_EXCL output path was overwritten")
	}
}
