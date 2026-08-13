package streaming

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

type oracleFunc func([]byte) (CompileResult, error)

func (f oracleFunc) Compile(source []byte) (CompileResult, error) {
	return f(source)
}

func result(status CompileStatus, source []byte, suites []ByteRange, preambleEnd int, preambleFrozen bool, rejection string) CompileResult {
	return CompileResult{
		Status:          status,
		CompleteSuites:  suites,
		Preamble:        Preamble{End: preambleEnd, Frozen: preambleFrozen},
		RejectionReason: rejection,
	}
}

func mustStream(t *testing.T, compiler GuestCompiler) *SourceStream {
	t.Helper()
	stream, err := NewSourceStream(compiler)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Begin(); err != nil {
		t.Fatal(err)
	}
	return stream
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func TestSourceStreamAdmitsGuestConfirmedSuiteShapes(t *testing.T) {
	tests := []struct {
		name   string
		chunks [][]byte
		build  func(source []byte, chunks [][]byte) map[string]CompileResult
	}{
		{
			name:   "simple statement",
			chunks: [][]byte{[]byte("value = 1\n")},
			build: func(source []byte, chunks [][]byte) map[string]CompileResult {
				return map[string]CompileResult{string(source): result(CompileComplete, source, []ByteRange{{Start: 0, End: len(source)}}, 0, true, "")}
			},
		},
		{
			name:   "multiline expression",
			chunks: [][]byte{[]byte("value = (1 +"), []byte(" 2)\n")},
			build: func(source []byte, chunks [][]byte) map[string]CompileResult {
				return map[string]CompileResult{
					string(chunks[0]): result(CompileIncomplete, chunks[0], nil, 0, true, ""),
					string(source):    result(CompileComplete, source, []ByteRange{{Start: 0, End: len(source)}}, 0, true, ""),
				}
			},
		},
		{
			name:   "def",
			chunks: [][]byte{[]byte("def answer():\n"), []byte("    return 42\n")},
			build: func(source []byte, chunks [][]byte) map[string]CompileResult {
				return map[string]CompileResult{
					string(chunks[0]): result(CompileIncomplete, chunks[0], nil, 0, true, ""),
					string(source):    result(CompileComplete, source, []ByteRange{{Start: 0, End: len(source)}}, 0, true, ""),
				}
			},
		},
		{
			name:   "decorated def",
			chunks: [][]byte{[]byte("@decorator\n"), []byte("def answer():\n"), []byte("    return 42\n")},
			build: func(source []byte, chunks [][]byte) map[string]CompileResult {
				return map[string]CompileResult{
					string(chunks[0]):                       result(CompileIncomplete, chunks[0], nil, 0, true, ""),
					string(append(chunks[0], chunks[1]...)): result(CompileIncomplete, append(chunks[0], chunks[1]...), nil, 0, true, ""),
					string(source):                          result(CompileComplete, source, []ByteRange{{Start: 0, End: len(source)}}, 0, true, ""),
				}
			},
		},
		{
			name:   "if else",
			chunks: [][]byte{[]byte("if flag:\n"), []byte("    value = 1\n"), []byte("else:\n"), []byte("    value = 2\n")},
			build: func(source []byte, chunks [][]byte) map[string]CompileResult {
				responses := map[string]CompileResult{}
				var prefix []byte
				for _, chunk := range chunks {
					prefix = append(prefix, chunk...)
					responses[string(prefix)] = result(CompileIncomplete, prefix, nil, 0, true, "")
				}
				responses[string(source)] = result(CompileComplete, source, []ByteRange{{Start: 0, End: len(source)}}, 0, true, "")
				return responses
			},
		},
		{
			name:   "try except",
			chunks: [][]byte{[]byte("try:\n"), []byte("    value = 1\n"), []byte("except ValueError:\n"), []byte("    value = 2\n")},
			build: func(source []byte, chunks [][]byte) map[string]CompileResult {
				responses := map[string]CompileResult{}
				var prefix []byte
				for _, chunk := range chunks {
					prefix = append(prefix, chunk...)
					responses[string(prefix)] = result(CompileIncomplete, prefix, nil, 0, true, "")
				}
				responses[string(source)] = result(CompileComplete, source, []ByteRange{{Start: 0, End: len(source)}}, 0, true, "")
				return responses
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var source []byte
			for _, chunk := range test.chunks {
				source = append(source, chunk...)
			}
			responses := test.build(source, test.chunks)
			stream := mustStream(t, oracleFunc(func(current []byte) (CompileResult, error) {
				response, ok := responses[string(current)]
				if !ok {
					return CompileResult{}, fmt.Errorf("unexpected oracle source %q", current)
				}
				return response, nil
			}))
			for _, chunk := range test.chunks {
				if err := stream.Chunk(chunk); err != nil {
					t.Fatal(err)
				}
			}
			seal, err := stream.End()
			if err != nil {
				t.Fatal(err)
			}
			if seal.Digest != digestBytes(source) {
				t.Fatalf("source digest=%q want=%q", seal.Digest, digestBytes(source))
			}
			if len(seal.Suites) != 1 || seal.Suites[0].Range != (ByteRange{Start: 0, End: len(source)}) {
				t.Fatalf("sealed suites=%+v", seal.Suites)
			}
			if seal.Suites[0].Digest != digestBytes(source) {
				t.Fatalf("suite digest=%q want=%q", seal.Suites[0].Digest, digestBytes(source))
			}
		})
	}
}

func TestSourceStreamRecordsStableRangesAndFreezesPreamble(t *testing.T) {
	preamble := []byte("from __future__ import annotations\n")
	body := []byte("value = 1\n")
	full := append(append([]byte(nil), preamble...), body...)
	stream := mustStream(t, oracleFunc(func(source []byte) (CompileResult, error) {
		switch string(source) {
		case string(preamble):
			return result(CompileIncomplete, source, nil, len(preamble), false, ""), nil
		case string(full):
			return result(CompileComplete, source, []ByteRange{{Start: len(preamble), End: len(full)}}, len(preamble), true, ""), nil
		default:
			return CompileResult{}, fmt.Errorf("unexpected source %q", source)
		}
	}))
	if err := stream.Chunk(preamble); err != nil {
		t.Fatal(err)
	}
	if err := stream.Chunk(body); err != nil {
		t.Fatal(err)
	}
	seal, err := stream.End()
	if err != nil {
		t.Fatal(err)
	}
	if seal.Preamble.Range != (ByteRange{Start: 0, End: len(preamble)}) {
		t.Fatalf("preamble range=%+v", seal.Preamble.Range)
	}
	if seal.Preamble.Digest != digestBytes(preamble) {
		t.Fatalf("preamble digest=%q want=%q", seal.Preamble.Digest, digestBytes(preamble))
	}
	if len(seal.Suites) != 1 || seal.Suites[0].Range != (ByteRange{Start: len(preamble), End: len(full)}) {
		t.Fatalf("suites=%+v", seal.Suites)
	}
}

func TestSourceStreamTrustsGuestOracleForSemanticVerdict(t *testing.T) {
	source := []byte("this is not Python, but the Guest oracle says it is a suite\n")
	stream := mustStream(t, oracleFunc(func(current []byte) (CompileResult, error) {
		return result(CompileComplete, current, []ByteRange{{Start: 0, End: len(current)}}, 0, true, ""), nil
	}))
	if err := stream.Chunk(source); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.End(); err != nil {
		t.Fatalf("host parser must not override Guest oracle: %v", err)
	}
}

func TestSourceStreamRejectsGuestInvalidSuffixesIncludingLateAndDynamicOperations(t *testing.T) {
	cases := []struct {
		name      string
		suffix    string
		rejection string
	}{
		{name: "late import", suffix: "import json\n", rejection: "late import"},
		{name: "dynamic import", suffix: "module = __import__('json')\n", rejection: "dynamic import"},
		{name: "eval", suffix: "value = eval('1')\n", rejection: "eval is not admitted"},
		{name: "exec", suffix: "exec('value = 1')\n", rejection: "exec is not admitted"},
		{name: "invalid suffix", suffix: "value =\n", rejection: "invalid Python suffix"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			prefix := []byte("value = 1\n")
			stream := mustStream(t, oracleFunc(func(current []byte) (CompileResult, error) {
				if string(current) == string(prefix) {
					return result(CompileComplete, current, []ByteRange{{Start: 0, End: len(current)}}, 0, true, ""), nil
				}
				return result(CompileInvalid, current, nil, 0, true, test.rejection), nil
			}))
			if err := stream.Chunk(prefix); err != nil {
				t.Fatal(err)
			}
			if err := stream.Chunk([]byte(test.suffix)); !errors.Is(err, ErrGuestRejected) {
				t.Fatalf("error=%v, want ErrGuestRejected", err)
			}
			if _, err := stream.End(); !errors.Is(err, ErrStreamFailed) {
				t.Fatalf("end error=%v, want ErrStreamFailed", err)
			}
		})
	}
}

