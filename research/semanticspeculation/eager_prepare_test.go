package semanticspeculation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildEagerComparatorPrepareChunksBindsResearchOnlySession(t *testing.T) {
	fragments, err := BuildEagerComparatorPrepareChunks(EagerComparatorPrepareConfig{
		Inputs:             json.RawMessage(`{"value":2}`),
		Chunks:             []string{"base = inputs['value'] + 1\n", "result = base\n"},
		AllowedImportRoots: []string{"json", "math"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fragments) != 4 || !strings.Contains(fragments[0], "from agent_runtime import eager_comparator") ||
		!strings.Contains(fragments[0], "_eager_comparator_module._begin") || !strings.Contains(fragments[0], `["json","math"]`) ||
		!strings.Contains(fragments[1], "._chunk(") || !strings.Contains(fragments[3], "._finish()") {
		t.Fatalf("fragments=%q", fragments)
	}
	for _, fragment := range fragments {
		if strings.Contains(fragment, "_stream_begin") || strings.Contains(fragment, "_stream_chunk") {
			t.Fatalf("production stream activated: %q", fragment)
		}
	}
}

func TestBuildEagerComparatorPrepareChunksRejectsInvalidInput(t *testing.T) {
	for _, config := range []EagerComparatorPrepareConfig{
		{Inputs: json.RawMessage(`{`), Chunks: []string{"result = 1\n"}},
		{Inputs: json.RawMessage(`{}`)},
		{Inputs: json.RawMessage(`{}`), Chunks: []string{""}},
	} {
		if _, err := BuildEagerComparatorPrepareChunks(config); err == nil {
			t.Fatalf("accepted config=%+v", config)
		}
	}
	for _, roots := range [][]string{{"math", "json"}, {"json", "json"}, {"_agent_runtime_host"}, {"not-a-root"}} {
		config := EagerComparatorPrepareConfig{
			Inputs: json.RawMessage(`{}`), Chunks: []string{"result = 1\n"}, AllowedImportRoots: roots,
		}
		if _, err := BuildEagerComparatorPrepareChunks(config); err == nil {
			t.Fatalf("accepted roots=%v", roots)
		}
	}
}
