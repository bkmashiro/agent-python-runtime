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

The named real-Guest run at capture commit `a7c9f94ddaeed8411ffcac28d5868cb8fdca7f65`
uses CPython/WASI artifact `sha256:664077c1d63445ec267b1b30e30ce31c72e7038d62a08fe1682c675a64cff257` and executes:

- a fresh child CPython/WASI Guest over a private workspace branch;
- an M1 source-bound programmatic capability call in the parent Guest;
- the actual Runtime observation attachment carrying the Host receipt/source binding.

It produces three views from header
`sha256:b6859981dcebccae4844eab16e8fbab0d3c6b11f5bd73faa0fe9c6821ad2fd45`
and shared retained event identities:

- local private `experiment_full`: 31 events plus Labstore objects at
  `~/.hermes/evidence/pysolate/dual-profile-mg2-a7c9f94/`, export file SHA-256
  `3cd11184f51f16bc1749d4757641a5861c820a0ddf4509e3c60a045eb140bf6e`;
- checked-in 9-event production projection
  `docs/evidence/dual-profile-causal-evidence-real-guest-production-v1.json`, file SHA-256
  `165e7aab2224810fb15b5f8aaf5e66762507e92c9fe3aa505651812b3573e843`;
- checked-in 21-event body-safe experiment projection
  `docs/evidence/dual-profile-causal-evidence-real-guest-public-v1.json`, file SHA-256
  `393b15c2ea58cf4b4ad075e935b92118c75494bf4fc647a224ea07d1a19ae97b`.

The private trace contains resolvable `blob.code` and `blob.file` references for the exact child
program and the captured `child.txt` output; both body-only events are physically absent from
portable exports. The production projection now carries true live Broker markers in the causal
order `intent → started → committed`; the observer is body-free and non-authoritative. A typed
`tool.decision` joins programmatic mechanism, Broker outcome, approval disposition,
argument/result digests, Plan and receipt; `model.output:not_recorded`
explicitly avoids claiming a provider output for this deterministic Harness run. The full
experiment trace records a bounded process-CPU/wall sample and an explicit
`source.executed_line` availability of `not_recorded`; it does not upgrade the static source-bound
receipt into a dynamic line claim.

Pysolate Lab accepts only portable v1 exports, recomputes header/event/export identities in the browser and presents the two checked-in views. It remains static, read-only and non-authoritative: it cannot execute, retry, replay, schedule, grant or publish.
