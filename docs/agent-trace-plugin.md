# Harness Agent trace/replay plugin

Status: optional development/research infrastructure. The Runtime remains a bounded Python executor and does not own provider parsing, Agent loops, direct tools, conversation state, or the trace database.

## Dependency direction

```text
Harness / Host / Verification
  ├── claimmanifest
  │     ├── agenttrace.Playback
  │     └── runtime.ExecutionRef
  └── agenttrace plugin
        └── Runtime execution_ref + receipts
```

`runtime` imports neither `agenttrace` nor `claimmanifest`. Both Host-side layers can therefore be removed without changing Runtime execution semantics. See [ADR 0009](adr/0009-co-designed-monorepo-boundaries.md).

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

`agenttrace.OpenSQLiteStore` creates a private `0600` SQLite database. It enables foreign keys, WAL, bounded busy timeout, and schema-integrity verification. `OpenSQLiteStoreReadOnly` opens an existing store without migrations, permission changes, or append authority. The store enforces:

- unique `(agent_run_id, sequence)` order;
- unique immutable event IDs;
- event digest and identifier validation before append;
- bounded ordered queries;
- contiguous-sequence validation in `LoadPlayback`;
- `ForkAt` plans anchored to an exact event and state fingerprint;
- integrity digests over ordered event, payload, and state identities.

`LoadPlayback` is a **structural recorded playback**: it validates and returns the immutable normalized event stream without contacting a provider or executing tools. `ForkAt` proves a branch origin and prefix digest. Re-executing a counterfactual branch still requires the Harness to supply a compatible checkpoint state. Exact provider-output replay is intentionally unavailable in metadata-only mode.

## Claim Manifest projection

`claimmanifest.FromMetadataPlayback` and `hermesbridge.TraceManager.ClaimManifest` project an integrity-checked playback plus the matching Host-authored `ExecutionRef` into `claim-manifest/v2`.

`v2` adds the required `completed_event_id` and strict successful-completion/evidence-graph binding. The earlier `v1` wire shape is no longer emitted or accepted by the current validator; callers must rebuild it from the integrity-checked playback rather than relabeling old JSON.

The manifest contains one claim per `artifact`, `base`, `authority`, `execution`, `effect`, and `outcome`, with explicit dependencies and one verifier status: `verified`, `contradicted`, `insufficient`, or `stale`. In the current metadata-only adapter:

- only one canonical `runtime.completed` payload with exact non-null typed fields, exact `status: "ok"`, matching execution identity, and the versioned Harness completion fields may support the execution claim; failed, unknown-status, duplicate-key, aliased, unknown-field, null-scalar, or duplicate-matching-completion payloads are rejected;
- `CompletedEventID`, playback digest, code digest, claim IDs, statuses, dependencies, and evidence references are bound to the exact generated graph rather than accepted as free-form labels;
- exact executed-code digest and successful execution-reference observation are `verified` structural claims;
- runtime base, authority binding, effects, and semantic outcome remain `insufficient`;
- maximum qualification is `structural-only` (R0);
- `RequireReplay(R1)` and `RequireReplay(R2)` fail with insufficient evidence;
- editing the serialized qualification to R1 or R2 fails manifest validation because the required evidence classes are absent.

A digest proves identity or trace integrity only. It does not prove input capture, deterministic dependencies, provider final state, semantic correctness, or outcome equivalence.

Minimal API:

```go
store, err := agenttrace.OpenSQLiteStore("/absolute/agent-trace.sqlite")
if err != nil { /* handle */ }
defer store.Close()

plugin := agenttrace.Plugin{
    Mode: agenttrace.ModeRequired,
    Sink: store,
}
ctx, err = agenttrace.WithPlugin(ctx, plugin)
result, err := agentic.RunDevelopmentTrialForModelWithIdentity(/* ctx, ... */)

playback, err := store.LoadPlayback(ctx, result.TrialID)
fork, err := playback.ForkAt(12, "counterfactual-run-1")

// The Harness owns compatible checkpoint bytes and continuation semantics.
forkRecorder, forkEvent, err := plugin.BeginFork(ctx, fork, nil)
```

The development pilot exposes the same wiring:

```bash
apyrun-agentic-pilot \
  ...existing-authorized-arguments... \
  -trace-mode required
```

The database is written to `<out>/agent-trace.sqlite`; the manifest records `trace_mode` and the relative `trace_path`. Default mode is `off`.

### Read-only operator CLI

`apyrun-agent-trace` never opens append authority. Its JSON outputs are versioned as `agent-trace-query/v1`:

```bash
apyrun-agent-trace --db /absolute/agent-trace.sqlite --op runs
apyrun-agent-trace --db /absolute/agent-trace.sqlite --op events --run RUN_ID --after 0 --limit 100
apyrun-agent-trace --db /absolute/agent-trace.sqlite --op stats --run RUN_ID
apyrun-agent-trace --db /absolute/agent-trace.sqlite --op export --run RUN_ID --out /absolute/events.jsonl
apyrun-agent-trace --db /absolute/agent-trace.sqlite --op fork --run RUN_ID --sequence 12 --new-run CHILD_RUN_ID
```

Export files are created exclusively with mode `0600`; existing files are never overwritten. `stats` validates the complete playback before reporting duration, payload bytes, event-type counts, and an integrity digest. `fork` emits a metadata-only `ForkPlan`. `Plugin.BeginFork` records `agent.fork.started` as the child run's first event before a Harness-owned continuation callback writes later events. This proves lineage and append integrity; it does not restore checkpoint bytes or execute the branch by itself.

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
