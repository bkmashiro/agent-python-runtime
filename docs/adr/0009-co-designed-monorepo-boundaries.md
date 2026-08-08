# ADR 0009: Co-designed monorepo subsystem boundaries

- Status: Accepted
- Date: 2026-08-08

## Context

Vinculum jointly optimizes Harness program lowering, Host authority binding, Pysolate execution, effects, and verification. Splitting Harness and Runtime into independent repositories now would make contract changes and cross-plane benchmarks unnecessarily expensive. Treating them as one undifferentiated component would instead let provider, conversation, approval, or verification concerns leak into the Runtime core.

The repository already contains independently testable Runtime, trace, bridge, integration, and evaluation packages. Repository topology therefore does not need to equal trust-boundary topology.

## Decision

Keep the implementation in one monorepo while enforcing logical subsystem and dependency boundaries.

```text
Harness / Host / Verification
          │
          ▼
Agent Execution Contract
          ▲
          │
Pysolate Runtime adapter
          │
          ▼
Pysolate Runtime core
```

- **Vinculum** names the co-designed whole system.
- **Pysolate Runtime** names the bounded CPython/WASI execution subsystem.
- Harness, Host effect handling, trace, and verification remain separate packages and independently testable surfaces.
- Runtime core must not import provider lifecycle, conversation state, Harness planning, trace persistence, task oracles, or claim verification.
- Cross-plane types are limited to versioned execution contracts such as requests, Host-owned authority, `ExecutionRef`, receipts, and artifact identities.
- Joint optimization is evaluated through top-level integration and benchmark suites, not by bypassing package boundaries.
- A subsystem may have its own binary or release artifact without requiring its own Git repository.

The first Claim Manifest implementation therefore lives in top-level Host-owned `claimmanifest`; it consumes `runtime.ExecutionRef` and `agenttrace.Playback`. `runtime` does not import it.

## Consequences

- Contract and benchmark changes can remain atomic in one commit.
- Pysolate remains usable and testable without a full Agent Harness.
- Harness evolution can use Runtime evidence without making Runtime understand prompts or providers.
- Go import direction provides an immediate cycle gate; architecture tests and review must continue to reject semantic leakage not visible as an import cycle.
- Repository extraction remains possible later if ownership, release cadence, or external consumers create a concrete need.

## Rejected alternatives

### Separate Harness and Runtime repositories now

Rejected because the contract is still evolving and the main performance claims require coordinated source, workload, and evidence revisions. Cross-repository version skew would add process without strengthening the current trust boundary.

### Put Harness behavior inside Runtime packages

Rejected because generated-program planning, provider lifecycle, approval, effect reconciliation, replay qualification, and task oracles are Host/Harness responsibilities. Co-design does not require shared authority or cyclic dependencies.

### Move the Runtime into the documentation repository

Rejected. The Vinculum documentation repository remains the canonical design and research workspace; executable implementation and its source-backed claims remain in this repository.
