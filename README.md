# Pysolate

**Safe, fast Python execution for AI agents, with controlled tools and recoverable runs.**

Let an agent write ordinary Python to process files and call your APIs. Pysolate
runs each script in a fresh isolated environment. Your application grants its
tools and workspace, while keeping credentials and the rest of the machine out
of reach. Implemented in Go with CPython and WebAssembly/WASI.

## The product in one minute

- **Isolation and explicit tool permissions.** Python has no ambient Host
  network, filesystem, environment, credentials, or subprocess access. It can
  call only the capabilities the Host grants.
- **High-density warm execution.** A long-lived Runner reuses compiled code and
  prepared clean state while bounded admission limits live Guests and external
  waits. Tasks waiting on approved external I/O can release a running slot so
  other tasks can progress, while retaining their own Python state.
- **Safe early reads.** A script can overlap independent, explicitly approved
  read-only calls. Pysolate does not run arbitrary writes ahead of their Python
  call sites, and the early-read path is optional.
- **Deterministic replay and crash recovery.** Recorded runs can reproduce
  controlled Python inputs and reuse completed Tool outcomes; durable runs keep
  journal state so a process restart can resume without duplicating an
  idempotent effect. A Host-owned workspace is a separate lifecycle: it can
  carry files from one disposable Guest to the next, but it is not replay.

The execution core directly evolves Pysolate Spine (`1abc99a`, MIT). It is not a
wrapper around the former runtime. The old APIs, experiments and evidence remain
in Git at `2be7488b`.

## A Python + Tool demo

The Python is an ordinary one-shot script. `inputs` and the `market` namespace
are injected for this Run; the Guest receives no credentials or network access:

```python
# examples/python-tools/strategy.py
symbols = inputs["symbols"]
quantities = inputs["quantities"]

quotes = [market.get_price(symbol=symbol) for symbol in symbols]
leader = max(quotes, key=lambda quote: quote["price"])
portfolio_value = sum(
    quote["price"] * quantities[quote["symbol"]]
    for quote in quotes
)

result = {
    "leader": leader,
    "portfolio_value": round(portfolio_value, 2),
    "quotes": quotes,
}
```

The Host grants exactly one capability. Its canonical identity remains stable,
while `PythonPath` controls the natural API presented to generated code:

```go
prices := map[string]float64{"AAPL": 225.50, "MSFT": 418.20, "NVDA": 176.40}
manifest := pysolate.Manifest{
    "market/get-price": {
        PythonPath:     "market.get_price",
        Description:    "Return the current price for an allowlisted symbol",
        InputSchema:    json.RawMessage(`{"type":"object","required":["symbol"]}`),
        AllowEarlyRead: true,
        Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
            var request struct { Symbol string `json:"symbol"` }
            if err := json.Unmarshal(raw, &request); err != nil { return nil, err }
            price, ok := prices[request.Symbol]
            if !ok { return nil, errors.New("symbol is not allowlisted") }
            return map[string]any{"symbol": request.Symbol, "price": price}, nil
        },
    },
}

runner, err := pysolate.New(ctx, wasm, manifest)
if err != nil { return err }
defer runner.Close(context.Background())
output, err := runner.Run(ctx, source, inputs)
```

Run the complete checked-in example against the real Guest:

```sh
go run ./examples/python-tools -guest dist/pysolate.wasm
```

```json
{
  "result": {
    "leader": {"price": 418.2, "symbol": "MSFT"},
    "portfolio_value": 1574.8,
    "quotes": ["..."]
  },
  "host_tool_calls": 3
}
```

It returns the Python value together with the observed Host-call count. The same
boundary can be backed by an HTTP client, database, internal service, or an MCP
server without packaging that dependency into Python.

## Start with the core path

With a verified `dist/pysolate.wasm`, run:

```sh
./demos/run-core.sh
```

Set `PYSOLATE_GUEST=/path/to/pysolate.wasm` when the Guest artifact lives
elsewhere. The five steps show approved
Host Tools, files carried across isolated executions, repeated execution
through the local HTTP service, safe early reads, and deterministic replay. The
workspace step is file continuity; the replay step reconstructs a recorded Run.

Measured examples:

- **17.82 → 2.90 ms warm execution** for short Python with the opt-in Linux COW
  data-image path. Runtime API measurement; startup and memory costs are reported
  [alongside the result](docs/cow-data-image.md).
