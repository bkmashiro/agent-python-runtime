# Codex MCP adapter

`apyrun-mcp` is an opt-in stdio adapter that exposes one `python_runtime` MCP tool to a Codex process. It is not an Agent harness: Codex owns the conversation, model, routing, approvals, and session lifecycle. The adapter owns only bounded Runtime execution and evidence.

```text
Codex
  -> stdio MCP tools/call
      -> apyrun-mcp
          -> existing Runtime execution/trace service
              -> one compiled CPython/WASI Runtime
              -> single-use prepared slot
```

The adapter does not install a Codex plugin, edit `~/.codex/config.toml`, start a network listener, connect to Hermes, or replace another Python tool. `scripts/codex-runtime` injects one complete MCP server definition through command-line `-c` overrides only. It adds the Runtime server to the active Codex configuration; it does not disable other MCP servers already configured by the user. Exiting Codex ends the stdio child.

## Build and inspect without a model call

Use a pinned Runtime artifact and its matching distribution manifest:

```bash
export APYRUN_ARTIFACT=/absolute/dist/agent-python-runtime.wasm
export APYRUN_MANIFEST=/absolute/dist/manifest.json

scripts/codex-runtime mcp list
```

The wrapper builds `.artifacts-private/bin/apyrun-mcp` on first use, creates a private per-session trace path below `.artifacts-private/codex-mcp`, and then executes the local `codex` binary. Override these local paths when needed:

```bash
export APYRUN_MCP_BIN=/absolute/bin/apyrun-mcp
export APYRUN_STATE_DIR=/absolute/private/state-dir
```

`APYRUN_STATE_DIR` must be an owned, non-symlink directory with mode `0700`. The adapter creates its SQLite trace with mode `0600`.

## Open an interactive Codex session

```bash
APYRUN_ARTIFACT=/absolute/dist/agent-python-runtime.wasm \
APYRUN_MANIFEST=/absolute/dist/manifest.json \
  scripts/codex-runtime -C /absolute/project
```

The server exposes exactly one enabled tool, `python_runtime`. Its input is:

```json
{
  "code": "result = inputs['left'] + inputs['right']",
  "inputs": {"left": 19, "right": 23},
  "output_schema": {"type": "integer"}
}
```

`inputs` defaults to `{}` and `output_schema` is optional. Unknown fields, duplicate JSON keys, source over the Runtime limit, invalid schemas, and oversized stdio messages are rejected. The result contains both MCP text content and structured content with the Runtime result, bounded metrics, and the Host-authored `ExecutionRef`.

The wrapper defaults to:

```text
mcp_servers.agent_python_runtime.default_tools_approval_mode = "prompt"
```

For a disposable non-interactive canary only, approval can be scoped to this one invocation:

```bash
APYRUN_MCP_APPROVAL_MODE=approve \
APYRUN_ARTIFACT=/absolute/dist/agent-python-runtime.wasm \
APYRUN_MANIFEST=/absolute/dist/manifest.json \
  scripts/codex-runtime exec --ephemeral \
  'Call python_runtime once and report its returned result.'
```

Accepted approval values are `auto`, `prompt`, `writes`, and `approve`. Do not persist `approve` as a normal interactive default.

## Identity and trace boundary

Codex JSON-RPC request IDs are untrusted transport correlation only. For each MCP process and tool call, the adapter generates:

- a random Agent-run ID for the stdio process;
- a monotonically increasing turn/output/segment coordinate for that process;
- a random logical invocation ID with attempt `1`;
- a separate execution ID immediately before Runtime execution.

These are adapter-scoped synthetic coordinates. They are not Codex thread IDs, Codex turn IDs, or evidence of a Codex final state; the stdio adapter cannot observe those lifecycle boundaries.

The required SQLite trace persists metadata and digests only. It does not store Python source, inputs, output, prompts, model responses, tool arguments, credentials, or reasoning. A successful tool result is not accepted as proof by itself: verification should also read the matching started/completed invocation records and integrity digest from `apyrun-agent-trace`.

## MCP subset

The stdio server implements the bounded JSON-RPC subset needed by Codex:

- `initialize` using MCP protocol version `2025-06-18`;
- `notifications/initialized`;
- `ping`;
- `tools/list`;
- `tools/call` for `python_runtime` only.

Requests are newline-delimited UTF-8 JSON and capped at 1 MiB. Responses are capped at 4 MiB because MCP carries the Runtime's still-1-MiB-bounded result in both text and structured envelopes. Request objects reject duplicate keys, trailing JSON, unknown authority-bearing fields, malformed IDs, and unsupported methods. Standard request `_meta` objects such as Codex's `progressToken` are accepted but ignored.

## Verification

```bash
go test ./codexmcp ./cmd/apyrun-mcp -count=1
scripts/test_codex_runtime.sh
go test -race ./codexmcp ./cmd/apyrun-mcp -count=1
go vet ./...
go build ./cmd/...
```

A real canary must use a pinned artifact, observe one completed Codex MCP tool event with `result: 42`, and independently read one Runtime invocation with matching IDs, one started event, one completed event, and a non-empty integrity digest. A model final answer without a completed MCP event is not execution evidence.
