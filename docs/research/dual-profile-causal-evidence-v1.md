# Dual-profile causal evidence v1

Status: **Implemented experimental contract, real Runtime attachment and body-safe Lab ingestion**

## Contract

`pysolate.causal-evidence.v1` is the single current durable trajectory contract. Every event has a canonical profile-independent identity, a strictly typed payload, prior-only causal parents, an ordinal and a Host-authored actor. A sealed export binds the trace header, exact event subset, profile and privacy class.

Two capture profiles share the same trace/header/event identities:

- `production_rollback` is a body-free allowlisted subset for replay prevention, effect reconciliation, cleanup, workspace disposition and terminal execution truth. It admits only trace lifecycle, execution attempt, authority snapshot, effect transition, workspace terminal and explicit truncation events.
- `experiment_full` is private-by-default and may retain model, source, Runtime observation, subagent, workspace and resource evidence under explicit event/parent/payload/Labstore limits. Its portable projection removes every body-bearing event rather than hiding a body in the UI.

`ambiguous` and non-compensable effects are never called rolled back. They remain `reconciliation_required`; a `compensated` transition requires a typed compensator identity.

## Source and execution claims

The contract keeps three different claims separate:

- `source.document`, `source.occurrence` and `source.decision` bind a verified static M1 occurrence to a dynamic capability occurrence, Plan and Host receipt;
- `source.executed_line` exists only when actual runtime instrumentation produced the event;
- availability values distinguish `available`, `not_recorded`, `unavailable` and `truncated`.

A source-bound receipt is therefore not an executed-line claim. Source decisions cross-bind their occurrence and committed effect/receipt through typed causal parents.

## Subagent inheritance planes

A child is represented by three independent event planes:

1. `subagent.context`: an explicitly materialized context and parent-authored brief;
2. `subagent.runtime`: a fresh child Run from an authority-free prepared image and attenuated child Plan, with `parent_live_state_inherited=false`;
3. `subagent.workspace`: an immutable parent root, private result root/delta and explicit selected/discarded disposition.

Model-context reuse, prepared-memory COW and filesystem branching have independent identities. None implies either of the others. The current filesystem branch is a semantic private branch backed by a materialized directory copy; it is not claimed as APFS/reflink/overlayfs COW.

## Storage and privacy

`research/trajectory` writes a canonical `0600` JSONL evidence log. Complete opt-in bodies remain separate private typed `research/labstore` objects. Body references are allowed only on body-only experiment events; body-free metadata events remain independently portable so public causal paths do not depend on omitted private nodes.

Host ceilings cover event count, parent count, payload bytes and Labstore object/graph bounds. Overflow blocks export until an explicit `evidence.truncated` event is recorded; a truncated trace cannot claim `evidence_complete=true`. Unknown fields, malformed identities, forward parents, relation mismatches and noncanonical JSON fail closed.

## Real evidence and Lab

The named real-Guest run at capture commit `049c1dba2bed993768b71a2f6ec4f388f346bf73`
uses CPython/WASI artifact `sha256:664077c1d63445ec267b1b30e30ce31c72e7038d62a08fe1682c675a64cff257` and executes:

- a fresh child CPython/WASI Guest over a private workspace branch;
- an M1 source-bound programmatic capability call in the parent Guest;
- the actual Runtime observation attachment carrying the Host receipt/source binding.

It produces three views from header
`sha256:3a6967bdf380f409988d1b4c7dfee399ec7562bb1973c0ab98d121ee55c329e8`
and shared retained event identities:

- local private `experiment_full`: 31 events plus Labstore objects at
  `~/.hermes/evidence/pysolate/dual-profile-mg2-049c1db/`, export file SHA-256
  `7d05e08b298f0f7b6117bd1024d38320ad2f04c1cfca7f41a53568c5bc5501bb`;
- checked-in 9-event production projection
  `docs/evidence/dual-profile-causal-evidence-real-guest-production-v1.json`, file SHA-256
  `a6b8680837e14238f454fa3c4170024d88e4356dab801e715f93067d743b6ccb`;
- checked-in 21-event body-safe experiment projection
  `docs/evidence/dual-profile-causal-evidence-real-guest-public-v1.json`, file SHA-256
  `63808594309122f082015d34dcf740ef7d26c765ab6f463a9c71bfb0aac73af0`.

The private trace contains resolvable `blob.code` and `blob.file` references for the exact child
program and the captured `child.txt` output; contract validation resolves each private object and
checks its typed kind plus declared content digest before accepting the event. Both body-only
events are physically absent from portable exports. The production projection now carries true
live Broker markers in the causal
order `intent → started → committed`; the observer is body-free and non-authoritative. A typed
`tool.decision` joins programmatic mechanism, Broker outcome, approval disposition,
argument/result digests, Plan and receipt; `model.output:not_recorded`
explicitly avoids claiming a provider output for this deterministic Harness run. The full
experiment trace records a bounded process-CPU/wall sample and an explicit
`source.executed_line` availability of `not_recorded`; it does not upgrade the static source-bound
receipt into a dynamic line claim.

Pysolate Lab accepts only portable v1 exports, recomputes header/event/export identities in the browser and presents the two checked-in views. It remains static, read-only and non-authoritative: it cannot execute, retry, replay, schedule, grant or publish.