- **3.51× batch throughput** under the same two-CPU, 2 GiB limit when Tool waits
  release running slots. The [controlled 200 ms I/O experiment](docs/linux-density-study.md)
  also records the extra resident memory.
- **Recovery after a real process kill:** the external fixture receives two
  requests with the same operation key and applies one effect.
  [Run the restart demo](demos/13-durable-restart.sh); provider idempotency remains
  part of the recovery contract.

For a long-running application, both service commands support opt-in Linux
`-cow-data-image` preparation. The durable service also exposes paginated,
payload-free `GET /v1/durable/runs/{id}/history`. See the
[ordinary service](docs/service.md) and [durable service](docs/durable-service.md)
for commands, seed requirements and measured HTTP results.

See the [`docs/` index](docs/README.md) for supported workflows, measured
performance, scheduling studies and recent deliveries. See
[Execution-derived durability](docs/execution-derived-durability.md) for the
precise comparison with workflow-first systems such as Temporal.

## Host responsibilities and boundaries

Pysolate controls the Guest lifecycle and the narrow boundary between Python and
Host capabilities. The embedding Host supplies tools, validates their inputs,
keeps credentials, network clients and MCP sessions outside the Guest, owns
workspace publication, and declares recovery behavior for durable effects.

Pysolate does not provide arbitrary Python compatibility, exactly-once behavior
for an external provider, tenant authentication, a workflow engine, or mounts
of arbitrary Host paths. These limits are part of the integration contract.

## Execution model

```text
Runner owns compiled code and an optional clean image
  -> create a private Guest for one Run
  -> execute Python, calling explicitly granted Host tools
  -> return the value or Python error
  -> close the Guest and finish its tool workers
```

There are four responsibilities:

- **Runner:** ordinary execution and complete-source early reads share one lifecycle.
- **Tools:** Host providers discover canonical tools, metadata and Go call implementations; a small Python shim exposes only that catalog.
- **Image:** optional full-copy or Linux private COW memory, captured before user execution.
- **Store:** optional SQLite history for deterministic replay and durable waits.

No Broker/Plan hierarchy, plugin catalog, receipts, source certificates, workspace transaction framework, generic workflow engine, native backend or cross-run result cache is required.

## Build and run

Requires Go 1.25+ and a verified `dist/pysolate.wasm`. The quickest path is an
explicit qualified bundle from your release/internal distribution channel:

```sh
make artifact-install BUNDLE=/path/or/https-url/pysolate-agent-core.tar.gz
make verify-artifact
```

To build from source on Linux x86_64 with a C compiler, make, Python 3.11+,
curl, tar and unzip:

```sh
make bootstrap  # one-time pinned CPython/WASI and static NumPy inputs
make guest      # native relink and package
# Later bootstrap/pure-Python changes only:
make repack
```

`PYSOLATE_BUILD_INPUTS` selects an existing CPython/WASI cache. The setup
downloads honor standard `http_proxy`, `https_proxy` and `no_proxy` variables.
The link script also accepts `PYSOLATE_NUMPY_NATIVE_ROOT` and
`PYSOLATE_NUMPY_PACKAGE_ROOT` to reuse NumPy inputs. Changing user Python or
Host tool catalogs does not rebuild the Guest. See
[artifact profiles](docs/artifact-profiles.md) for the verified bundle format
and exact relink/repack boundary.

```sh
printf 'result = inputs["value"] + 1\n' | go run ./cmd/pysolate -inputs '{"value":41}'
go run ./cmd/pysolate -source examples/numpy.py
```

The CLI grants only a pure `echo` demonstration tool. Applications supply their own tools through the Go API. Python stdout is forwarded to stderr; the final JSON value is printed to stdout.

For the short presentation path, run the four core product demonstrations:

```sh
./demos/run-core.sh
```

It covers dynamic namespaced Host tools, workspace continuation across
disposable Guests, the hot HTTP service, and deterministic Tool-outcome replay.
For the original short acceptance suite, use:

```sh
./demos/run-all.sh
```

See [`demos/README.md`](demos/README.md) for the complete core, acceptance,
integration, durability and research demo index.

A Runner compiles once and may serve independent Runs. Each Run owns its Python state and tool workers. Tools must be concurrency-safe and honor their context. Call `Close` after all Runs have returned.

