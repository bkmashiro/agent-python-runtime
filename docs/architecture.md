# Architecture

## Status

This document defines the target architecture. A component is not implemented until its tests and Linux/WASI gates pass.

## Product boundary

Agent Python Runtime executes one bounded generated-Python run. It is not an agent planner, tool selector, MCP marketplace, package installer, general Linux sandbox, or external-effect transaction system.

V1 external effects remain read-only. The optional workspace capsule is mutable Host-owned file state inside the sandbox boundary, not a permission to modify an external repository or service. A possible future Host-owned side-effect layer is discussed separately in [Effect Plane: playback, COMMIT, and APPROVE](effect-plane.md); workspace semantics do not expand that authority.

```text
Agent harness
  ├─ optional metadata-only trace/replay plugin (Harness-owned)
  └─ RunRequest + Host-owned RunConfig
       ▼
Go Runtime
  ├─ request/schema validation
  ├─ backend-neutral Runner/Factory contract
  ├─ wazero adapter and fresh-instance isolation baseline
  ├─ lifecycle, cancellation, and limits
  ├─ current fresh or never-served single-use prepared execution
  ├─ target dispatcher-owned Runtime/CompiledModule flyweight
  ├─ target Linux COW prepared image and private slot mappings
  ├─ capability broker
  └─ receipts and metrics
  │ versioned core-WASM ABI
  ▼
CPython WASI guest with optional selected NumPy profile
  ├─ trusted runtime bootstrap
  ├─ optional trusted preparation
  ├─ generated-code execution
  └─ narrow agent_runtime Python SDK
```

The agent harness decides whether Python is useful. The runtime does not force simple actions through code.

Complete Agent-loop observability is an optional Harness concern. The Runtime accepts only a Host context `InvocationRef` and projects a Host-authored `execution_ref`; it does not parse providers or own the trace database. See [Harness Agent trace/replay plugin](agent-trace-plugin.md).

## Trust boundaries

### Untrusted

- generated Python;
- `RunRequest.code` and `RunRequest.inputs`;
- requested tool names and arguments;
- output values, exceptions, and guest pointers;
- guest stdout/stderr if enabled in a future version.

### Host-owned

- capability grants;
- credentials and endpoint allowlists;
- memory, wall-time, output, and tool-call budgets;
- instance lifecycle and pool admission;
- optional workspace manager, opaque reference, limits, and exclusive Runner lease;
- receipt identity;
- artifact selection and accepted ABI version.

Authority-bearing fields are not accepted from `RunRequest`.

## State classes

### Prepared base

Created only by trusted bootstrap/preparation code. The implemented optimization admits a never-served initialized instance to a bounded Host pool, checks it out once, and discards it after that Run. No snapshot capture, restore, or served-instance reuse is implemented.

The active target adds an exact sealed prepared-memory image shared by independent Linux `MAP_PRIVATE` slot mappings. The first promotion level still discards every served slot. A later slot may return to the pool only after complete mutable-memory, global, table, WASI, Host-resource, growth, timeout, and failure-path evidence. See [ADR 0008](adr/0008-cow-python-reactor-performance-density.md) and the [research roadmap](research-roadmap.md).

### Run-local

Generated-code globals, imported modules, arrays, temporary buffers, capability results, counters, and outputs. It must not survive the run.

### Cross-run file state

The optional implemented workspace capsule preserves only a Host-owned bounded tree of ordinary files and directories. Sequential disposable instances reopen the same root; no per-Run tree copy, Python heap, WebAssembly memory, Broker, descriptor, or native resource continues. Each workspace-bound instance also receives a separate bounded `/tmp`, created only after checkout and destroyed when that instance closes. The v1 workspace tree is mutable and non-transactional: completed writes survive Run failure, while snapshots, rollback, fork, patch export, and restart persistence are deferred. See [Workspace Capsule v1](workspace-capsule.md).

### Future stateful sessions

Stateful sessions are a separate future Host-owned lifecycle contract, not an extension of untrusted `RunRequest` and not a relaxation of V1 freshness. A durable session may be represented by an explicitly bound live module, an exact-build memory capsule, or a Guest-defined logical capsule only after the complete mutable state and external-resource boundary is proven. Dirty linear-memory pages alone are partial state.

See [ADR 0006](adr/0006-execution-session-lifecycle.md), the [Host-owned lifecycle contract](session-lifecycle-contract.md), and the [research roadmap](research-roadmap.md). These documents add no session manager, capsule, persistence, or restore claim by themselves.

