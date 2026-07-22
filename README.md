# Agent Python Runtime

A capability-controlled Python execution runtime for AI agents.

The runtime is intended for compound data workflows where an agent benefits from running generated Python, processing several Host-mediated tool results locally, and returning only a bounded final result. Guest code receives no ambient filesystem, environment, network, process, or secret authority. External operations cross Host-enforced capabilities with explicit budgets and receipts.

## Status

Tranche 0 contracts and provenance gates are implemented. Runtime/guest execution is not yet implemented, released, or deployed. Passing schema or source-lock tests is not WASI execution evidence.

## Initial scope

- CPython 3.14 and a verified NumPy subset compiled for `wasm32-wasip1`;
- Go Host built on wazero;
- prepared runtime pool with request-local state reset;
- strict memory, wall-time, output, and tool-call budgets;
- one real read-only batch capability;
- JSON request/result ABI and deterministic execution receipts;
- Linux/WASI artifact and denial gates.

The first version is not a general Linux sandbox, agent framework, MCP marketplace, or arbitrary PyPI environment.

## Documentation

- [Standalone implementation handoff](docs/plans/2026-07-22-agent-python-runtime-handoff.md)
- [Implementation plan](docs/plans/2026-07-22-agent-python-runtime-implementation-plan.md)
- [Architecture](docs/architecture.md)
- [Threat model](docs/threat-model.md)
- [Development and gates](docs/development.md)
- [Runtime boundary ADR](docs/adr/0001-runtime-boundaries.md)
- [Guest ABI ADR](docs/adr/0002-guest-abi-v1.md)
- [Artifact provenance ADR](docs/adr/0003-artifact-provenance.md)
