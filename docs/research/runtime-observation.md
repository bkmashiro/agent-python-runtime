# Runtime observation contract

Status: **Current** for the bounded `pysolate.runtime-observation.v1`
contract and its wazero lifecycle integration. A durable recorder, research
database, syscall trace, and internal Python trace are not Current.

## Boundary and ownership

Runtime observation is optional Host evidence. The Host creates an
`observe.Session`, selects its failure mode and Recorder, and attaches it to a
Run context. Runtime does not select a store, start a background exporter, or
accept observation configuration from Agent Python.

An enabled session is admitted only when its `execution_id` exactly matches the
Host `InvocationRef` on the same context. The Host-authored `ExecutionRef` in
the final response carries `agent_run_id`, turn/item/segment coordinates,
logical `invocation_id`, one-based `invocation_attempt`, physical
`execution_id`, and the digest of the exact executed code. A recorder or future
Lab joins that response relation to observation events by `execution_id`.
Those Harness coordinates are not duplicated into every event.

The Guest cannot author an observation event or override `ExecutionRef`.
Observation is evidence about an admitted execution; it grants no capability
and does not change the sealed capability Plan.

## Version 1 envelope

Every accepted event is canonical JSON with these exact fields:

- `schema_version`: `pysolate.runtime-observation.v1`;
- `execution_id`: the Host physical-attempt identity;
- `sequence`: a one-based, gap-free sequence within that execution;
- optional `parent_sequence`: an earlier successfully appended event in the
  same session;
- `type`: one of the closed event types below;
- `payload`: the exact typed payload for that event.

The session accepts at most 1,024 events. A payload is at most 16 KiB and an
encoded event is at most the payload limit plus 2 KiB. Encoding and decoding
reject invalid UTF-8, duplicate keys at any depth, folded aliases, unknown or
missing fields, explicit nulls, trailing data, non-canonical whitespace or key
order, malformed digests, and invalid causal parents. Payloads, parent values,
Recorder inputs, and returned events are defensively copied.

Cross-event parent existence is a Session invariant. The standalone v1 decoder
can prove only that a declared parent sequence is lower than the event's own
sequence; a Recorder or Lab loading separate envelopes must validate the
complete per-execution sequence and causal relation before trusting it as a
stream.

## Current event types

| Type | Current evidence |
|---|---|
| `execution.started` | Exact executed-code digest plus optional artifact, execution-profile, deterministic-profile, and capability-Plan references. The current wazero path emits artifact, code and profile references at start. |
| `capability.plan_bound` | The sealed Host capability-Plan identity for a Run with a Broker. It is emitted after Broker creation even when the Guest makes no capability call. |
| `capability.call` | Capability name, zero-based operation index, canonical argument digest, outcome, and optional Plan/result digests projected from Host receipts after Broker validation. It is transcript order, not wall-clock timing. |
| `workspace.finalized` | Initial and final workspace identities, final tree identity, final file/byte counts, and up to 128 path-sorted file additions, removals, or modifications. `changes_truncated` reports overflow and `syscall_order_available` is always `false`. |
| `execution.completed` | `ok`, the canonical final-result digest, and whether the event stream is complete. |
| `execution.failed` | A bounded Host error class, optional result digest, and whether the event stream is complete. |

A Run without a Broker still emits the start and terminal lifecycle events when
observation is enabled. A mounted-workspace Run emits its final-state event
before its terminal event. Admission failures that occur before
`execution.started` are not represented as a started execution.

Large or private bodies do not belong in these events. Capability results live
in protected Playback Bundles when capture is enabled; workspace content lives
in protected workspace artifacts or a separately governed research store.
Digests establish equality with later-provided bytes, not their correctness or
authorization.

Version 1 has no generic LabStore-object field and does not encode a privacy
classification. A future Lab may create a separate protected relation from an
event coordinate to a typed body reference, but it must not inject the body
into the v1 event or treat a digest as export permission.

## Recorder modes

| Mode | Behavior |
|---|---|
| `off` | Performs no payload validation and no Recorder work. Runtime behavior is unchanged. |
| `best_effort` | A rejected append does not consume a sequence number and does not fail the Run. The session becomes incomplete, and a later terminal event cannot claim complete evidence. If `execution.started` itself is rejected but the Recorder later recovers, Runtime may emit a parentless sequence-1 terminal event with `evidence_complete=false`; it does not fabricate the missing start event. |
| `required` | A rejected append does not consume a sequence number and fails the evidence-producing Run path. It invalidates research evidence; it does not grant authority, undo an already completed Guest mutation, or create transaction semantics. |

Recorder `Append` is synchronous. A Session serializes concurrent appends so
accepted sequences remain gap-free. Runtime owns neither retry nor persistence
policy; those belong to the Recorder/Harness outside the engine.

## Measured wazero visibility decision

The repository currently pins wazero `v1.11.0`. The measurement found three
relevant boundaries:

1. Runtime stably owns Guest lifecycle calls, the `agent_runtime_v1.host_call`
   capability boundary, validated Host receipts, and workspace-manager
   snapshots. These can be observed without inspecting Guest internals.
2. wazero exposes function callbacks through
   `experimental.FunctionListener`. Those callbacks expose raw WebAssembly
   function parameters/results and permit memory inspection. They are an
   experimental API, not a stable semantic WASI event contract. Turning them
   into paths, byte counts, or errno semantics would couple evidence to WASI
   ABI decoding and Guest memory.
3. The mounted-filesystem extension is also under wazero's experimental system
   interfaces. Wrapping it would expose implementation-level filesystem calls,
   not a proven complete, stable ordering of the Guest's semantic file
   operations.

The Current decision is therefore to use only the first boundary. Pysolate
records Host lifecycle and capability evidence plus bounded initial/final
file-level workspace deltas. It does **not** record syscall order, open/read/
write chronology, transient file states, unchanged-file reads, or a complete
WASI trace. `workspace.finalized.syscall_order_available=false` is a contract,
not a temporary display choice.

No claim is made about Python bytecode, local variables, stack frames, heap
objects, WebAssembly memory, or every control-flow branch. The function
listener was not installed and wazero was not forked.

## Privacy and interpretation

Observation payloads omit code, prompts, capability result bodies, workspace
file bodies, endpoints, headers, and credentials. Metadata is not automatically
public: an `execution_id`, workspace path, digest, count, or error class can
still be sensitive in context. The Host Recorder must apply access, retention,
and export policy before a future Lab exposes events.

An event digest is integrity evidence, not authentication. A complete event
stream is not proof of semantic correctness, and an observation trace is not
deterministic replay.

## Compatibility and upgrades

Version 1 is exact-key and canonical. Adding, removing, renaming, retyping, or
changing the meaning or bounds of a field requires a new schema version. A v1
reader must reject, not ignore, unknown versions or fields; case folding and
best-effort reinterpretation are forbidden.

There is no in-place event upgrade in Runtime. A future Lab migration may read
protected v1 bytes with the v1 decoder and author a new-version object with a
new content identity and an explicit `derived_from` relation. It must retain
the original bytes/identity while required by policy and must not rewrite a v1
event under its existing identity. Mixed-version streams must be partitioned
or explicitly related rather than concatenated as though they were one v1
Session.

See [lab-boundary.md](lab-boundary.md) for storage ownership and
[../playback-bundles.md](../playback-bundles.md) for Bundle and branch
compatibility.
