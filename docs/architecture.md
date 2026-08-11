# Architecture

## Purpose

Pysolate proves that a programming Agent can submit normal Python while the Host retains all authority. It is one execution component, not an Agent planner, package platform, scheduler, transaction service, or general Linux sandbox. Its Current implementation and longer-term Python-native capability-computer direction are separated in [product-direction.md](product-direction.md).

## Run path

1. Decode a bounded `RunRequest`.
2. Reject authority-bearing request fields and unsupported requirements.
3. Derive simple static import roots from the Agent source.
4. Bind those roots to a Host-owned execution profile and verified Guest distribution.
5. Instantiate a fresh wazero module with bounded memory and timeout.
6. Initialize CPython and apply optional trusted Host preparation.
7. Ask the Guest to validate the Python source contract.
8. Execute the request with an optional per-run Host tool broker.
9. Replace Guest receipt/metric claims with Host-authored values.
10. Close the Guest and per-run resources.

A failed, trapped, timed-out, or successful module is never reused.

## Authority split

### Agent request

The Agent may provide:

- `run_id` as a diagnostic label;
- Python `code`;
- JSON `inputs`;
- an optional JSON `output_schema`;
- compatibility requirements that can only narrow admission.

### Host configuration

The Host owns:

- artifact and manifest selection;
- execution profile and allowed imports;
- timeout, memory and byte limits;
- workspace contents;
- tool registration and call budget;
- trusted preparation code;
- receipt identity.

## Python compatibility

`BindAgentSource` derives imports and writes the compatibility declaration. Agent-facing callers therefore submit code, not bookkeeping metadata.

The Host scanner intentionally accepts only a small import preamble. It is not a Python parser. The CPython Guest independently parses and validates the source before execution. Conservative Host rejection is acceptable for this PoC; accidentally broad admission is not.

## Host tools

The active tool surface uses one generic Guest-to-Host JSON call envelope and a small Host Registry. Each registration binds a canonical `CapabilitySpec`—capability/version identity, documentation, effect/playback declarations, handler identity, strict input/output schemas and Python projection—to a Host handler, plus an opaque `CapabilityGrant` identity derived from the exact Host-owned per-Run policy. Before Guest startup, the Host seals the sorted specs, grants and total call budget into an immutable `pysolate.capability-plan.v4`; late registration is rejected. Handler identity remains stable implementation compatibility while changing target policy changes the grant and plan identities. The Broker accepts only that sealed plan, validates arguments before the handler, and validates results before returning them. The CLI generates module objects and compatibility aliases from those same sealed specs:

```python
workspace.read_text(path)      # alias: read_text(path)
workspace.write_text(path, content)  # alias: write_text(path, content)
workspace.list_files()         # alias: list_files()
```

The backing workspace for this typed surface is an in-memory map, not an ambient Host directory. Paths must be canonical and relative. Calls are bounded and produce small Host receipts. Every capability receipt binds the plan identity, and the Host projects that identity into the response even when no tool is called. Guest-authored plan evidence is rejected.

The plan also derives defensive direct Agent tool schemas from the same definitions. This generated but deliberately small surface does not restore the former generalized SDK generator, plugin discovery or durable effect workflow.

## WASI filesystem

The CLI can alternatively bind a Host-selected `runtime/workspace` lease as `/workspace` plus a fresh `/tmp`. The request cannot select the backing Host path, Capsule path, workspace limits or final disposition. Mounted workspaces and the typed in-memory workspace tools are mutually exclusive. The Host may restore or snapshot a complete Capsule and must explicitly choose `export_on_success`, `export_on_response` or `discard`; see [workspace-capsules.md](workspace-capsules.md).

## Active packages

```text
runtime/engine             runtime-neutral Runner contract
runtime/engine/wazero      fresh Guest implementation
runtime/capability         generic bounded Host calls
runtime/workspace          optional rooted WASI workspace
runtime/receipt            compact Host call receipts
runtime                    request/profile/artifact/response contracts
cmd/apyrun                 operator and PoC CLI
```

## Explicit non-goals

- served-instance reset or reuse;
- prepared/COW execution;
- durable effect transactions and compensation;
- network tools or credentials;
- MCP/daemon/plugin lifecycle;
- scheduler and benchmark orchestration;
- package installation and native extensions;
- production recovery, migration or multi-tenant operations.
