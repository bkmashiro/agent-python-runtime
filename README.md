# Agent Python Runtime

A low-latency, single-use CPython/WASI execution substrate for short-lived agent programs, with fresh per-Run authority.

Pysolate moves trusted CPython/package initialization off the request path, then executes each generated program with a fresh Host-owned authority boundary. The agent framework supplies code and JSON inputs; the Go Host owns artifact selection, limits, tool grants, credentials, workspace binding, instance lifecycle, effects, and receipts.

```text
PREPARED COMPUTATION   deterministic initialization happens before checkout
COLD AUTHORITY         every Run receives fresh grants, budgets, Broker and scratch
DISPOSABLE EXECUTION   a served CPython/WASI instance never returns to the ready pool
BOUNDED CONTINUATION   only a Host-owned ordinary-file workspace may cross Runs
```

The latency claim is lifecycle-specific: preparation cost is paid before a profile hit. It is not a claim that WASM executes Python or NumPy kernels faster than native Python.

Pysolate deliberately exposes programmable Python plus typed Host capabilities rather than ambient operating-system authority. It is not a general Linux sandbox, package manager, shell, VM fallback manager, persistent interpreter, or coding-agent computer. For explicitly declared unsupported runtime features, Pysolate returns a Host-authored structured rejection before execution; the legacy ABI field `escalation_required` means only that the caller must select another execution profile. Pysolate does not perform Hard escalation, cross-runtime continuation, VM replay, or error-text inference.

