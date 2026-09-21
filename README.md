# Pysolate

A small Go runtime for agent-authored Python in isolated CPython/WASI Guests.

The execution core is a direct evolution of Pysolate Spine (`1abc99a`, MIT), not a wrapper around the former runtime. The old APIs, experiments and evidence remain in Git at `2be7488b`.

See the [`docs/` index](docs/README.md) for supported workflows, performance
evidence, the current scheduling study, and a summary of recent deliveries.

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
- **Tools:** Host providers discover canonical tools, metadata and Go call implementations; a small Python shim exposes only that catalog.
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

`PYSOLATE_BUILD_INPUTS` selects an existing CPython/WASI cache. The link script also accepts `PYSOLATE_NUMPY_NATIVE_ROOT` and `PYSOLATE_NUMPY_PACKAGE_ROOT` to reuse NumPy inputs. The supported `agent-core` artifact contains CPython, NumPy and one pinned pure-Python package, PyYAML. Changing user Python or Host tool catalogs does not require rebuilding the Guest; bootstrap or pure-Python package changes need only a VFS repack. The build separates legacy RandomState symbols from Generator symbols because their integer ABIs differ in a static WASI link.

```sh
printf 'result = inputs["value"] + 1\n' | go run ./cmd/pysolate -inputs '{"value":41}'
go run ./cmd/pysolate -source examples/numpy.py
```

The CLI grants only a pure `echo` demonstration tool. Applications supply their own tools through the Go API. Python stdout is forwarded to stderr; the final JSON value is printed to stdout.

For presentation-ready end-to-end examples, use the scripts under [`demos/`](demos/README.md):

```sh
./demos/01-basic-python.sh
./demos/02-namespaced-tools.sh
./demos/03-workspace-edit.sh
./demos/04-hot-service.sh
./demos/05-corpus-replay.sh
# Measurement-oriented demos:
./demos/06-semantic-phases.sh
./demos/07-live-io.sh
# Or run all demos:
./demos/run-all.sh
```

```go
tools := pysolate.Manifest{
    "market/get-price": {
        PythonPath: "stock.getprice",
        Call: func(ctx context.Context, args json.RawMessage) (any, error) {
            return 21, nil // Application-owned authorization and argument validation go here.
        },
        AllowEarlyRead: true,
    },
}
runner, err := pysolate.New(ctx, wasm, tools)
if err != nil { return err }
defer runner.Close(context.Background())
out, err := runner.Run(ctx, `result = stock.getprice(item="book") * inputs["quantity"]`, map[string]int{"quantity": 2})
```

A Runner compiles once and may serve independent Runs. Each Run owns its Python state and tool workers. Tools must be concurrency-safe and honor their context. Call `Close` after all Runs have returned.

### Dynamic Host tools and MCP adapters

`ToolSpec` separates the canonical Host identity from its `PythonPath`, and can carry a description, JSON input schema and discovery annotations. Provider-native identities remain stable while the generated Python surface stays natural:

```python
price = stock.getprice(symbol="AAPL")
contents = filesystem.read_file(path="notes.txt")
```

Every generated function still dispatches through the same narrow Host call ABI. Only canonical identity, Python path and the early-read bit enter the Guest; descriptions and schemas stay on the Host so an Agent can inspect them before generating a one-shot script. There is no runtime `describe()` round trip. Top-level paths such as `price(...)` remain supported. Metadata is descriptive: MCP `readOnlyHint` does not enable early execution. The Host must still set `AllowEarlyRead` explicitly.

`ToolProvider` and `ManifestFromProviders` merge discovered catalogs while rejecting duplicate canonical identities and colliding Python paths. `mcpadapter.Provider` converts an already connected MCP client into separately configured canonical and Python namespaces. MCP transport, authentication, session lifecycle, schema enforcement and credentials stay on the Host and are never packaged into Guest Python.

### Private workspaces

