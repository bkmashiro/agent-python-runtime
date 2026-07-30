# Agent Python Runtime

A capability-controlled CPython/WASI runtime for executing generated Python without giving guest code ambient access to the host.

The agent framework stays outside the sandbox. It supplies code and JSON inputs; the Go host owns runtime limits, tool grants, credentials, instance lifecycle, and receipts.

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
- no ambient Guest filesystem, environment, socket, process, or credential access;
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

On the retained Linux benchmark, a fresh native CPython process importing NumPy took a median `324.067 ms`; a request hitting an already prepared NumPy-ready COW slot took `3.863 ms`. This is a lifecycle comparison, not a claim that WebAssembly executes NumPy kernels faster than native code. See the [benchmark report](docs/reports/scheduler-experiment-results.md#8-python-and-numpy-request-lifecycle).

## Repository map

```text
abi/v1/                    request, response, tool, and evidence schemas
cmd/apyrun/                local CLI
cmd/apyrun-benchmark/      artifact-bound benchmark harness
runtime/engine/            backend-neutral runtime interface
runtime/engine/wazero/     wazero fresh, prepared, and COW implementations
runtime/capability/        host-owned capability registry and adapters
guest/                     CPython/WASI source, bootstrap, and build pipeline
integration/e2e/           real guest integration tests
eval/                      deterministic agent-workflow evaluation fixtures
docs/                      architecture, threat model, integration, and results
```

## Documentation

- [Framework integration test drive](docs/framework-integration.md)
- [Architecture](docs/architecture.md)
- [Threat model](docs/threat-model.md)
- [Local CLI](docs/operator-cli.md)
- [Development and test gates](docs/development.md)
- [Benchmark methodology](docs/benchmarking.md)
- [Scheduler and NumPy-ready results](docs/reports/scheduler-experiment-results.md)
- [Supply chain](docs/supply-chain.md)
- [Research roadmap](docs/research-roadmap.md)

## License

[MIT](LICENSE)
