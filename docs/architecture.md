# Architecture

## Purpose

Pysolate proves that a programming Agent can submit normal Python while the Host retains all authority. It is one execution component, not an Agent planner, package platform, scheduler, transaction service, or general Linux sandbox.

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

The active tool surface uses one generic Guest-to-Host JSON call envelope and a small Host registry. The CLI can prebind three workspace functions into the Python globals:

```python
read_text(path)
write_text(path, content)
list_files()
```

The backing workspace is an in-memory map, not an ambient Host directory. Paths must be canonical and relative. Calls are bounded and produce small Host receipts.

This fixed surface replaces the former generalized JSON-Schema-to-Python SDK generator and durable effect workflow.

## WASI filesystem

The lower-level engine can also bind a Host-selected `runtime/workspace` lease as `/workspace` plus a fresh `/tmp`. The request cannot select the backing Host path. The focused PoC CLI uses typed in-memory workspace tools instead, keeping the demonstrated authority boundary small.

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