### Dynamic Host tools and MCP adapters

`ToolSpec` separates the canonical Host identity from its `PythonPath`, and can carry a description, JSON input schema and discovery annotations. Provider-native identities remain stable while the generated Python surface stays natural:

```python
price = stock.getprice(symbol="AAPL")
contents = filesystem.read_file(path="notes.txt")
```

Every generated function still dispatches through the same narrow Host call ABI. Only canonical identity, Python path and the early-read bit enter the Guest; descriptions and schemas stay on the Host so an Agent can inspect them before generating a one-shot script. There is no runtime `describe()` round trip. Top-level paths such as `price(...)` remain supported. Metadata is descriptive: MCP `readOnlyHint` does not enable early execution. The Host must still set `AllowEarlyRead` explicitly.

`ToolProvider` exposes a shared `Capability` contract. `ManifestFromProviders` builds an ordinary execution catalog; `DiscoverCapabilities` lets a durable Host inspect the same normalized definitions before `durable.ToolsFromProviders` requires an explicit version, recovery mode and scheduling policy for every capability. Duplicate canonical identities and colliding Python paths fail closed. `mcpadapter.Provider` converts an already connected MCP client into separately configured canonical and Python namespaces. `mcpadapter/gosdk` connects the official MCP Go SDK, including trusted stdio subprocesses, to that narrow interface. MCP transport, authentication, session lifecycle, schema enforcement and credentials stay on the Host and are never packaged into Guest Python. See [the MCP integration contract](docs/mcp.md), or run `./demos/11-mcp-stdio.sh` for a real initialize → tools/list → Guest call → tools/call round trip.

### Private workspaces

`runtime/workspace` creates a bounded private filesystem and grants an exclusive writer lease. `CreateFromDirectory` copies an ordinary Host working tree once; `.git` metadata remains Host-owned and source files are never modified in place. `RunWorkspace` mounts that lease only at `/workspace` and starts user code there with `/workspace` first on `sys.path`, enabling relative files and local-module imports. Ordinary `Run` still has no Host filesystem authority. The rooted adapter rejects traversal, symlinks, hard links, devices, filesystem-boundary crossings and writes beyond Host-selected file/byte/depth limits.

Workspace-prepared images need the same WASI preopen shape captured at initialization. Use `NewPreparedWorkspace` or Linux `NewPreparedWorkspaceCOW`, then execute with `RunWorkspace`. Ordinary `NewPrepared` runners reject workspace attachment instead of restoring an incompatible image. Workspace state can continue across disposable Guests, but publication back to a real project remains a separate Host operation. Writable workspaces are not part of `RunRecorded` durable replay.

`Lease.Snapshot` produces a path-independent revision and a bounded file manifest; `workspace.Diff` reports deterministic added, modified and deleted metadata without embedding file contents. `Lease.ExportChanges` can separately materialize a caller-bounded, versioned change bundle, and `CheckDirectoryConflicts` verifies only its touched Host paths against baseline metadata without writing them. Publication, merge and authorization remain outside the runtime. Python errors preserve private changes for Host inspection until cleanup. See [the workspace lifecycle and executable acceptance](docs/workspace.md), or run:

```sh
go run ./examples/workspace-edit -guest dist/pysolate.wasm
go run ./examples/agent-core-usecases -guest dist/pysolate.wasm
```

See [the qualified common-usecase boundary](docs/common-usecases.md) for repository/config editing, structured-data processing, Host-enriched scripts and deliberate exclusions.

### Deterministic corpus replay

`cmd/pysolate-corpus` executes frozen programs against exact Host-tool fixtures through the real Guest. The stdlib-only importer currently adapts pinned HumanEval canonical programs and BFCL ground-truth calls without putting an LLM in the measurement loop. See [the corpus schema, adapter semantics and reproducible commands](docs/corpus-replay.md), or run `./demos/05-corpus-replay.sh`.

`make workloads` runs the maintained 20-case common Agent workload lane plus
real workspace, MCP stdio and durable process-restart acceptance. See the
[workload pack scope and interpretation](docs/workload-pack.md).

`make replay-check` verifies seeded entropy and logical clocks across separate
processes, then checks completed Tool-outcome replay and prepared seed state.
The [determinism contract and next replay features](docs/deterministic-execution-and-replay.md)
separate program-visible replay guarantees from real scheduling variance.

