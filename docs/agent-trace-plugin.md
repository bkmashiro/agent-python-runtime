# Harness Agent trace/replay plugin

Status: optional development/research infrastructure. The Runtime remains a bounded Python executor and does not own provider parsing, Agent loops, direct tools, conversation state, or the trace database.

## Dependency direction

```text
Harness / eval/agentic
  └── agenttrace plugin
        └── Runtime execution_ref + receipts
```

`runtime` does not import `agenttrace`. The plugin can therefore be removed without changing Runtime execution semantics.

## Runtime correlation contract

A Host may attach `runtime.InvocationRef` with `engine.WithInvocationRef` before `Runner.Run`:

- `agent_run_id`, `turn_seq`, `output_item_seq`, and `segment_seq` locate the source Harness output;
- `invocation_id` identifies one logical invocation;
- `invocation_attempt` starts at 1 and increases only when the same logical invocation is retried;
- `execution_id` uniquely identifies one Host execution attempt.

The reference is Host context, not Guest input. It is absent from `RunRequest`, Guest globals, and the Python environment. Guest request `run_id` remains untrusted and is not used as execution identity.

After strict Guest response validation, the Host projects:

```json
{
  "execution_ref": {
    "agent_run_id": "...",
    "turn_seq": 0,
    "output_item_seq": 2,
    "segment_seq": 0,
    "invocation_id": "...",
    "invocation_attempt": 1,
    "execution_id": "...",
    "executed_code_sha256": "sha256:..."
  }
}
```

`executed_code_sha256` hashes the exact UTF-8 bytes of decoded `RunRequest.code`; in wrapped/compacted paths it therefore describes the effective executed code, not the original model segment. A Guest-supplied `execution_ref` is rejected. When capabilities are enabled, every receipt `run_id` must equal `execution_id`; the capability broker is also created with this Host identity. The same identity is the transaction run identity.

Without Host provenance, existing CLI and API behavior is unchanged and `execution_ref` is omitted.

## Portable event contract

`agenttrace.Event` is append-only and versioned as `agent-trace-event/v1`. Each event contains:

- stable event/run identifiers and a monotonically increasing per-run sequence;
- event type and observation time;
- parent event ID;
- canonical metadata payload plus SHA-256 digest;
- optional state fingerprint.

The implemented normalized event types cover run start/completion, route selection, LLM request/response, parsed output observation, direct-tool start/completion, Runtime invocation start/completion, checkpoint creation, and final state.

Portable payloads are metadata/digest-only. Validation rejects (case-insensitively) fields named `prompt`, `developer_prompt`, `tool_surface`, `request_body`, `response_body`, `raw`, `arguments`, `observation`, `code`, or `content` at any nesting level. The plugin does **not** persist prompts, provider bodies, reasoning, tool arguments, Python source, observations, attachments, credentials, or raw diagnostic artifacts.

Raw diagnostic capture remains a separate 0700/0600 development artifact path. Adding encrypted raw capture, retention policies, or blob-backed provider playback is a separate privacy and publication decision.

## Failure modes

`agenttrace.Plugin` supports:

- `off`: no recorder and no persistence;
- `best-effort`: append failures increment a dropped counter but never change the Agent result;
- `required`: append failures fail the Agent run.

Tracing is observer-only: event data never enters capability grants, policy, approval, transaction policy, routing, prompts, or tool inputs.

## SQLite store and playback

`agenttrace.OpenSQLiteStore` creates a private `0600` SQLite database. It enables foreign keys, WAL, bounded busy timeout, and schema-integrity verification. The store enforces:

- unique `(agent_run_id, sequence)` order;
- unique immutable event IDs;
- event digest and identifier validation before append;
- bounded ordered queries;
- contiguous-sequence validation in `LoadPlayback`;
- `ForkAt` plans anchored to an exact event and state fingerprint.

`LoadPlayback` is a **structural recorded playback**: it validates and returns the immutable normalized event stream without contacting a provider or executing tools. `ForkAt` proves a branch origin and prefix digest. Re-executing a counterfactual branch still requires the Harness to supply a compatible checkpoint state. Exact provider-output replay is intentionally unavailable in metadata-only mode.

Minimal API:

```go
store, err := agenttrace.OpenSQLiteStore("agent-trace.sqlite")
if err != nil { /* handle */ }
defer store.Close()

ctx, err = agenttrace.WithPlugin(ctx, agenttrace.Plugin{
    Mode: agenttrace.ModeRequired,
    Sink: store,
})
result, err := agentic.RunDevelopmentTrialForModelWithIdentity(/* ctx, ... */)

playback, err := store.LoadPlayback(ctx, result.TrialID)
fork, err := playback.ForkAt(12, "counterfactual-run-1")
```

The development pilot exposes the same wiring:

```bash
apyrun-agentic-pilot \
  ...existing-authorized-arguments... \
  -trace-mode required
```

The database is written to `<out>/agent-trace.sqlite`; the manifest records `trace_mode` and the relative `trace_path`. Default mode is `off`.

## Boundary summary

The trace plugin can correlate:

```text
LLM exchange metadata/digests
  -> parsed output coordinates
  -> routing decision
  -> Direct tool or Runtime execution
  -> execution_ref / receipt and transaction identity
  -> checkpoint/final-state digest
```

It cannot reconstruct prompt text, provider output text, Python source, tool arguments, or checkpoint bytes from the portable store. That limitation is deliberate rather than a missing Runtime responsibility.
