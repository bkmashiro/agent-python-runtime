package agentic

import (
	"encoding/json"
	"testing"
)

func TestScoreStatelessCallsMatchesBFCLParallelSemantics(t *testing.T) {
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
	if task.ID == "" {
		t.Fatal("fixture task missing")
	}
	calls := []FunctionCall{
		callJSON(t, "library_search_book", map[string]any{"title": "1984", "author": "George Orwell", "platform": "British-Library"}),
		callJSON(t, "art_auction_fetch_artwork_price", map[string]any{"artwork_name": "The Scream", "artist": "Edvard Munch", "platform": "Christie"}),
		callJSON(t, "library_search_book", map[string]any{"title": "To Kill a Mockingbird", "author": "harper lee", "platform": "New York Public Library"}),
		callJSON(t, "art_auction_fetch_artwork_price", map[string]any{"artwork_name": "Starry Night", "artist": "Vincent Van Gogh", "platform": "Sotheby"}),
	}
	score := ScoreStatelessCalls(task, calls)
	if !score.Passed || score.ErrorCode != "" {
		t.Fatalf("score=%+v", score)
	}
}

func TestScoreStatelessCallsRejectsWrongArgumentsAndCount(t *testing.T) {
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range dataset.Tasks {
		if candidate.Source.SourceID == "parallel_multiple_2" {
			task = candidate
			break
		}
	}
	wrong := []FunctionCall{
		callJSON(t, "circle_calculate_area", map[string]any{"radius": 6}),
		callJSON(t, "circle_calculate_circumference", map[string]any{"diameter": 10}),
	}
	if score := ScoreStatelessCalls(task, wrong); score.Passed || score.ErrorCode != "call_mismatch" {
		t.Fatalf("wrong score=%+v", score)
	}
	if score := ScoreStatelessCalls(task, wrong[:1]); score.Passed || score.ErrorCode != "wrong_call_count" {
		t.Fatalf("count score=%+v", score)
	}
}

func TestProviderToolNamesAreInjectiveAndBounded(t *testing.T) {
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range dataset.Tasks {
		seen := map[string]string{}
		for _, tool := range task.Tools {
			providerName, err := ProviderToolName(tool.Name)
			if err != nil {
				t.Fatalf("task=%s tool=%s: %v", task.ID, tool.Name, err)
			}
			if prior := seen[providerName]; prior != "" && prior != tool.Name {
				t.Fatalf("task=%s collision=%s", task.ID, providerName)
			}
			seen[providerName] = tool.Name
		}
	}
}

func callJSON(t *testing.T, name string, value any) FunctionCall {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return FunctionCall{Name: name, Arguments: data}
}