func TestSourceStreamRejectsPreambleMutationAfterFreeze(t *testing.T) {
	prefix := []byte("value = 1\n")
	stream := mustStream(t, oracleFunc(func(source []byte) (CompileResult, error) {
		if string(source) == string(prefix) {
			return result(CompileComplete, source, []ByteRange{{Start: 0, End: len(source)}}, 0, true, ""), nil
		}
		return result(CompileComplete, source, []ByteRange{{Start: 0, End: len(source)}}, 1, true, ""), nil
	}))
	if err := stream.Chunk(prefix); err != nil {
		t.Fatal(err)
	}
	if err := stream.Chunk([]byte("# suffix\n")); !errors.Is(err, ErrPreambleChanged) {
		t.Fatalf("error=%v, want ErrPreambleChanged", err)
	}
}

func TestSourceStreamRejectsDuplicateEndAndCancellation(t *testing.T) {
	source := []byte("value = 1\n")
	stream := mustStream(t, oracleFunc(func(current []byte) (CompileResult, error) {
		return result(CompileComplete, current, []ByteRange{{Start: 0, End: len(current)}}, 0, true, ""), nil
	}))
	if err := stream.Chunk(source); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.End(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.End(); !errors.Is(err, ErrStreamEnded) {
		t.Fatalf("duplicate end error=%v, want ErrStreamEnded", err)
	}

	cancelled := mustStream(t, oracleFunc(func(current []byte) (CompileResult, error) {
		return result(CompileIncomplete, current, nil, 0, true, ""), nil
	}))
	if err := cancelled.Chunk([]byte("value =")); err != nil {
		t.Fatal(err)
	}
	if err := cancelled.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := cancelled.Cancel(); !errors.Is(err, ErrStreamCancelled) {
		t.Fatalf("duplicate cancel error=%v, want ErrStreamCancelled", err)
	}
	if err := cancelled.Chunk([]byte(" 1\n")); !errors.Is(err, ErrStreamCancelled) {
		t.Fatalf("chunk after cancel error=%v, want ErrStreamCancelled", err)
	}
	if _, err := cancelled.End(); !errors.Is(err, ErrStreamCancelled) {
		t.Fatalf("end after cancel error=%v, want ErrStreamCancelled", err)
	}
}
