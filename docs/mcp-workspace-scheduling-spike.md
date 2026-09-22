# MCP scheduling to workspace workflow spike

This spike exercises a common agent-shaped workflow without an LLM in the timed loop:

1. two durable Runs each make two dependent calls to a real MCP stdio server;
2. the Host compares inline calls with `ExternalIO` slot yielding;
3. each validated MCP result is passed into a separate private-workspace Guest;
4. Python edits JSON and Markdown;
5. the Host exports a bounded `ChangeSet` and verifies the original source is conflict-free.

The code and inputs are deterministic. Every sample checks the exact MCP values, call count, changed-file count, conflict report and Guest result.

## Architectural boundary

Writable workspaces are deliberately excluded from durable replay. `ExternalIO` scheduling currently belongs to the durable Executor, while `RunWorkspace` belongs to the ordinary Runner. The spike therefore uses two explicit stages rather than coupling workspace state into durable replay:

```text
MCP stdio -> durable fetch/normalize -> validated JSON
                                      -> workspace Guest -> ChangeSet
```

This is an observed product boundary, not hidden benchmark setup. The experiment answers whether real MCP calls retain the live-I/O scheduling benefit and measures how much of the complete workflow remains outside that optimizable phase. It does not claim that a workspace Guest currently yields its running slot during a Tool call.

## Run

```sh
./demos/12-mcp-workspace-scheduling.sh

# Directly, with a shorter smoke configuration:
go run ./examples/mcp-workspace-scheduling \
  -guest dist/pysolate.wasm \
  -tasks 2 \
  -iterations 1 \
  -tool-delay 10ms
```

The command starts its own trusted MCP subprocess using the official Go SDK. It requires no external service or network access.

## Output contract

The JSON report includes every raw sample and a p50 summary for each scheduling mode:

- `create_ns`: durable control-plane row creation, outside the fetch timer;
- `fetch_batch_ns`: admission through completion of all MCP-backed Runs;
- `workspace_batch_ns`: private workspace provisioning, Guest edits, export and conflict checks;
- `total_ns`: creation, fetch and workspace stages together;
- `tool_queue_ns`, `tool_service_ns`, `continuation_resume_ns`: sums of observed Tool phases;
- `peak_mcp_calls`: concurrent real MCP client calls;
- `changed_files`, `conflict_free`, `oracle_passed`: correctness checks.

Runner construction, Guest preparation, MCP initialization and tool discovery happen before samples and are intentionally excluded. Workspace execution is sequential in this initial spike so it does not introduce a second, implicit scheduler.
The harness alternates which scheduling mode runs first on each iteration to
reduce fixed-order cache and thermal bias.

## Initial local observation

A three-sample macOS arm64 run used two workflows, one running slot, two resident slots, and a deterministic 50 ms delay in each of two dependent MCP calls per workflow:

- inline fetch p50: `235.29 ms`;
- `ExternalIO` fetch p50: `116.87 ms`, a `50.3%` reduction;
- inline complete-workflow p50: `558.27 ms`;
- `ExternalIO` complete-workflow p50: `431.84 ms`, a `22.6%` reduction;
- MCP concurrency increased from `1` to `2`;
- all six samples made exactly four MCP calls, exported four files, remained conflict-free, and passed the deterministic oracle.

The workspace stage was about `295–317 ms` for two sequential workspaces and therefore diluted the fetch-stage gain. These numbers are exploratory local evidence, not a cross-host performance claim. Re-run on Linux before using them as scheduler policy evidence.

## Decision signal

This spike supports the current narrow direction:

- real MCP transport does not erase the benefit of yielding a running slot during known external I/O;
- the unoptimized workspace stage is now the larger part of this small end-to-end workflow;
- do not merge writable workspace state into durable replay merely to improve this benchmark;
- next measure workspace-aware live scheduling separately, with explicit lifecycle and authority semantics, only if real workloads frequently call tools from within the editing Guest.
