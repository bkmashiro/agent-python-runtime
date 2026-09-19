# Pysolate

A small Go runtime for agent-authored Python in isolated CPython/WASI Guests.

The execution core is a direct evolution of Pysolate Spine (`1abc99a`, MIT), not a wrapper around the former runtime. The old APIs, experiments and evidence remain in Git at `2be7488b`.

## Execution model

```text
Runner owns compiled code and an optional clean image
  -> create a private Guest for one Run
  -> execute Python, calling explicitly granted Host tools
  -> return the value or Python error
  -> close the Guest and finish its tool workers
```

There are four responsibilities:

- **Runner:** ordinary execution, PLM and append-only source input share one lifecycle.
- **Tools:** a name maps to a Go function and an explicit early-read declaration.
- **Image:** optional full-copy or Linux private COW memory, captured before user execution.
- **Store:** optional SQLite history for deterministic replay and durable waits.

No Broker/Plan hierarchy, plugin catalog, receipts, source certificates, workspace transaction framework, generic workflow engine, native backend or cross-run result cache is required.

## Run

Requires Go 1.25+ and a built `dist/pysolate.wasm`. On Linux x86_64 with a C compiler, make, Python 3.11+, curl, tar and unzip:

```sh
python3 tools/setup-build-inputs.py  # one-time pinned CPython/WASI build
python3 tools/setup-numpy.py         # pinned static NumPy build
bash build-guest.sh                  # relink this project's Guest
```

`PYSOLATE_BUILD_INPUTS` selects an existing CPython/WASI cache. The link script also accepts `PYSOLATE_NUMPY_NATIVE_ROOT` and `PYSOLATE_NUMPY_PACKAGE_ROOT` to reuse NumPy inputs. Changing user Python does not require rebuilding the Guest. The build separates legacy RandomState symbols from Generator symbols because their integer ABIs differ in a static WASI link.

```sh
printf 'result = inputs["value"] + 1\n' | go run ./cmd/pysolate -inputs '{"value":41}'
go run ./cmd/pysolate -source examples/numpy.py
```

The CLI grants only a pure `echo` demonstration tool. Applications supply their own tools through the Go API. Python stdout is forwarded to stderr; the final JSON value is printed to stdout.

```go
tools := pysolate.Manifest{
    "price": {
        Call: func(ctx context.Context, args json.RawMessage) (any, error) {
            return 21, nil // Application-owned authorization and argument validation go here.
        },
        AllowEarlyRead: true,
    },
}
runner, err := pysolate.New(ctx, wasm, tools)
if err != nil { return err }
defer runner.Close(context.Background())
out, err := runner.Run(ctx, `result = price(item="book") * inputs["quantity"]`, map[string]int{"quantity": 2})
```

A Runner compiles once and may serve independent Runs. Each Run owns its Python state and tool workers. Tools must be concurrency-safe and honor their context. Call `Close` after all Runs have returned.

## Optional execution modes

- `RunPLM` prepares only explicitly allowed stable, read-only snapshots. Values and errors are delivered at their original Python calls. Failed tools are not automatically retried.
- `RunPrefix` accepts append-only source chunks, prepares eligible reads and executes the completed source in the same Guest. It shares the Run's existing future table.
- `NewPrepared` copies a clean initialized image into each Guest.
- `NewPreparedCOW` uses sealed Linux memfd/private mappings. It does not fall back to a different backend or restore an active stack.

```sh
go run ./cmd/pysolate -source examples/echo.py -mode plm
go run ./cmd/pysolate -source examples/echo.py -mode prefix -prepared copy
# Linux:
go run ./cmd/pysolate -source examples/echo.py -mode prefix -prepared cow
```

The CLI's prefix mode replays file lines. An embedding application can feed a real source stream.

## Durable runs

`durable/` records source, inputs, seed, artifact identity, declared tool versions, call outcomes and waits. A restart creates a fresh Guest, replays saved outcomes and executes the unfinished suffix. Intent is committed before external dispatch; the outcome is committed before delivery to Python.

