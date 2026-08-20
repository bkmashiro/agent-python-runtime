package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const SyntheticCaseProjectionSchemaVersion = "pysolate.semantic-speculation-synthetic-case.v1"

type SyntheticChunk struct {
	Source                   string
	ReleaseAfterMilliseconds uint32
}

type SyntheticCase struct {
	ID                   string
	Class                string
	Inputs               json.RawMessage
	Chunks               []SyntheticChunk
	ExpectedOutcome      string
	ExpectedLogicalCalls uint32
}

type SyntheticChunkProjection struct {
	Index                    uint32 `json:"index"`
	SourceSHA256             string `json:"source_sha256"`
	ReleaseAfterMilliseconds uint32 `json:"release_after_milliseconds"`
}

type SyntheticCaseProjection struct {
	SchemaVersion        string                     `json:"schema_version"`
	ID                   string                     `json:"id"`
	Class                string                     `json:"class"`
	SourceSHA256         string                     `json:"source_sha256"`
	SourceScheduleSHA256 string                     `json:"source_schedule_sha256"`
	InputsSHA256         string                     `json:"inputs_sha256"`
	ExpectedOutcome      string                     `json:"expected_outcome"`
	ExpectedLogicalCalls uint32                     `json:"expected_logical_calls"`
	Chunks               []SyntheticChunkProjection `json:"chunks"`
}

func Phase3SyntheticCases() []SyntheticCase {
	return []SyntheticCase{
		{
			ID: "branch_not_taken", Class: "adversarial", Inputs: json.RawMessage(`{}`),
			Chunks: []SyntheticChunk{
				{Source: "if False:\n    value = time.read('weather')\n", ReleaseAfterMilliseconds: 0},
				{Source: "result = {'ok': True}\n", ReleaseAfterMilliseconds: 150},
			},
			ExpectedOutcome: "success", ExpectedLogicalCalls: 0,
		},
		{
			ID: "earlier_exception", Class: "adversarial", Inputs: json.RawMessage(`{}`),
			Chunks: []SyntheticChunk{
				{Source: "raise RuntimeError('before')\n", ReleaseAfterMilliseconds: 0},
				{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 150},
				{Source: "result = value\n", ReleaseAfterMilliseconds: 300},
			},
			ExpectedOutcome: "runtime_error", ExpectedLogicalCalls: 0,
		},
		{
			ID: "external_read_valid_suffix", Class: "positive", Inputs: json.RawMessage(`{"tail":"done"}`),
			Chunks: []SyntheticChunk{
				{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0},
				{Source: "tail = inputs['tail']\n", ReleaseAfterMilliseconds: 150},
				{Source: "result = {'value': value, 'tail': tail}\n", ReleaseAfterMilliseconds: 300},
			},
			ExpectedOutcome: "success", ExpectedLogicalCalls: 1,
		},
		{
			ID: "later_runtime_error", Class: "adversarial", Inputs: json.RawMessage(`{}`),
			Chunks: []SyntheticChunk{
				{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0},
				{Source: "raise RuntimeError('after')\n", ReleaseAfterMilliseconds: 150},
				{Source: "result = value\n", ReleaseAfterMilliseconds: 300},
			},
			ExpectedOutcome: "runtime_error", ExpectedLogicalCalls: 1,
		},
		{
			ID: "later_syntax_error", Class: "adversarial", Inputs: json.RawMessage(`{}`),
			Chunks: []SyntheticChunk{
				{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0},
				{Source: "result = )\n", ReleaseAfterMilliseconds: 150},
			},
			ExpectedOutcome: "syntax_error", ExpectedLogicalCalls: 0,
		},
		{
			ID: "pure_local", Class: "control", Inputs: json.RawMessage(`{"n":3}`),
			Chunks: []SyntheticChunk{
				{Source: "value = inputs['n'] + 1\n", ReleaseAfterMilliseconds: 0},
				{Source: "result = value * 2\n", ReleaseAfterMilliseconds: 150},
			},
			ExpectedOutcome: "success", ExpectedLogicalCalls: 0,
		},
		{
			ID: "unknown_wrapper", Class: "negative_control", Inputs: json.RawMessage(`{}`),
			Chunks: []SyntheticChunk{
				{Source: "def fetch():\n    return time.read('weather')\n", ReleaseAfterMilliseconds: 0},
				{Source: "value = fetch()\n", ReleaseAfterMilliseconds: 150},
				{Source: "result = value\n", ReleaseAfterMilliseconds: 300},
			},
			ExpectedOutcome: "success", ExpectedLogicalCalls: 1,
		},
	}
}

