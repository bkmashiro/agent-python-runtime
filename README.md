# Agent Python Runtime

A capability-controlled Python execution runtime for AI agents.

The runtime is intended for compound data workflows where an agent benefits from running generated Python, processing Host-mediated tool results locally, and returning only a bounded final result. Guest code receives no ambient filesystem, environment, network, process, or secret authority. External reads cross the Host-enforced, read-only `fetch_many` capability with explicit grants, budgets, and receipts; guest code never receives raw network authority.

## Status

A neutral CPython 3.14 `wasm32-wasip1` core and one read-only `fetch_many` vertical slice are implemented and verified in private GitHub Actions. The same pinned-source artifact is built, checked, uploaded, downloaded by an independent job, and executed through the backend-neutral Go `Runner` contract with the wazero V1 adapter. Real E2E gates cover neutral execution, Host-owned capability mediation and receipts, fresh-instance isolation, trusted prepare behavior, timeout recovery, and ambient-authority denial.

The runtime is not released or deployed. An explicit manual-only `numpy-core` artifact profile, an opt-in bounded pool of never-served single-use prepared instances, and fail-closed public-target egress policy are implemented and separately gated. They do not imply arbitrary package support, served-instance snapshot/restore, private-network access, production sandboxing, release, or deployment. Exact double-build reproducibility remains unclaimed. See [implementation status and evidence](docs/status.md) for exact run links, artifact identity, proven properties, measurements, and exclusions.

## Current core

- CPython 3.14 built from a SHA-256-locked official source for `wasm32-wasip1`;
- neutral `runtime_init` / `runtime_prepare` / `alloc` / `dealloc` / `execute` ABI;
- strict JSON request/result schemas and negative fixtures;
- backend-neutral Go `Runner`/`Factory` boundary with a wazero V1 adapter;
- Host-owned time, memory, request, response, and capability bounds;
- read-only `fetch_many` target aliases, Host-owned credentials, partial results, and receipts;
- one local/development `apyrun` CLI with separate Host policy and untrusted RunRequest stdin;
- fresh instance per request by default; optional never-served single-use prepared candidates, bounded cancellation, and failure recovery;
- explicit manual-only `numpy-core` artifact profile with selected arrays/reductions/linalg, error, and cross-Run freshness evidence;
- provenance manifest, checksums, reviewed imports/exports, and private CI artifact;
- Linux/WASI execution and denial gates.

## Roadmap boundary

The original execution-runtime roadmap has completed the bounded `fetch_many`, single-use prepared-state, supply-chain, explicit `numpy-core`, and truthful-closeout tracks. `base` remains automatic and `numpy-core` remains manual-only; fresh-instance fallback and all non-release boundaries remain intact. A separate session-lifecycle successor roadmap is documented, but this closeout does not silently activate that larger scope.

The project is not a general Linux sandbox, agent framework, MCP marketplace, arbitrary PyPI environment, or write/effect execution system.

## Documentation

- [Local operator CLI](docs/operator-cli.md)
- [Runtime benchmark evidence](docs/benchmarking.md)
- [Prepared-state safety audit](docs/prepared-state-audit.md)
- [Planned execution/session lifecycle successor roadmap](docs/plans/2026-07-23-agent-python-session-lifecycle-autonomous-megagoal.md)
- [Guest artifact reproducibility](docs/reproducibility.md)
- [Guest supply-chain evidence](docs/supply-chain.md)
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
- [Execution slots and stateful session lifecycle ADR](docs/adr/0006-execution-session-lifecycle.md)
