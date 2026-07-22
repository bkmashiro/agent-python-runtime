# Agent Python Runtime

A capability-controlled Python execution runtime for AI agents.

The runtime is intended for compound data workflows where an agent benefits from running generated Python, processing Host-mediated tool results locally, and returning only a bounded final result. Guest code receives no ambient filesystem, environment, network, process, or secret authority. Future external operations must cross Host-enforced capabilities with explicit budgets and receipts.

## Status

A neutral CPython 3.14 `wasm32-wasip1` core vertical slice is implemented and verified in private GitHub Actions. The same pinned-source artifact is built, checked, uploaded, downloaded by an independent job, and executed by the Go/wazero Host. Real E2E gates cover neutral execution, fresh-instance isolation, trusted prepare behavior, timeout recovery, and ambient-authority denial.

The runtime is not released or deployed. NumPy, Host capability mediation, receipts, prepared snapshots, reproducibility double-builds, and production hardening remain future work. See [implementation status and evidence](docs/status.md) for exact run links, artifact identity, proven properties, and exclusions.

## Current core

- CPython 3.14 built from a SHA-256-locked official source for `wasm32-wasip1`;
- neutral `runtime_init` / `runtime_prepare` / `alloc` / `dealloc` / `execute` ABI;
- strict JSON request/result schemas and negative fixtures;
- Go Host built on wazero with Host-owned time, memory, request, and response bounds;
- fresh instance per request, bounded cancellation, and failure recovery;
- provenance manifest, checksums, reviewed imports/exports, and private CI artifact;
- Linux/WASI execution and denial gates.

## Roadmap boundary

The next product slice is one real read-only Host capability. NumPy and prepared snapshot optimization follow only after their source, license, state-restoration, and failure-recovery gates are proven.

The project is not a general Linux sandbox, agent framework, MCP marketplace, arbitrary PyPI environment, or write/effect execution system.

## Documentation

- [Active autonomous mega-goal](docs/plans/2026-07-22-agent-python-runtime-autonomous-megagoal.md)
- [Implementation status and evidence](docs/status.md)
- [Standalone implementation handoff](docs/plans/2026-07-22-agent-python-runtime-handoff.md)
- [Implementation plan](docs/plans/2026-07-22-agent-python-runtime-implementation-plan.md)
- [Architecture](docs/architecture.md)
- [Threat model](docs/threat-model.md)
- [Future Effect Plane: playback, COMMIT, and APPROVE](docs/effect-plane.md)
- [Development and gates](docs/development.md)
- [Runtime boundary ADR](docs/adr/0001-runtime-boundaries.md)
- [Guest ABI ADR](docs/adr/0002-guest-abi-v1.md)
- [Artifact provenance ADR](docs/adr/0003-artifact-provenance.md)
- [WebAssembly backend boundary ADR](docs/adr/0004-webassembly-backend-boundary.md)
- [`fetch_many` capability ADR](docs/adr/0005-fetch-many-capability.md)