**Product direction (Framing, not a Current coverage claim):** Pysolate aims to replace Computer-first execution as the default for bounded agent programs. Python carries local control flow while Host-owned semantic capabilities, workspace, effects, and evidence retain real authority; ambient, native, and interactive long-tail workloads remain an upstream placement concern selected before execution. See the Vinculum design note [Pysolate-first agentic execution](https://github.com/bkmashiro/vinculum/blob/main/docs/pysolate-first-agentic-execution.md).

The programming model and snapshot mechanism are not unique. Cloudflare Code Mode executes generated code with projected tools; Python Workers prepares Pyodide/imports and snapshots WebAssembly linear memory to bootstrap new V8 isolates. Workers isolates may then serve multiple requests. Pysolate instead gives each served instance exactly one untrusted Run, binds fresh Host authority, closes that instance, and verifies that non-workspace state does not continue. It does not yet restore and reuse a served instance. See the [Vinculum related-work audit](https://github.com/bkmashiro/vinculum/blob/main/docs/related-work.md) and the [north-star evaluation contract](docs/north-star-evaluation-contract.md).

## Design

### Why Python?

Agents are good at choosing a plan. They are expensive and inconsistent at replaying every mechanical step of that plan. Once the procedure is known, run it as code.

Python is the execution language because models already write it well, it expresses loops and data transforms compactly, and the same snippet can call typed Host tools without giving the Guest their credentials or transports. The model decides *what* to do; Python performs the predictable part once.

| Keyword | Use it for |
|---|---|
| `CODE FIRST` | Known, deterministic work: filter, join, validate, aggregate, retry, branch. |
| `TOOLS AS FUNCTIONS` | Project granted Host tools into Python with typed arguments and ordinary return values. |
| `HOST-OWNED EFFECTS` | Keep credentials, commit policy, receipts, rollback, and approval outside model code. |
| `BOUNDED CONCURRENCY` | Let the Host cap active Guests and parallel I/O instead of allowing unbounded fan-out. |
| `RESET AFTER RUN` | Start the next request from a clean prepared image; never return a served slot to the ready pool. |

The [2026-07-28 MCP specification](https://modelcontextprotocol.io/specification/2026-07-28/changelog) removed protocol-level sessions and made the core request/response protocol stateless. That fits this runtime: an adapter can normalize an MCP `tools/list` result into a Host-owned catalog, then project accepted JSON Schemas as Python functions. If a tool needs cross-call state, it passes an explicit server-minted handle as an ordinary argument. The Guest does not own an MCP transport session.

A projected tool looks like a normal function:

```python
from host_tools import notes_search

notes = notes_search(query="WASM isolation", limit=5)
result = {"titles": [note["title"] for note in notes]}
```

The Host still owns the catalog snapshot, grant, call limit, credential, timeout, effect class, and receipt. Unsupported or ambiguous schemas are not exposed automatically.

### Small examples

**Predictable data work**

```python
# inputs = {"orders": [{"price": 12}, {"price": 30}, {"price": 7}]}
paid = [row for row in inputs["orders"] if row["price"] >= 10]
result = {"count": len(paid), "total": sum(row["price"] for row in paid)}
```

One model turn becomes one bounded run. There is no model round-trip for each filter, sum, or branch.

**Bounded parallel reads**

```python
from agent_runtime import tools

items = tools.fetch_many([
    {"request_id": "item-1", "target": "catalog", "path": "/items/1"},
    {"request_id": "item-2", "target": "catalog", "path": "/items/2"},
    {"request_id": "item-3", "target": "catalog", "path": "/items/3"},
])
result = {"items": items}
```

`fetch_many` is one Guest call. The Host executes it in bounded waves, preserves input order, and emits receipts. Guest code never receives a URL, socket, proxy setting, or credential.

For one Host-granted target/path, the Guest SDK also provides:

```python
item = tools.web_fetch("catalog", "/items/1")
```

`web_fetch` is a convenience wrapper over `fetch_many`; its target is still an opaque Host alias, not a URL. The provider-neutral `web.search` typed adapter and generated `web_search(query, max_results)` catalog projection are Current with a deterministic network-free Provider fixture. No live search provider is wired or qualified yet. See [Semantic web capabilities](docs/semantic-web-capabilities.md).

**Effects and rollback**

Each granted tool declares one effect level:

```text
read_only      no state change
reversible     exact rollback is available
compensatable  recovery uses a separate forward action
irreversible   no rollback; Host approval policy applies
```

The policy is also Host-owned: `DENY`, `AUTO_COMMIT`, `AGENT_COMMIT_REQUIRED`, or `USER_APPROVAL_REQUIRED`. Guest Python can request an operation; it cannot approve itself or relabel an irreversible effect as reversible.

### Output

Every run returns one JSON envelope:

```json
{
  "status": "ok",
  "result": {"count": 2, "total": 42},
  "receipts": [],
  "metrics": {
    "guest_time_ms": 1.25,
    "capability_calls": 0,
    "result_bytes": 22
  },
  "error": null
}
```

Tool calls add Host-authored receipts, for example:

```json
{
  "receipt_id": "receipt-01",
  "run_id": "host-run-01",
  "capability": "notes.search",
  "operation_index": 0,
  "outcome": "ok"
}
```

Failures return `status: "error"` with a bounded error object. Model text is never accepted as proof that a tool ran, committed, rolled back, or compensated.

### Concurrency and reset

The Host controls maximum memory, CPU, admission, ready inventory, refill, and tool-call concurrency. Execution can use a fresh Guest, a never-served preinitialized Guest, or a Linux COW-ready slot. Prepared profiles such as `numpy-ready-v1` move deterministic setup before readiness. Every served slot is discarded. A prepared pool creates a clean replacement; the COW path restores that replacement from the sealed image. This keeps request state isolated while preserving fast checkout.

Current properties:

- CPython 3.14 targeting `wasm32-wasip1`;
- backend-neutral Go `Runner`/`Factory` interface with a wazero backend;
- strict JSON request, result, tool-catalog, transaction, and evidence contracts;
- no ambient Guest filesystem, environment, socket, process, or credential access; an optional Host-bound `/workspace` exposes only the ordinary-file capsule described below;
- fresh, single-use prepared, and Linux COW-ready execution strategies;
- provenance manifests, source locks, checksums, SBOMs, and Linux/WASI integration tests;
- optional `numpy-core` artifact and `numpy-ready-v1` pre-COW warmup profile.

## Quick start

### 1. Run host-side tests

Requirements: Go 1.25 and Python 3.

```bash
git clone git@github.com:bkmashiro/agent-python-runtime.git
cd agent-python-runtime

go test ./...
python3 -m unittest discover -s tests -v
python3 -m unittest discover -s guest/tests -v
```

These checks exercise the Go runtime, schemas, source locks, and build contracts. They do not execute the real WASI guest.

### 2. Build and test the real guest

The guest build currently requires Linux x86-64, `build-essential`, `pkg-config`, `unzip`, and `xz-utils`. It downloads only SHA-256-locked inputs declared in `guest/build/sources.lock.json`.

```bash
AGENT_RUNTIME_ARTIFACT_PROFILE=base \
  guest/build/build-guest.sh

python3 guest/build/verify-artifact.py \
  dist/agent-python-runtime.wasm \
  dist/manifest.json

(
  cd dist
  sha256sum --check SHA256SUMS
)

AGENT_RUNTIME_GUEST="$PWD/dist/agent-python-runtime.wasm" \
  go test ./integration/e2e -count=1 -v
```

The `numpy-core` profile is manual and experimental:

```bash
AGENT_RUNTIME_ARTIFACT_PROFILE=numpy-core \
AGENT_RUNTIME_COW_FIXED_MEMORY=1 \
  guest/build/build-guest.sh
```

### 3. Execute one request

After building the base artifact:

```bash
go run ./cmd/apyrun \
  -artifact dist/agent-python-runtime.wasm \
  < abi/v1/fixtures/valid/request/basic.json
```

The request contains only generated code, JSON inputs, an untrusted run label, and an optional output schema. Host capabilities and credentials are configured separately; they cannot be granted by the request.

## Integration model

```text
agent framework
    │ generated code + JSON inputs
    ▼
Go host
    ├── validates the request
    ├── selects fresh / prepared / COW execution
    ├── enforces resource limits
    ├── mediates explicitly granted tool calls
    └── validates the bounded result
    ▼
CPython/WASI guest
```

For a first integration, start with a deterministic read-only fake tool and verify success, denial, timeout, and cross-run isolation before connecting any real provider. See [Framework integration test drive](docs/framework-integration.md).

## Execution strategies

| Strategy | Preparation boundary | Served instance reuse |
|---|---|---|
| `fresh` | instantiate and initialize for each request | no |
| `single-use-preinitialized` | initialize a never-served instance before checkout | no |
| `cow-ready-single-use` | restore a never-served slot from a sealed Linux COW image | no |

A configured COW warmup profile runs after CPython initialization and before the canonical image is sealed. For example, `numpy-ready-v1` imports NumPy before readiness so a matching request does not repeat the import.

Linux operators can explicitly derive a replacement-only snapshot shell that preserves every non-Data section and seeds active data before canonical initialization. It remains off by default and fails closed for artifacts with growable/imported memory, non-constant active-data offsets, or a WebAssembly start section:

```json
{
  "prepared_capacity": 256,
  "execution_strategy": "cow-ready-single-use",
  "cow_snapshot_shell": true
}
```

The optimization changes instance preparation only; served instances remain single-use and are never returned to the pool.

## Optional workspace capsule

A Host may bind a wazero `Factory` to an opaque workspace reference. The Host can provision that reference from copied `InitialFile` values or copy a previously materialized trusted directory once with `CreateFromDirectory`; source paths are never exposed to the guest. The guest then sees `/workspace` as a bounded ordinary-file tree. Sequential Runs use fresh or never-served single-use instances while reopening the same Host-owned tree, so file changes continue without preserving or copying interpreter state. The workspace is unavailable during prepared initialization and COW image creation, and a workspace-bound Runner serializes active Runs.

This surface does not add Git, shell, subprocess, package-manager, native executable, arbitrary mount, socket, credential, or Host-path access. The workspace is mutable and non-transactional: completed writes survive a failed Run. A workspace-bound Runner also receives a fresh bounded `/tmp` for each single-use instance; it is destroyed after that Run and never continues. Rollback, snapshots, patch export, and restart persistence remain deferred. See [Workspace Capsule v1](docs/workspace-capsule.md).

Verify a concrete artifact against the live 30-check instance contract before deployment; add a bounded disposable-instance stress loop when needed:

```bash
go run ./cmd/apyrun-verify-workspace \
  -artifact /absolute/path/to/agent-python-runtime.wasm \
  -stress-iterations 100 \
  -output workspace-verification.json
```

The v2 report is bound to the artifact SHA-256 and records actual engine properties plus workspace continuation, fresh heap, per-Run `/tmp`, failure/timeout/cancellation semantics, per-Run Broker freshness, exclusive leases, hidden Host paths, cleanup, and optional stress completion.

On the retained Linux benchmark, a fresh native CPython process importing NumPy took a median `324.067 ms`; a request hitting an already prepared NumPy-ready COW slot took `3.863 ms`. This is a lifecycle comparison, not a claim that WebAssembly executes NumPy kernels faster than native code. See the [benchmark report](docs/reports/scheduler-experiment-results.md#8-python-and-numpy-request-lifecycle).

## Repository map

```text
abi/v1/                    request, response, tool, and evidence schemas
cmd/apyrun/                local CLI
cmd/apyrun-benchmark/      artifact-bound benchmark harness
cmd/apyrun-verify-workspace/ live artifact Workspace Capsule verifier
runtime/engine/            backend-neutral runtime interface
runtime/engine/wazero/     wazero fresh, prepared, COW, and gated workspace mounting
runtime/workspace/         Host-owned ordinary-file workspace manager and rooted FS
runtime/capability/        host-owned capability registry and adapters
guest/                     CPython/WASI source, bootstrap, and build pipeline
integration/e2e/           real guest integration tests
eval/                      deterministic agent-workflow evaluation fixtures
agenttrace/                optional metadata-only Harness trace/replay plugin
hermesbridge/              isolated Hermes Unix-socket adapter boundary
cmd/apyrun-hermesd/        long-lived, no-capability Hermes Runtime bridge
codexmcp/                  minimal stdio MCP adapter for opt-in Codex sessions
cmd/apyrun-mcp/            one-tool Codex MCP server
cmd/apyrun-agent-trace/    read-only trace query, stats, JSONL export, and fork planning CLI
docs/                      architecture, threat model, integration, and results
```

## Documentation

- [Hermes Runtime bridge](docs/hermes-runtime-bridge.md)
- [Codex MCP adapter](docs/codex-mcp-adapter.md)
- [Framework integration test drive](docs/framework-integration.md)
- [Harness Agent trace/replay plugin](docs/agent-trace-plugin.md)
- [Architecture](docs/architecture.md)
- [Threat model](docs/threat-model.md)
- [Local CLI](docs/operator-cli.md)
- [Structured unsupported and escalation outcome](docs/unsupported-escalation.md)
- [Semantic web capabilities](docs/semantic-web-capabilities.md)
- [Development and test gates](docs/development.md)
- [Benchmark methodology](docs/benchmarking.md)
- [North-star evaluation contract](docs/north-star-evaluation-contract.md)
- [Scheduler and NumPy-ready results](docs/reports/scheduler-experiment-results.md)
- [Phase 6 NumPy-ready COW density and load qualification](docs/reports/phase6-numpy-density-results.md)
- [Phase 7 paired NumPy-ready COW density experiment](docs/phase7-paired-numpy-density.md)
- [Supply chain](docs/supply-chain.md)
- [Research roadmap](docs/research-roadmap.md)

## License

[MIT](LICENSE)