`runtime/workspace` creates a bounded private filesystem and grants an exclusive writer lease. `CreateFromDirectory` copies an ordinary Host working tree once; `.git` metadata remains Host-owned and source files are never modified in place. `RunWorkspace` mounts that lease only at `/workspace` and starts user code there with `/workspace` first on `sys.path`, enabling relative files and local-module imports. Ordinary `Run` still has no Host filesystem authority. The rooted adapter rejects traversal, symlinks, hard links, devices, filesystem-boundary crossings and writes beyond Host-selected file/byte/depth limits.

Workspace-prepared images need the same WASI preopen shape captured at initialization. Use `NewPreparedWorkspace` or Linux `NewPreparedWorkspaceCOW`, then execute with `RunWorkspace`. Ordinary `NewPrepared` runners reject workspace attachment instead of restoring an incompatible image. Workspace state can continue across disposable Guests, but publication back to a real project remains a separate Host operation. Writable workspaces are not part of `RunRecorded` durable replay.

`Lease.Snapshot` produces a path-independent revision and a bounded file manifest; `workspace.Diff` reports deterministic added, modified and deleted metadata without embedding file contents. Python errors preserve private changes for Host inspection until cleanup. See [the workspace lifecycle and executable acceptance](docs/workspace.md), or run:

```sh
go run ./examples/workspace-edit -guest dist/pysolate.wasm
go run ./examples/agent-core-usecases -guest dist/pysolate.wasm
```

See [the qualified common-usecase boundary](docs/common-usecases.md) for repository/config editing, structured-data processing, Host-enriched scripts and deliberate exclusions.

### Deterministic corpus replay

`cmd/pysolate-corpus` executes frozen programs against exact Host-tool fixtures through the real Guest. The stdlib-only importer currently adapts pinned HumanEval canonical programs and BFCL ground-truth calls without putting an LLM in the measurement loop. See [the corpus schema, adapter semantics and reproducible commands](docs/corpus-replay.md), or run `./demos/05-corpus-replay.sh`.

### Long-running service

`cmd/pysolate-server` keeps the compiled module and prepared clean images hot behind a bounded local HTTP API. It supports stateless Runs plus create/run/snapshot/read/destroy workspace lifecycles. Saturated execution capacity returns HTTP 429 immediately; the service does not grow an implicit queue.

```sh
go run ./cmd/pysolate-server -guest dist/pysolate.wasm -listen 127.0.0.1:8080 -max-active 4
```

The standalone binary intentionally grants no Host tools. An embedding application passes its trusted provider/MCP-derived `Manifest` to `service.New`; changing that catalog rebuilds service preparation but not the Guest artifact. See [the service API, trust boundary and loopback benchmark](docs/service.md).

## Optional execution modes

- `RunPLM` prepares only explicitly allowed stable, read-only snapshots. Values and errors are delivered at their original Python calls. Failed tools are not automatically retried.
- `RunPrefix` accepts append-only source chunks, prepares eligible reads and executes the completed source in the same Guest. It shares the Run's existing future table.
- `NewPrepared` copies a clean initialized image into each Guest.
- `NewPreparedCOW` uses sealed Linux memfd/private mappings. It does not fall back to a different backend or restore an active stack.
- `NewPreparedWorkspace` and `NewPreparedWorkspaceCOW` capture the mount shape required by private workspaces.

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

No Host directories, environment, network sockets or subprocess authority are ambient in the Guest. Guest imports use the locked [`agent-core` artifact profile](docs/artifact-profiles.md), including NumPy. External services, credentials and MCP connections stay behind Host tools. A Host can separately grant one runtime-owned private workspace at `/workspace`; arbitrary Host paths are never accepted as Guest mounts.

Current engineering defaults are 512 MiB maximum linear memory, 1 MiB per request/result and per stdout/stderr buffer, 1024 tool issues per attempt, and 64 outstanding early reads. Use context deadlines for elapsed-time bounds. The durable Store retains at most 64 MiB of logical payload per Run; SQLite/WAL physical overhead is separate.

## Read the code

