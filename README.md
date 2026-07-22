# Agent Python Runtime

A capability-controlled Python execution runtime for AI agents.

The runtime is intended for compound data workflows where an agent benefits from running generated Python, processing several tool results locally, and returning only a bounded final result. Guest code runs without ambient filesystem, environment, network, process, or secret access. External operations are mediated by host-enforced capabilities with explicit budgets and receipts.

## Status

Planning and handoff only. No runtime code, remote repository, release, deployment, or hosted service exists yet.

## Initial scope

- CPython 3.14 and a verified NumPy subset compiled for `wasm32-wasip1`
- Go host built on wazero
- prepared runtime pool with request-local state reset
- strict memory, time, output, and tool-call budgets
- one real read-only batch capability
- JSON request/result ABI and deterministic execution receipts
- Linux/WASI artifact and denial gates

The first version is not a general Linux sandbox, an agent framework, an MCP marketplace, or an arbitrary PyPI environment.

## Source of truth

Read [`docs/plans/2026-07-22-agent-python-runtime-handoff.md`](docs/plans/2026-07-22-agent-python-runtime-handoff.md) before implementation.