func (fixture SyntheticCase) Validate() error {
	if !identifierPattern.MatchString(fixture.ID) || !validSyntheticClass(fixture.Class) || len(fixture.Chunks) == 0 ||
		!validSyntheticOutcome(fixture.ExpectedOutcome) || fixture.ExpectedLogicalCalls > 1 {
		return errors.New("invalid synthetic case")
	}
	var input any
	if json.Unmarshal(fixture.Inputs, &input) != nil {
		return errors.New("invalid synthetic inputs")
	}
	canonical, err := json.Marshal(input)
	if err != nil || !bytes.Equal(canonical, fixture.Inputs) {
		return errors.New("synthetic inputs must be canonical JSON")
	}
	var total int
	for index, chunk := range fixture.Chunks {
		if chunk.Source == "" || (index == 0 && chunk.ReleaseAfterMilliseconds != 0) ||
			(index > 0 && fixture.Chunks[index-1].ReleaseAfterMilliseconds >= chunk.ReleaseAfterMilliseconds) {
			return errors.New("invalid synthetic source schedule")
		}
		total += len([]byte(chunk.Source))
		if total > 1<<20 {
			return errors.New("synthetic source exceeds one MiB")
		}
	}
	return nil
}

func (fixture SyntheticCase) Source() []byte {
	var joined []byte
	for _, chunk := range fixture.Chunks {
		joined = append(joined, []byte(chunk.Source)...)
	}
	return joined
}

func (fixture SyntheticCase) SourceSHA256() string {
	return fixture.Projection().SourceSHA256
}

func (fixture SyntheticCase) SourceScheduleSHA256() string {
	return fixture.Projection().SourceScheduleSHA256
}

func (fixture SyntheticCase) InputsSHA256() string {
	return fixture.Projection().InputsSHA256
}

func (fixture SyntheticCase) ChunkBodies() [][]byte {
	bodies := make([][]byte, len(fixture.Chunks))
	for index, chunk := range fixture.Chunks {
		bodies[index] = append([]byte(nil), []byte(chunk.Source)...)
	}
	return bodies
}

func (fixture SyntheticCase) Projection() SyntheticCaseProjection {
	chunks := make([]SyntheticChunkProjection, len(fixture.Chunks))
	for index, chunk := range fixture.Chunks {
		chunks[index] = SyntheticChunkProjection{
			Index: uint32(index + 1), SourceSHA256: syntheticDigest([]byte(chunk.Source)),
			ReleaseAfterMilliseconds: chunk.ReleaseAfterMilliseconds,
		}
	}
	schedule, _ := json.Marshal(chunks)
	return SyntheticCaseProjection{
		SchemaVersion: SyntheticCaseProjectionSchemaVersion,
		ID:            fixture.ID, Class: fixture.Class,
		SourceSHA256:         syntheticDigest(fixture.Source()),
		SourceScheduleSHA256: syntheticDigest(append([]byte(SyntheticCaseProjectionSchemaVersion+"\x00"), schedule...)),
		InputsSHA256:         syntheticDigest(fixture.Inputs),
		ExpectedOutcome:      fixture.ExpectedOutcome, ExpectedLogicalCalls: fixture.ExpectedLogicalCalls,
		Chunks: chunks,
	}
}

func EncodeSyntheticCaseProjection(value SyntheticCaseProjection) ([]byte, error) {
	if value.SchemaVersion != SyntheticCaseProjectionSchemaVersion || !identifierPattern.MatchString(value.ID) ||
		!validSyntheticClass(value.Class) || !digestPattern.MatchString(value.SourceSHA256) ||
		!digestPattern.MatchString(value.SourceScheduleSHA256) || !digestPattern.MatchString(value.InputsSHA256) ||
		!validSyntheticOutcome(value.ExpectedOutcome) || value.ExpectedLogicalCalls > 1 || len(value.Chunks) == 0 {
		return nil, errors.New("invalid synthetic case projection")
	}
	for index, chunk := range value.Chunks {
		if chunk.Index != uint32(index+1) || !digestPattern.MatchString(chunk.SourceSHA256) ||
			(index == 0 && chunk.ReleaseAfterMilliseconds != 0) ||
			(index > 0 && value.Chunks[index-1].ReleaseAfterMilliseconds >= chunk.ReleaseAfterMilliseconds) {
			return nil, errors.New("invalid synthetic chunk projection")
		}
	}
	return json.Marshal(value)
}

func syntheticDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validSyntheticOutcome(value string) bool {
	switch value {
	case "runtime_error", "success", "syntax_error":
		return true
	default:
		return false
	}
}

func validSyntheticClass(value string) bool {
	switch value {
	case "adversarial", "control", "negative_control", "positive":
		return true
	default:
		return false
	}
}
