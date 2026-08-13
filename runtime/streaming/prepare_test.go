package streaming

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestBuildPrepareScriptQuotesUntrustedChunksAndCanonicalizesInputs(t *testing.T) {
	script, err := BuildPrepareScript(PrepareConfig{
		Inputs: json.RawMessage(`{"z":2,"a":1}`),
		Chunks: []string{"value = \"'''\\nimport os\"\n", "result = value\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`_stream_json.loads("{\"a\":1,\"z\":2}")`,
		"_stream_chunk(" + strconv.Quote("value = \"'''\\nimport os\"\n") + ")",
		"stream_final = _stream_runtime._stream_end()",
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("script missing %q:\n%s", fragment, script)
		}
	}
}

func TestBuildPrepareScriptRejectsInvalidEnvelope(t *testing.T) {
	for name, config := range map[string]PrepareConfig{
		"invalid inputs": {Inputs: json.RawMessage(`{`), Chunks: []string{"result=1\n"}},
		"no chunks":      {Inputs: json.RawMessage(`{}`)},
		"empty chunk":    {Inputs: json.RawMessage(`{}`), Chunks: []string{""}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildPrepareScript(config); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestLivePrepareBuildersDoNotRequireFutureChunks(t *testing.T) {
	begin, err := BuildBeginPrepare(BeginConfig{Inputs: json.RawMessage(`{"value":1}`), SpeculationMaxCalls: 2})
	if err != nil || !strings.Contains(begin, "_stream_begin") {
		t.Fatalf("begin=%q err=%v", begin, err)
	}
	chunk, err := BuildChunkPrepare("result = inputs['value']\n")
	if err != nil || !strings.Contains(chunk, "_stream_chunk") {
		t.Fatalf("chunk=%q err=%v", chunk, err)
	}
	if end := BuildEndPrepare(); !strings.Contains(end, "_stream_end") {
		t.Fatalf("end=%q", end)
	}
}
