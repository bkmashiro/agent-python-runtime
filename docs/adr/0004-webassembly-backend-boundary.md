# ADR 0004: WebAssembly backend boundary

- Status: Accepted for V1 implementation
- Date: 2026-07-22

## Context

The first vertical slice embedded wazero types directly in `runtime/engine`. The guest ABI, policy, and artifact are Core WebAssembly/WASI contracts, but callers consequently depended on one concrete runtime type. Future reset optimizations also risked conflating a security property with a wazero-specific memory mechanism.

## Decision

Define a narrow backend-neutral boundary in `runtime/engine`:

- `Runner` exposes `Run`, `Close`, and validated observable `Properties`;
- `Factory` names a backend and constructs a `Runner` from artifact bytes and Host-owned `RunConfig`;
- `ResetMode` records the isolation mechanism the runner actually applies;
- unknown or incomplete property claims are rejected.

The current implementation lives in `runtime/engine/wazero` and reports `fresh-instance`. The integration suite holds only an `engine.Runner` and injects `wazero.Factory`.

Do not abstract backend module, memory, function, linker, store, interrupt, or snapshot types. Those details remain inside each adapter. A future backend must pass the same artifact verification, authority-denial, timeout recovery, and freshness conformance tests before it is supported.

## Reset semantics

`fresh-instance` is the portable fail-closed baseline: each request instance is closed and the next request is newly instantiated from the compiled artifact.

`prepared-restore` is a reserved claim, not a current implementation. An adapter may report it only after proving restoration for every mutable artifact state class, including linear memories, mutable globals, tables, Host resources, memory growth, and failure paths. A trap, cancellation, or unsupported state still requires instance discard.

## Consequences

- The guest ABI and product policy do not name wazero.
- The Go module continues to depend on wazero because it is the only implemented V1 adapter.
- Replacing the adapter does not change request/response contracts, but requires all real artifact conformance gates.
- Runtime-specific pooling, copy-on-write, or snapshot APIs can be used without leaking into callers.
- A backend name or reset claim is evidence metadata, not proof by itself.

## Rejected alternatives

### Keep returning `*wazero.Engine`

Rejected because callers and future lifecycle code would become coupled to one backend before reset optimization is designed.

### Abstract every low-level runtime primitive

Rejected because different runtimes have materially different memory, interruption, resource, and snapshot semantics. A lowest-common-denominator wrapper would hide security-relevant differences.

### Require multiple backend implementations now

Rejected because no current product requirement justifies a second runtime. The neutral contract and conformance suite preserve the option without adding an unverified adapter.
