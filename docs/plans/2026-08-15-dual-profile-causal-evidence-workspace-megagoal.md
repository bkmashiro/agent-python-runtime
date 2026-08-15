# Megagoal 2 — Dual-Profile Causal Evidence and Workspace Contract

> **For Hermes:** Execute only this plan. The master architecture remains
> `docs/plans/2026-08-15-source-bound-agent-program-roadmap.md`. Stop before Lab UI redesign.

**Status:** Active

**Baseline:** `28d46c8ab116b091382334d192747c22a8d83736` on
`feat/programmatic-hot-approval`.

## Goal

Replace `pysolate.agent-trajectory.v0` with one current typed causal evidence
contract whose canonical identities support two explicit exports:

- `production_rollback`: a strict, body-free subset containing only Host facts
  required for unsafe-replay prevention, cleanup, typed compensation or
  reconciliation;
- `experiment_full`: bounded private capture of model/source/pass/tool/runtime/
  subagent/workspace/resource evidence, with a separate body-safe public export.

The Runtime remains authoritative for execution. Evidence is observation, never
a capability, replay, rollback or scheduling mechanism.

## Archaeology result and retained seams

- `runtime/observe` already provides bounded Host-authored physical-execution
  lifecycle, capability and workspace events. Keep it as a runtime producer and
  normalize it through one recorder adapter; it is not the durable aggregate
  browser contract.
- `runtime/engine.WithObservationSession` and wazero's observation lifecycle are
  the narrow real-Guest attachment seam.
- `runtime/receipt`, `runtime/capability`, `runtime/semantic`,
  `runtime/subagent` and `runtime/workspace` expose the Host identities needed
  for typed joins; evidence must copy projections rather than acquire handles.
- `research/labstore` already supplies private/portable content-addressed
  objects, bounds, typed links, roots, recovery and retention. Preserve the
  storage core and extend kinds/relations only where the new contract requires.
- `research/trajectory` is the v0 aggregate contract and must be replaced in
  place after consumers migrate. Its generic optional-field event shape,
  scripted fixture generator and browser v0 parser are not retained.
- `apps/lab-web/src/trajectoryData.ts` is an ingestion/validation seam only in
  this megagoal. Update it to the new schema without redesigning the UI.

## Frozen design rules

1. One event identity is canonical and profile-independent. Profile exports may
   omit events but cannot renumber, rewrite or reinterpret retained events.
2. Causal parents and typed relations name prior canonical event/object
   identities. Missing, future, duplicate and cross-trace references fail closed.
3. Production is an allowlisted strict projection. Experiment-only event kinds,
   body refs, source tracing, monitoring, provider details and generic timing
   must be physically absent, not merely hidden by a presenter.
4. `rollback` is valid only with a typed compensator and successful compensation
   evidence. Ambiguous or non-compensable effects end as
   `reconciliation_required`.
5. `source_bound`, `executed_line`, `not_recorded`, `unavailable` and
   `truncated` are distinct typed states.
6. Child model context/brief, prepared-image/fresh-Run identity and workspace
   base/result/delta are three independent relation planes. None implies parent
   live heap, Broker, approval or authority inheritance.
7. Bodies are private Labstore objects. Public exports may contain bounded typed
   metadata and portable refs only; credentials and private paths are forbidden.
8. Bounds cover encoded event bytes, event count, relation count, JSON nodes and
   depth, body bytes, workspace entries/files and exported trace size. Truncation
   is explicit terminal evidence; silent omission is invalid.

## Phase 1 — RED: contract, identity and profiles

**Status:** Complete. `pysolate.causal-evidence.v1` provides exact canonical decode, profile-independent event identities, prior-only causal links, strict production allowlisting, private body references through Labstore, body-free portable projection, configured event/parent/payload ceilings, explicit truncation evidence and a `0600` append/readback log. The old v0 API remains temporarily only for Phase 4 consumer migration and is not accepted by the v1 decoder.

Write failing tests for:

- exact-key canonical trace/header/event/export documents;
- stable event identities independent of export profile;
- prior-only causal links and deterministic ordering;
- unknown kinds/statuses/relations and malformed IDs fail closed;
- production allowlist and physical absence of all experiment-only fields;
- effect terminal matrix: committed, compensated, cleanup-only, ambiguous and
  reconciliation-required;
