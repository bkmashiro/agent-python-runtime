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

## Request

```json
{
  "run_id": "demo",
  "code": "result = inputs['value'] + 1",
  "inputs": {"value": 41}
}
```

Agent-facing callers should omit `compatibility`; the Host derives it. `run_id` is an untrusted diagnostic label, not an authority identifier.

## Exit behavior

- `0`: a structured Guest response was written;
- `1`: runtime or I/O failure;
- `2`: invalid request, config, artifact or source admission;
- `3`: a requirement requires escalation outside Pysolate.

Diagnostics are short and do not include credentials, Host file contents, private model traces or Python source.
