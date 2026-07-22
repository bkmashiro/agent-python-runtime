# ADR 0001: Runtime boundaries

- Status: Accepted for V1 implementation
- Date: 2026-07-22

## Context

AI agents sometimes benefit from local loops, joins, filtering, and NumPy transformations over multiple tool results. Giving generated Python ambient Host authority would collapse the trust boundary and expose credentials, files, network, and process APIs.

## Decision

Build a standalone Go/wazero runtime around a versioned CPython WASI guest.

- The agent harness chooses direct tool call versus Python run.
- `RunRequest` contains code and data but no trusted authority.
- Host-owned `RunConfig` contains capability grants and enforceable budgets.
- External operations cross a named Host capability broker.
- Guest code has no inherited filesystem, environment, network, subprocess, or credentials.
- The Host owns lifecycle, pooling, cancellation, reset, and receipts.
- V1 supports read-only capabilities only.

## Consequences

- Python is an optional compound-workflow tool, not a mandatory action ABI.
- Package compatibility is intentionally narrower than native Python.
- A manifest or configuration declaration cannot substitute for denial tests.
- Unsupported hard limits fail closed.
- Agent planning, MCP marketplaces, durable artifact stores, and writes remain outside V1.

## Rejected alternatives

### Native Python subprocess as primary boundary

Rejected because it does not provide the selected WASI capability boundary and would require a separate OS sandbox design.

### Reusing Shimmy's guest protocol

Rejected because its evaluator-specific request shape, compatibility exports, and product semantics are not neutral Agent runtime contracts.

### Pyodide resident worker

Rejected for V1 because persistent JS/Python Host state complicates independent-run freshness and Host capability analysis.