For a small complete-source latency example, `./demos/16-fullcode-overlap.sh`
compares ordinary execution with overlapping two independent, explicitly
opted-in Host reads. It uses controlled delays and does not rely on prefix
streaming; see [the scope and exclusions](docs/fullcode-tool-overlap.md).

### Long-running service

`cmd/pysolate-server` keeps the compiled module and prepared clean images hot behind a bounded local HTTP API. It supports stateless Runs plus create/run/snapshot/read/destroy workspace lifecycles. Saturated execution capacity returns HTTP 429 immediately; the service does not grow an implicit queue.

```sh
go run ./cmd/pysolate-server -guest dist/pysolate.wasm -listen 127.0.0.1:8080 -max-active 4
```

The standalone binary intentionally grants no Host tools. An embedding application passes its trusted provider/MCP-derived `Manifest` to `service.New`; changing that catalog rebuilds service preparation but not the Guest artifact. See [the service API, trust boundary and loopback benchmark](docs/service.md).

### Durable local service

`cmd/pysolate-durable-server` adds persisted Run definitions, bounded attempt
admission, waits, cancellation and crash recovery around the durable Runner:

```sh
go run ./cmd/pysolate-durable-server \
  -guest dist/pysolate.wasm -db /tmp/pysolate-runs.db -listen 127.0.0.1:8081
```

The process-restart demo uses a real idempotent Host effect and verifies that a
crash after effect commit does not duplicate it during replay:

```sh
./demos/13-durable-restart.sh
```

The standalone command has no Host tools; embedding applications supply a
versioned durable Tool catalog. See [the durable service API and recovery
contract](docs/durable-service.md).

## Optional execution modes

- `RunWithEarlyReads` prepares only explicitly allowed stable, read-only snapshots. Values and errors are delivered at their original Python calls. Failed tools are not automatically retried.
- `NewPrepared` copies a clean initialized image into each Guest.
- `NewPreparedCOW` uses sealed Linux memfd/private mappings. It does not fall back to a different backend or restore an active stack.
- `NewPreparedWorkspace` and `NewPreparedWorkspaceCOW` capture the mount shape required by private workspaces.

```sh
go run ./cmd/pysolate -source examples/echo.py -mode early-reads
go run ./cmd/pysolate -source examples/echo.py -mode early-reads -prepared copy
# Linux:
go run ./cmd/pysolate -source examples/echo.py -mode early-reads -prepared cow
```

Streaming source execution has been removed. Pass complete source to `RunWithEarlyReads` (formerly `RunPLM`) or use CLI `-mode early-reads`; there is no `RunPrefix` replacement.

## Durable runs

`durable/` records source, inputs, seed, artifact identity, declared tool versions, call outcomes and waits. A restart creates a fresh Guest, replays saved outcomes and executes the unfinished suffix. Intent is committed before external dispatch; the outcome is committed before delivery to Python.

Unresolved external operations follow the Host's declared safe-retry, idempotent, lookup, manual or wait policy. Nothing infers those semantics from a tool name or source code. The optional journal can stop an attempt in a way Python cannot catch.

`RunRecorded` uses fresh Guests with per-attempt seeded WASI randomness and logical clocks. Early reads are excluded. An explicitly seeded prepared image can restore the matching deterministic initialization state; ordinary unseeded images are rejected. `PythonError` is a completed Python failure; timeout, storage and other infrastructure errors remain distinct Go errors.

The new SQLite format does not migrate old runtime databases. Cancellation stops future progress and signals the local attempt; it cannot roll back an external operation already started elsewhere.

## Boundaries

No Host directories, environment, network sockets or subprocess authority are ambient in the Guest. Guest imports use the locked [`agent-core` artifact profile](docs/artifact-profiles.md), including NumPy. External services, credentials and MCP connections stay behind Host tools. A Host can separately grant one runtime-owned private workspace at `/workspace`; arbitrary Host paths are never accepted as Guest mounts.

Current engineering defaults are 512 MiB maximum linear memory, 1 MiB per request/result and per stdout/stderr buffer, 1024 tool issues per attempt, and 64 outstanding early reads. Use context deadlines for elapsed-time bounds. The durable Store retains at most 64 MiB of logical payload per Run; SQLite/WAL physical overhead is separate.

## Read the code

