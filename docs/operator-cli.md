# Operator CLI

`cmd/apyrun` is the only active integration entry point. It reads one `RunRequest` from stdin and writes one `RunResponse` to stdout.

## Flags

```text
-artifact  required path to agent-python-runtime.wasm
-manifest  required when execution_profile is configured
-config    optional Host-owned JSON configuration
```

## Host configuration

```json
{
  "timeout_ms": 20000,
  "max_request_bytes": 1048576,
  "max_response_bytes": 1048576,
  "memory_limit_pages": 8192,
  "execution_profile": {
    "id": "base",
    "allowed_imports": ["csv", "json"]
  },
  "workspace_files": {
    "input.txt": "hello"
  },
  "max_tool_calls": 8
}
```

All fields are optional. Unknown fields and trailing JSON are rejected. Resource fields default to the values in `runtime.DefaultRunConfig`.

`execution_profile` and `-manifest` must appear together. The CLI reads the manifest-selected import inventory and qualification sidecars, verifies the artifact, then replaces any Agent compatibility bookkeeping with Host-derived static imports.

When `workspace_files` is present, the CLI prebinds:

```python
read_text(path)
write_text(path, content)
list_files()
```

The workspace is in memory. `max_tool_calls` defaults to eight. It does not grant a Host path, socket, subprocess or package installation.

## Mounted workspace and complete capsule storage

As an alternative to `workspace_files`, the Host config may provision a rooted `/workspace` from a validated Host directory snapshot or a complete capsule:

```json
{
  "workspace": {
    "input_capsule": "/absolute/host/state.pwc",
    "output_capsule": "/absolute/host/next-state.pwc",
    "disposition": "export_on_success"
  }
}
```

`source_directory` may replace `input_capsule`; omitting both creates an empty workspace. `disposition` is mandatory: `export_on_success` and `export_on_response` require `output_capsule`, while `discard` forbids it. All configured paths must be clean and absolute. `workspace` and `workspace_files` are rejected together rather than creating two inconsistent state planes. The Agent request cannot select input/output paths, disposition policy, or workspace limits.

The Guest accesses this state with ordinary Python file APIs under `/workspace`. `/tmp` remains per-Run scratch. Every bounded response includes a Host-authored disposition receipt with request, initial state, final state and optional exact capsule identities. Output capsules are complete, deterministic storage artifacts and are atomically published with mode `0600`; they are not mounted in place or backed by SQLite. See [workspace-capsules.md](workspace-capsules.md).

## Request

```json
{
  "run_id": "demo",
  "code": "result = inputs['value'] + 1",
  "inputs": {"value": 41}
}
```

Agent-facing callers should omit `compatibility`; the Host derives it. `run_id` is an untrusted diagnostic label, not an authority identifier.

When typed Host tools are configured, the Host canonicalizes their versioned `CapabilitySpec` definitions, compiles strict input/output schemas, derives opaque `CapabilityGrant` identities from Host-owned per-Run policy documents, generates trusted Python module/method objects plus optional aliases and direct tool schemas, and seals the sorted specs, grants and total call budget as `pysolate.capability-plan.v4` before Guest startup. The response carries `capability_plan_sha256` even when no tool is called, and every capability receipt binds the same identity. Guest-authored plan or grant evidence is rejected.

`information_sources.demo_catalog` configures one dedicated, credential-free structured source. The Host config must provide an exact `http` or `https` endpoint, positive timeout and bounded response size. The generated Agent surface is `sources.demo_catalog()`; no URL or transport argument crosses the Guest boundary. The adapter performs GET only, disables environment proxies and compression, refuses redirect following, and requires status 200 plus `application/json`. It may coexist with a mounted workspace.

## Exit behavior

- `0`: a structured Guest response was written;
- `1`: runtime or I/O failure;
- `2`: invalid request, config, artifact or source admission;
- `3`: a requirement requires escalation outside Pysolate.

Diagnostics are short and do not include credentials, Host file contents, private model traces or Python source.