Stateless COW execution-slot reuse is not a stateful-session contract. It restores a trusted prepared base and discards all Run-local state; it does not preserve a user Python heap between Runs.

## Guest lifecycle

```text
compile artifact once
→ either instantiate synchronously for this Run
  or checkout one never-served instance that already completed _initialize/runtime_init
→ attach a fresh Host-owned per-Run broker
→ for a bound workspace, activate its per-module gate only after checkout
→ runtime_prepare(request-specific trusted code/data)
→ execute(untrusted request)
→ validate bounded response
→ close and discard the served instance on every outcome
→ refill the bounded prepared pool in the background when enabled
```

No dirty instance returns to the pool. A trap, cancellation, failed preparation, unsupported memory shape, or refill failure closes/discards the affected instance; a pool miss uses the synchronous fresh path.

The target COW lifecycle evolves in evidence-gated levels:

```text
shared Runtime/CompiledModule
→ independently initialized and verified prepared slots
→ sealed canonical memory image
→ one MAP_PRIVATE mapping per slot
→ first level: serve once and discard
→ later level: clear Host request state, restore every eligible state class,
  remap private pages to the canonical image, verify invariants, then reuse
```

This target is not implemented until the corresponding Linux gates pass. Page-write profiling precedes CPython allocator, subinterpreter, dirty-aware reset, or compression work.

## Execution slots versus durable sessions

Execution capacity and session durability are distinct resources:

```text
trusted immutable base(s)
        ├── bounded sessionless hot execution slots
        ├── explicitly bound live sessions when latency requires them
        ├── compressed warm capsules
        └── versioned/encrypted cold capsules
```

Hot-slot count follows active concurrency and resume requirements, not total session count. Shared bases are created only from trusted deterministic preparation and never from arbitrary user session memory. Exact-memory capsules bind the Guest artifact, profile, trusted preparation recipe, backend/runtime ABI, architecture, page size, and complete state schema. Logical capsules require an explicit Guest export/import contract; arbitrary Python objects are not assumed to be portable.

## Reset claim boundary

Linear-memory restoration is not assumed to reset every possible WebAssembly or Host state. Each artifact is classified as:

- naturally unwound after the export returns;
- represented in snapshotted linear memory;
- absent/read-only because the Host did not grant a capability;
- unsupported and rejected/discarded.

Mutable globals, tables, Host handles, external side effects, and memory growth require explicit evidence or fail-closed behavior.

## Capability path

Generated Python imports a narrow SDK such as:

```python
from agent_runtime import tools
rows = tools.fetch_many(requests)
```

The SDK serializes a bounded request to a versioned Host import. The Host resolves a pre-granted capability, applies endpoint and call budgets, performs I/O with Host-owned credentials, writes a bounded response into guest memory, and emits one receipt per internal operation.

[ADR 0007](adr/0007-mcp-transactional-tool-workflows.md) and the [research roadmap](research-roadmap.md) preserve this single import while defining dynamic typed tool projection and Host-owned transaction/effect handling. Capabilities outside the tested contracts remain unavailable.

The guest never receives a raw socket or credential.

## Artifact boundary

The guest producer is build infrastructure, not runtime orchestration. GitHub Actions emits:

- the `.wasm` artifact;
- manifest and SHA-256;
- source/toolchain identity;
- exact imports/exports;
- third-party notices and SBOM;
- raw smoke results.

The Go E2E job consumes the exact artifact produced in the same workflow. Native Python is not a substitute.

## Initial package structure

Directories are added only with tested behavior:

```text
abi/v1/                   JSON contracts and fixtures
guest/                    neutral CPython/WASI producer and optional selected profile
runtime/abi/              ABI validation and memory protocol
runtime/engine/           backend-neutral Runner/Factory and ABI framing
runtime/engine/wazero/    current wazero adapter, fresh path, and single-use prepared pool
runtime/capability/       Host-owned grants and broker
runtime/receipt/          bounded operation evidence
integration/e2e/          real artifact consumer tests
cmd/apyrun/               implemented local/development CLI
cmd/apyrun-benchmark/     fresh/prepared evidence command
benchmark/                machine-readable evidence schemas
```

## Completed initial vertical slice

The first milestone was a pure-CPython guest built from pinned licensed sources and consumed by a Go/wazero Host in Linux CI. It proved bounded execution, denial of ambient authority, cancellation, error handling, and next-run freshness before the later bounded `fetch_many`, prepared-candidate, and explicit `numpy-core` slices were admitted.