```text
runner.go, bridge.go       Guest lifecycle and Host calls
tool_provider.go           provider discovery and normalized tool metadata
mcpadapter/                narrow MCP provider plus official Go SDK adapter
future.go                  Run-owned early reads
prepared.go                clean-image orchestration
internal/cowmem/           Linux private-memory backend and platform stub
recording.go               deterministic attempts and journal stops
durable/                   SQLite Store and recovery driver
runtime/workspace/         bounded private filesystems and change handoff
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

See [the measured results and trade-offs](docs/performance-results.md) for seeded reconstruction, native cache, Guest startup, historical PLM/prefix measurements and admission. The default paths remain explicit; improvements are not a claim that every workload or cold cache is faster.

## Verified scope

The new Guest was built and run, including matrix operations and separate NumPy Generator/RandomState integer-ABI regressions. `make check` passes with the real artifact. Historical targeted PLM/prefix and Store/journal race tests passed; a whole durable race run exceeded its initial 150-second execution budget and was not counted as a pass.

Linux tests exercise actual private COW mappings and Guest isolation. Recovery was verified after closing/reopening SQLite, after killing the process following an external fixture commit, and after hard-stopping/restarting a 2-vCPU/2-GiB Linux VM at that same window. The recovered fixture recorded one read, two write requests and one idempotent effect. This is not a physical-host power-loss guarantee.

Old APIs and databases are intentionally incompatible. The core and Guest contain 2,883 physical source lines (including comments/blank lines, excluding tests, CLI, build tooling and dependencies). Build tooling is separate from the execution core.

## Prepared deterministic attempts

`NewPreparedRecorded(ctx, wasm, tools, seed)` and its Linux COW variant
`NewPreparedRecordedCOW` capture clean Python memory **and** the entropy stream
position and logical clocks consumed during initialization. `RunRecorded` requires
that exact seed. Different Runs can share the image when they explicitly use the
same seed; there is no per-seed cache. A mismatched seed is rejected, never silently
replayed with different randomness. Early reads remain excluded from recorded execution.

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

Prefer `Admit(ctx, runID)` and `Attempt.Result(ctx)`. `durable.Result` returns `StateCompleted`, `StateParked`, `StateBlocked`, `StateFailed` or `StateCancelled`; Python and recovery failures include a typed `Failure`. Only infrastructure failures remain Go errors. Repeated admission of an already queued or resident Run returns `ErrBusy`, and a full admission queue returns `ErrQueueFull`. `AdvanceResult`, `Submit` and `Wait` remain compatibility APIs; `Wait` converts structured states back to their legacy errors.

Host tools are `durable.Inline` by default and keep the running slot. A tool explicitly declared with `Scheduling: durable.ExternalIO` lets the Executor reuse its running slot for another resident attempt while the live Guest and Wasm stack wait. When the call returns, its continuation enters the ready queue and reacquires a running slot before Python continues; ready continuations are preferred with a bounded burst so new admissions cannot starve. This policy is Host-local and is not part of the persisted Tool semantic snapshot. Use it only for cooperative, context-aware external I/O, not local CPU work. `Executor.Stats()` exposes running, resident, in-flight Tool, live-waiting, ready and queued counts.

Attach a shared `durable.ToolLatencyStats` as a Tool `Observer` to measure external-capacity queue time, Host service time, and continuation resume time separately. Snapshots provide deterministic means and EWMAs by tool, version, operation, scheduling class, and payload bucket. See [Scheduling evaluation](docs/scheduling-evaluation.md) for the real phase matrix and offline simulator; observations do not alter replay or effect safety.

A durable park is different: the Guest is destroyed and the logical Run can outlive it without resident capacity. Persist a decision through `Runner.Decide`, then explicitly `Admit` the same Run again to reconstruct and replay. Admission and scheduling stay outside `Runner`; the Runner continues to own journals, replay, effects, and Guest construction.

Cancelling the admission context cancels that attempt, leaving durable state resumable. `Executor.Cancel` uses durable cancellation first. `Executor.Close` stops new admission and drains existing jobs; a timeout does not destroy active resources, and Close can be retried. The caller still owns Runner and Store and closes them after drain. There is no background deadline scan, persistent queue, automatic retry or implicit resubmission after restart. The staged semantic-scheduling study and fixed workloads are documented in [`docs/semantic-scheduling-study.md`](docs/semantic-scheduling-study.md).
