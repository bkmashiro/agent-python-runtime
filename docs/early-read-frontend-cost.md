# Early-read front-end cost: phase investigation

This investigation changes only an opt-in diagnostic test and documentation.
It does not implement caching, pre-import modules into production images, or
change `Output.Transformed`.

## Executed path

At source base `87bd0f34`, `guest/bootstrap.py:execute` handles early reads by:

1. importing `ast` and `plm.transform`;
2. calling `transform(source, manifest)`;
3. calling `ast.unparse(tree)` when helpers were generated;
4. compiling the AST and executing it.

The unparsed string is returned as `transformed`. It is not the executable input:
`compile` receives the AST. `guest/plm.py:rewrite` also transforms an eligible
single tool assignment, even though it has no second call to overlap.

`prepared.go:prepare` captures a freshly initialized Guest. Its baseline has not
run an early-read request. Running such a request once on a prepared Runner does
not warm imports in the next request: the next Guest restores the clean image.
The diagnostic recordings observe `ast`, `plm` and the formatter dependencies
loading again inside each fresh Guest.

## Measurement

The opt-in `TestEarlyReadFrontendProfile` runs three cases through the same
prepared Runner, rotating their order:

- ordinary API call: `result=read(value=42)`;
- early-read API call: the same source and Host tool;
- an ordinary-Run diagnostic wrapper that times front-end stages internally.

The diagnostic wrapper uses local stand-ins only to check that the compiled AST
still returns 42. It makes no Host tool call and is **not** a replacement for the
actual early-read path. Its six transformations each start from the same source
text, producing a fresh tree. Pass two measures behavior after imports have
already happened in that Guest; it is not a production cross-request cache.

Three independent Linux processes each made 36 executions, excluding their first
three as warm-up. Each measured case therefore has 11 fresh Guests per process.
The Host callback has zero synthetic delay. Raw sources, inputs, outputs, tool
calls, raw Guest I/O and phase observations remain in private local recordings.
The [reviewed summary](performance-data/early-read-contention/frontend-cost.json)
retains per-process medians.

Environment: Linux/x86_64, COW data-image, `GOMAXPROCS=2`, affinity CPU IDs 0 and 36.
This follow-up verified those are two SMT threads on the same physical core.
An inherited cgroup memory limit of 2,147,483,648 bytes was read on the compute
node. Do not describe this as two independent physical cores.

## Results

Ranges below retain the three independent process medians:

| Observation | Time |
| --- | --- |
| Ordinary complete Run | 2.02–2.03 ms |
| Early-read complete Run | 18.59–18.79 ms |
| First `import ast` | 3.45–3.49 ms |
| First `import plm` | 0.87–0.89 ms |
| First transform, including parse/analysis/rewrite | 1.30–1.32 ms |
| First `ast.unparse` | 10.44–10.46 ms |
| Compile transformed AST | 0.30–0.31 ms |
| Second `ast.unparse` in the same diagnostic Guest | 0.44–0.45 ms |

The first `ast.unparse` adds `_ast_unparse` and `contextlib` to `sys.modules`.
Later passes add neither module. Transform and compile times remain close to
their first-pass values. This makes first-use formatter work and module loading
the leading candidates, ahead of parsing/rewriting or compilation for this tiny
source. A direct pre-import counterfactual has not been run, so the complete
first-versus-second unparse difference is not claimed as import time alone.

The isolated API delta is about 16.6 ms. **This does not exactly decompose the
previous mixed-load observation of roughly 30 ms.** The previous experiment had
paced arrivals, multiple live requests and 50 ms backend waits. Do not pool the
cohorts or present within-Guest warm measurements as a measured runtime speedup.

## CPU profile and independent Go-phase check

The existing `pysolate-bench` ran 300 requests per arm, with one zero-delay tool,
concurrency one, COW data-image and measured-window CPU profiling. Its generated
source is slightly different from the diagnostic source, so these numbers are a
corroborating check, not values to subtract from the diagnostic medians.

Mean per-request Go phases:

- Guest creation: ordinary 1.082 ms, early-read 1.007 ms;
- Guest execution: ordinary 1.159 ms, early-read 18.842 ms;
- Guest close: ordinary 0.115 ms, early-read 0.211 ms.

The extra work is overwhelmingly in execution. Go CPU samples for early reads
include 57.98% in `runtime._ExternalCode`, with further time in wazero call
machinery. This profile does not resolve most generated Wasm/CPython functions.
It supports the location of the cost but does not identify a Python C function
or independently prove a particular import dominates. The internal Guest timing
and observed module changes supply the finer evidence.

## Optimization assessment — not implemented

The first candidate worth testing is initialization reuse for trusted front-end
modules in a clean prepared image. The baseline would need to preserve fresh
user state, seeded/recorded behavior, Guest globals/tables and resource ownership.
Its startup time, retained memory, dirty-page behavior and ordinary short-script
cost must be measured before deciding whether it belongs in any default path.

`tools/precompile-stdlib.py` already selects `ast`, `plm`, `_ast_unparse` and
`contextlib`. Bytecode selection in the build script does not initialize those
modules in every restored Guest. This investigation has not separately audited
whether each module loaded a valid `.pyc` in the tested artifact.

A separate candidate is avoiding unnecessary display rendering when callers do
not need it. That requires an explicit behavior/API decision: the current path
returns `Output.Transformed`, so simply removing `ast.unparse` would change an
observable result. No such change was made.

A source/AST cache is lower priority for this small program. Parsing/analysis and
compilation are a much smaller part of the observed cost, while a cross-request
cache introduces identity, authority and memory-bounding requirements. Larger
programs may have a different profile and were not measured here.

## Reproduce the diagnostic

Use a new private path each time. Linux selects `cow-image`; macOS can use `copy`
for correctness checks but its timings are a separate environment.

```sh
PYSOLATE_FRONTEND_PROFILE=/private/new-front-end.jsonl \
PYSOLATE_FRONTEND_PREPARE=cow-image \
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" \
  go test ./cmd/pysolate-contention-bench \
  -run '^TestEarlyReadFrontendProfile$' -count=1 -v
```

The normal test suite skips this opt-in probe. A copy-mode run under `-race`
passed separately; its timings are not included in the performance results.

Existing Go profiler invocation, one fresh process per work variant:

```sh
go run ./cmd/pysolate-bench -guest dist/pysolate.wasm \
  -mode cow -cow-data-image -work early-reads -calls 1 -delay 0 \
  -n 300 -concurrency 1 \
  -measured-cpuprofile /private/early.cpu -phases /private/early.phases.json
# Repeat with -work tools and different output paths.
```

Keep the command's full stdout as well. These stock benchmark/profile rows are
aggregate diagnostics, not a full execution-replay archive. The front-end test
records its individual diagnostic and actual API executions separately.