```text
runner.go, bridge.go       Guest lifecycle and Host calls
tool_provider.go           provider discovery and normalized tool metadata
mcpadapter/                narrow adapter for connected MCP clients
future.go, prefix.go       Run-owned early reads and source streaming
prepared.go                clean-image orchestration
internal/cowmem/           Linux private-memory backend and platform stub
recording.go               deterministic attempts and journal stops
durable/                   SQLite Store and recovery driver
runtime/workspace/         bounded rooted private filesystems
integration/               public-API tests against the real Guest
guest/                     CPython bridge, execution and small AST passes
cmd/pysolate/              CLI
cmd/pysolate-server/       bounded local HTTP service
cmd/pysolate-service-bench/ real loopback hot-path benchmark
cmd/pysolate-corpus/        deterministic real-Guest corpus runner
corpus/                     strict corpus schema and replay provider
```

## Check

```sh
make check
# Or run explicitly after building the artifact:
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" go test ./... -count=1
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" go test -race ./... -count=1
PYTHONPATH=guest python3 -m unittest discover -s guest
python3 -m unittest discover -s tools -p 'test_*.py'
python3 tools/probe-guest-modules.py --guest dist/pysolate.wasm
go vet ./...
```

Real-Guest tests fail when the artifact is missing. Linux COW tests require Linux. This implementation intentionally does not preserve the old HTTP/CLI protocols or their experimental execution paths.

## Performance and bounded execution

See [the measured results and trade-offs](docs/performance-results.md) for seeded reconstruction, native cache, Guest startup, PLM/prefix and admission. The default paths remain explicit; improvements are not a claim that every workload or cold cache is faster.

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

## Bounded durable execution

`durable.NewExecutor(controller, durable.Limits{MaxRunning: 2, MaxResident: 8, MaxInflightTools: 4, MaxQueued: 16})` adds bounded admission over the narrow `AttemptController` lifecycle interface. `MaxRunning` limits Guests executing Python, `MaxResident` also counts live Guests waiting in opted-in Host I/O, and `MaxInflightTools` separately bounds those external calls. `MaxActive` remains a compatibility alias for `MaxRunning`; when `MaxResident` is omitted it defaults to the running limit and preserves the old memory bound.

Prefer `Admit(ctx, runID)` and `Attempt.Result(ctx)`: completion and durable parking are explicit `AdvanceResult` states, while execution/control failures remain errors. Repeated admission of an already queued or resident Run returns `ErrBusy`, and a full admission queue returns `ErrQueueFull`. `Submit`/`Wait` remain compatibility aliases; `Wait` converts a parked result back to `ErrParked`/`ParkError`.

Host tools are `durable.Inline` by default and keep the running slot. A tool explicitly declared with `Scheduling: durable.ExternalIO` lets the Executor reuse its running slot for another resident attempt while the live Guest and Wasm stack wait. When the call returns, its continuation enters the ready queue and reacquires a running slot before Python continues; ready continuations are preferred with a bounded burst so new admissions cannot starve. This policy is Host-local and is not part of the persisted Tool semantic snapshot. Use it only for cooperative, context-aware external I/O, not local CPU work. `Executor.Stats()` reports running, resident, in-flight Tool, live-waiting, ready and queued counts.

A durable park is different: the Guest is destroyed and the logical Run can outlive it without resident capacity. Persist a decision through `Runner.Decide`, then explicitly `Admit` the same Run again to reconstruct and replay. Admission and scheduling stay outside `Runner`; the Runner continues to own journals, replay, effects, and Guest construction.

Cancelling the admission context cancels that attempt, leaving durable state resumable. `Executor.Cancel` uses durable cancellation first. `Executor.Close` stops new admission and drains existing jobs; a timeout does not destroy active resources, and Close can be retried. The caller still owns Runner and Store and closes them after drain. There is no background deadline scan, persistent queue, automatic retry or implicit resubmission after restart. The staged semantic-scheduling study and fixed workloads are documented in [`docs/semantic-scheduling-study.md`](docs/semantic-scheduling-study.md).
