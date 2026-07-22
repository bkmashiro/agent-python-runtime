# Architecture

## Status

This document defines the target architecture. A component is not implemented until its tests and Linux/WASI gates pass.

## Product boundary

Agent Python Runtime executes one bounded generated-Python run. It is not an agent planner, tool selector, MCP marketplace, package installer, general Linux sandbox, or external-effect transaction system.

```text
Agent harness
  │ RunRequest + Host-owned RunConfig
  ▼
Go Runtime
  ├─ request/schema validation
  ├─ wazero engine and instance pool
  ├─ lifecycle, cancellation, and limits
  ├─ snapshot/restore or instance discard
  ├─ capability broker
  └─ receipts and metrics
  │ versioned core-WASM ABI
  ▼
CPython/NumPy WASI guest
  ├─ trusted runtime bootstrap
  ├─ optional trusted preparation
  ├─ generated-code execution
  └─ narrow agent_runtime Python SDK
```

The agent harness decides whether Python is useful. The runtime does not force simple actions through code.

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
- receipt identity;
- artifact selection and accepted ABI version.

Authority-bearing fields are not accepted from `RunRequest`.

## State classes

### Prepared base

Created only by trusted bootstrap/preparation code. Snapshot capture occurs after the exported call returns to the Host, at a quiescent boundary.

### Run-local

Generated-code globals, imported modules, arrays, temporary buffers, capability results, counters, and outputs. It must not survive the run.

### Cross-run artifacts

Deferred. V1 returns bounded JSON/bytes plus a digest. It does not persist an interpreter heap or arbitrary Pickle.

## Guest lifecycle

```text
compile artifact once
→ instantiate with deny-by-default WASI context
→ runtime_init(trusted config)
→ optional runtime_prepare(trusted code/data)
→ capture prepared state
→ checkout instance
→ execute(untrusted request)
→ validate bounded response
→ restore prepared state OR discard instance
→ return healthy instance to pool
```

A trap, cancellation, unsupported memory shape, or failed reset makes the instance unhealthy. It is closed, not returned to the pool.

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
abi/v1/                 JSON contracts and fixtures
guest/                  neutral CPython/WASI producer
runtime/abi/             ABI validation and memory protocol
runtime/engine/          wazero integration and hard limits
runtime/pool/            lifecycle and health
runtime/snapshot/        prepared-state capture/reset
runtime/capability/      Host-owned grants and broker
runtime/receipt/         bounded operation evidence
integration/e2e/         real artifact consumer tests
cmd/apyrun/              eventual single CLI entry
docs/evidence/           canonical JSON and generated reports
```

## First vertical slice

The first milestone is a pure-CPython guest built from pinned licensed sources and consumed by a Go/wazero Host in Linux CI. It must prove bounded execution, denial of ambient authority, cancellation, error handling, and next-run freshness before NumPy or external capabilities are added.