Unresolved external operations follow the Host's declared safe-retry, idempotent, lookup, manual or wait policy. Nothing infers those semantics from a tool name or source code. The optional journal can stop an attempt in a way Python cannot catch.

`RunRecorded` uses fresh Guests with per-attempt seeded WASI randomness and logical clocks. PLM is excluded. An explicitly seeded prepared image can restore the matching deterministic initialization state; ordinary unseeded images are rejected. `PythonError` is a completed Python failure; timeout, storage and other infrastructure errors remain distinct Go errors.

The new SQLite format does not migrate old runtime databases. Cancellation stops future progress and signals the local attempt; it cannot roll back an external operation already started elsewhere.

## Boundaries

No Host directories, environment, network sockets or subprocess authority are mounted into the Guest. Guest imports are the libraries packaged in the artifact, including NumPy. Tools are the external-I/O boundary; the Host is responsible for their authorization and argument validation.

Current engineering defaults are 512 MiB maximum linear memory, 1 MiB per request/result and per stdout/stderr buffer, 1024 tool issues per attempt, and 64 outstanding early reads. Use context deadlines for elapsed-time bounds. The durable Store retains at most 64 MiB of logical payload per Run; SQLite/WAL physical overhead is separate.

## Read the code

```text
runner.go, bridge.go       Guest lifecycle and Host calls
future.go, prefix.go       Run-owned early reads and source streaming
prepared.go, cow*.go       clean images and private memory
recording.go               deterministic attempts and journal stops
durable/                   SQLite Store and recovery driver
guest/                     CPython bridge, execution and small AST passes
cmd/pysolate/              CLI
```

## Check

```sh
make check
# Or run explicitly after building the artifact:
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" go test ./... -count=1
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" go test -race ./... -count=1
PYTHONPATH=guest python3 -m unittest discover -s guest
go vet ./...
```

Real-Guest tests fail when the artifact is missing. Linux COW tests require Linux. This implementation intentionally does not preserve the old HTTP/CLI protocols or their experimental execution paths.

## Verified scope

The new Guest was built and run, including matrix operations and separate NumPy Generator/RandomState integer-ABI regressions. `make check` passes with the real artifact. Targeted PLM/prefix and Store/journal race tests pass; a whole durable race run exceeded its initial 150-second execution budget and was not counted as a pass.

Linux tests exercise actual private COW mappings and Guest isolation. Recovery was verified after closing/reopening SQLite, after killing the process following an external fixture commit, and after hard-stopping/restarting a 2-vCPU/2-GiB Linux VM at that same window. The recovered fixture recorded one read, two write requests and one idempotent effect. This is not a physical-host power-loss guarantee.

Old APIs and databases are intentionally incompatible. The core and Guest contain 2,883 physical source lines (including comments/blank lines, excluding tests, CLI, build tooling and dependencies). Build tooling is separate from the execution core.

## Prepared deterministic attempts

`NewPreparedRecorded(ctx, wasm, tools, seed)` and its Linux COW variant
`NewPreparedRecordedCOW` capture clean Python memory **and** the entropy stream
position and logical clocks consumed during initialization. `RunRecorded` requires
that exact seed. Different Runs can share the image when they explicitly use the
same seed; there is no per-seed cache. A mismatched seed is rejected, never silently
replayed with different randomness. PLM remains excluded from recorded execution.

For durable use, pass `durable.Preparation{Seed: "seed", COW: true}` as the optional
last argument to `durable.NewRunner`. The default is still fresh. This changes no
stored schema and can resume history made by a fresh Runner with the same seed.
The image has startup and retained-memory costs; account for them, not just warm
resume time. See `docs/performance-results.md` for measurements.

## Optional compilation cache

The CLI accepts `-cache DIRECTORY` to reuse wazero native compilation across process restarts. It is off by default. Use a private, trusted directory; do not accept a cache path or files controlled by submitted Python. Compilation-cache hits do not share Python state or Host tool authority.

Embedders can create a `wazero.CompilationCache` (in memory or with a directory) and pass `pysolate.WithCompilationCache(ctx, cache)` to any constructor, including the durable constructor. The caller closes the cache after all using Runners. No files are created when this option is absent.
