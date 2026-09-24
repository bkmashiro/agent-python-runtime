package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
)

// Diagnostic only: an ordinary Run measures Python frontend stages explicitly.
// Repeated stages share one diagnostic Guest, not memory across user runs.
const frontendProbe = `import time, sys
clock=time.perf_counter_ns
modules_before=set(sys.modules)
t=clock()
import ast
ast_ns=clock()-t
ast_modules=sorted(set(sys.modules)-modules_before)
modules_before=set(sys.modules)
t=clock()
from plm import transform
plm_ns=clock()-t
plm_modules=sorted(set(sys.modules)-modules_before)
source="result=read(value=42)"
manifest=[{"name":"read","python_path":"read","allow_early_read":True}]
samples=[]
for i in range(6):
    t=clock()
    tree, helpers=transform(source,manifest)
    transform_ns=clock()-t
    modules_before=set(sys.modules)
    t=clock()
    rendered=ast.unparse(tree)
    unparse_ns=clock()-t
    unparse_modules=sorted(set(sys.modules)-modules_before)
    t=clock()
    compiled=compile(tree,"<pysolate>","exec")
    compile_ns=clock()-t
    samples.append({"transform_ns":transform_ns,"unparse_ns":unparse_ns,"compile_ns":compile_ns,"unparse_imports":unparse_modules})
# Check the generated code with local diagnostic helpers. This does not claim
# to measure actual early dispatch; the separate early arm uses the real API.
scope={"read":lambda **args:args["value"]}
scope[helpers[0]]=lambda thunk:thunk()
scope[helpers[1]]=lambda handle,name,**args:args["value"]
exec(compiled,scope)
result={"answer":scope["result"],"import_ast_ns":ast_ns,"import_plm_ns":plm_ns,"ast_imports":ast_modules,"plm_imports":plm_modules,"samples":samples,"rendered":rendered}
`

func TestEarlyReadFrontendProfile(t *testing.T) {
	path := os.Getenv("PYSOLATE_FRONTEND_PROFILE")
	if path == "" {
		t.Skip("set PYSOLATE_FRONTEND_PROFILE to a new private JSONL path")
	}
	wasm, err := os.ReadFile(os.Getenv("PYSOLATE_GUEST"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	trace := newTrace(f, nil)
	mode := os.Getenv("PYSOLATE_FRONTEND_PREPARE")
	trace.emit(map[string]any{"kind": "header", "source": frontendProbe, "prepare": mode, "guest_sha256": fmt.Sprintf("%x", sha256.Sum256(wasm)), "note": "diagnostic ordinary-Run wrapper; warm samples only within one Guest; no production behavior changed"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	type observation struct {
		AtNS  int64           `json:"at_ns"`
		Args  json.RawMessage `json:"args"`
		Value int             `json:"value"`
	}
	type captureKey struct{}
	manifest := pysolate.Manifest{"read": {AllowEarlyRead: true, Call: func(ctx context.Context, args json.RawMessage) (any, error) {
		var a struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if out, ok := ctx.Value(captureKey{}).(*[]observation); ok {
			*out = append(*out, observation{time.Since(trace.origin).Nanoseconds(), append(json.RawMessage(nil), args...), a.Value})
		}
		return a.Value, nil
	}}}
	var runner *pysolate.Runner
	if mode == "cow-image" {
		runner, err = pysolate.NewPreparedCOW(ctx, wasm, manifest, pysolate.COWOptions{DataImage: true})
	} else if mode == "copy" || mode == "" {
		runner, err = pysolate.NewPrepared(ctx, wasm, manifest)
	} else {
		t.Fatalf("unsupported diagnostic preparation %q", mode)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := runner.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 12; i++ {
		// Rotate order, preserving the first iteration as an explicit warm-up.
		names := []string{"ordinary", "early", "stages"}
		for j := 0; j < len(names); j++ {
			name := names[(i+j)%len(names)]
			source := "result=read(value=42)"
			if name == "stages" {
				source = frontendProbe
			}
			var calls []observation
			callCtx := context.WithValue(ctx, captureKey{}, &calls)
			callCtx, raw := perfdiag.WithGuestIO(callCtx)
			collector := perfdiag.NewCollector()
			callCtx = perfdiag.WithCollector(callCtx, collector)
			start := time.Now()
			startNS := start.Sub(trace.origin).Nanoseconds()
			var output pysolate.Output
			if name == "early" {
				output, err = runner.RunWithEarlyReads(callCtx, source, nil)
			} else {
				output, err = runner.Run(callCtx, source, nil)
			}
			elapsed := time.Since(start).Nanoseconds()
			errorText := ""
			if err != nil {
				errorText = err.Error()
			}
			trace.emit(map[string]any{"kind": "execution", "case": name, "iteration": i, "warmup": i == 0, "source": source, "inputs": nil, "start_ns": startNS, "elapsed_ns": elapsed, "calls": calls, "output": output, "raw_io": raw, "error": errorText, "phases": collector.Snapshot()})
			if err != nil {
				t.Fatal(err)
			}
			if name == "stages" {
				var result struct {
					Answer  int   `json:"answer"`
					Samples []any `json:"samples"`
				}
				if err := json.Unmarshal(output.Value, &result); err != nil || result.Answer != 42 || len(result.Samples) != 6 || len(calls) != 0 {
					t.Fatalf("invalid diagnostic result: %s %v", output.Value, err)
				}
			} else if string(output.Value) != "42" || len(calls) != 1 {
				t.Fatalf("invalid real API result: %+v %v", output, calls)
			}
		}
	}
	trace.emit(map[string]any{"kind": "complete", "executions": 36, "measured_executions": 33})
	if err := trace.failure(); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}