- typed absence/truncation and all configured bounds;
- public export rejecting private bodies, paths and credential-shaped fields.

Implement the minimum new `research/trajectory` contract and private append log.
Do not add runtime wiring until the focused tests are green.

Gate: focused trajectory/labstore tests, race, `git diff --check`; update this
plan; signed commit and push.

## Phase 2 — typed source, execution, subagent and workspace joins

**Status:** Complete. Typed payloads cover production authority/effect/workspace/attempt evidence, M1 source document/occurrence/decision/receipt joins, instrumentation-only executed-line claims and independent subagent context/runtime/workspace planes. Cross-plane identifiers and admitted receipt parents are validated; parent live-state inheritance and dishonest rollback states fail closed.

Add failing tests and minimal typed payloads for:

- M1 source document, static occurrence, dynamic occurrence, admitted/rejected
  decision and receipt linkage;
- runtime execution/authority/effect/workspace terminal evidence;
- child context capsule + brief identity;
- shared prepared-image + fresh child Run identity;
- parent immutable workspace root + private child result root + changed
  entries/bytes + Host select/discard;
- invalid cross-plane inference and mismatched Plan/receipt/root identities.

Only Host projections enter events. Do not expose runtime handles or add new
execution behavior.

Gate: focused contract/source/subagent/workspace/capability/receipt tests and
race; signed commit and push.

## Phase 3 — one real path and three products

**Status:** Complete. The named non-skipping CPython/WASI path records an actual source-bound Broker receipt, a fresh child Guest, private workspace branch/root, raw private Runtime observations, explicit executed-line absence and bounded CPU/wall resource truth. Header `sha256:cb45…9b8f` yields 27-event private full, 7-event production and 19-event public experiment products from shared retained identities; checked-in and private artifact hashes are documented in `docs/research/dual-profile-causal-evidence-v1.md`.

Implement one adapter from `runtime/observe.Recorder` into the new evidence log.
Drive a named real CPython/WASM execution through the existing observation
session and enrich it with Host-owned M1, subagent and workspace projections.
From the same canonical identities produce:

1. private `experiment_full` export;
2. minimal body-free `production_rollback` export;
3. body-safe public experiment projection.

The experiment profile enables every available bounded telemetry family,
including executed-line monitoring when supported, and records explicit
unavailable/truncated states otherwise. A skipped Guest test is not evidence.

Store raw/private artifacts outside the repository. Check in only compact
body-safe evidence with source commit/tree, Guest artifact SHA-256, commands and
identity joins.

Gate: non-skipping real-Guest E2E plus deterministic export/readback tests;
signed commit and push.

## Phase 4 — clean break and ingestion migration

**Status:** Complete. v0 log/tests, both scripted generators, workflow converter and checked-in v0 fixtures are removed. Active docs point to v1; Lab accepts only exact causal-evidence v1 plus its new view index, recomputes header/event/export identities, and passes unit/build/desktop+narrow ingestion gates. Historical result documents remain explicitly labelled and have no compatibility parser.

After Phase 3 is green:

- remove v0 schema code and dual-read paths;
- remove the scripted `trajectory-fixture` generator and superseded checked-in
  v0 trajectory/experiment fixtures;
- migrate or replace current workflow experiment capture to the new contract;
- update Lab TypeScript ingestion/validation and tests only; no UI redesign;
- update current docs that claim v0 is active, leaving clearly labelled
  historical reviews/results intact.

Gate: no active `pysolate.agent-trajectory.v0` or trajectory-index-v0 parser;
Lab unit/build/ingestion pass; signed commit and push.

## Phase 5 — verification, review and stop gate

Required gates:

```text
go test -race ./...
go vet ./...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
apps/lab-web: npm test; npm run build
non-skipping named real-Guest evidence test
git diff --check
```

Run an independent bounded review of profile leakage, effect terminology,
Guest/body injection, identity joins, ordering/bounds/truncation, public safety
and recorder non-authority. Fix blockers, rerun proportional/full gates, update
master status, sign, push and stop.

## Stop gate and non-goals

Report the new contract, exact production allowlist, experiment capture,
source/subagent/workspace joins, three named products, deleted v0 surface,
verification and remaining risks.

Do not enter Megagoal 3, redesign Lab UI, implement compensation/replay, fork a
parent live runtime, add optimizer passes, cold continuation, snapshot/region
execution, production scheduling or merge the branch.
