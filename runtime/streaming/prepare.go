package streaming

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

// BeginConfig binds Host-owned authority and inputs before any Agent source
// chunk arrives.
type BeginConfig struct {
	Inputs              json.RawMessage
	Plan                *capability.Plan
	SpeculationMaxCalls uint32
}

// PrepareConfig is a bounded batch convenience wrapper. Live harnesses should
// call BuildBeginPrepare, BuildChunkPrepare, and BuildEndPrepare as data arrives.
type PrepareConfig struct {
	Inputs              json.RawMessage
	Chunks              []string
	Plan                *capability.Plan
	SpeculationMaxCalls uint32
}

func BuildBeginPrepare(config BeginConfig) (string, error) {
	var inputs any
	if len(config.Inputs) == 0 || json.Unmarshal(config.Inputs, &inputs) != nil {
		return "", errors.New("stream inputs must be valid JSON")
	}
	canonicalInputs, err := json.Marshal(inputs)
	if err != nil {
		return "", fmt.Errorf("canonicalize stream inputs: %w", err)
	}
	var builder strings.Builder
	if config.Plan != nil {
		builder.WriteString(config.Plan.StreamingPythonPrelude())
	}
	builder.WriteString("\nimport json as _stream_json\nimport agent_runtime as _stream_runtime\n")
	fmt.Fprintf(&builder, "_stream_runtime._stream_begin(_stream_json.loads(%s), %d)\n", strconv.Quote(string(canonicalInputs)), config.SpeculationMaxCalls)
	return builder.String(), nil
}

func BuildChunkPrepare(chunk string) (string, error) {
	if chunk == "" {
		return "", errors.New("stream chunk must be non-empty")
	}
	return "import agent_runtime as _stream_runtime\nstream_event = _stream_runtime._stream_chunk(" + strconv.Quote(chunk) + ")\n", nil
}

func BuildEndPrepare() string {
	return "import agent_runtime as _stream_runtime\nstream_final = _stream_runtime._stream_end()\n"
}

func BuildPrepareScript(config PrepareConfig) (string, error) {
	chunks, err := BuildPrepareChunks(config)
	if err != nil {
		return "", err
	}
	return strings.Join(chunks, "\n"), nil
}

func BuildPrepareChunks(config PrepareConfig) ([]string, error) {
	if len(config.Chunks) == 0 {
		return nil, errors.New("stream chunks are required")
	}
	begin, err := BuildBeginPrepare(BeginConfig{
		Inputs: config.Inputs, Plan: config.Plan, SpeculationMaxCalls: config.SpeculationMaxCalls,
	})
	if err != nil {
		return nil, err
	}
	fragments := []string{begin}
	for _, chunk := range config.Chunks {
		fragment, err := BuildChunkPrepare(chunk)
		if err != nil {
			return nil, err
		}
		fragments = append(fragments, fragment)
	}
	return append(fragments, BuildEndPrepare()), nil
}
