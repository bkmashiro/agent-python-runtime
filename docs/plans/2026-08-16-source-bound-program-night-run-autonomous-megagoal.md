# Source-Bound Agent Program Night-Run Autonomous Mega-Goal

> **Status: completed historical night-run handoff.** It is not an active worker prompt. Active paper claims, benchmark selection and evaluation closeout are governed by the [Effect-Compiler Paper and Evaluation Source of Truth](2026-08-16-effect-compiler-paper-evaluation-source-of-truth.md).

> **For Hermes:** This is the long-running unattended `/goal` handoff. Read this file fully, inspect live Git state, finish the current dirty M2 closeout first, then implement Megagoal 3's Human-First Lab in roadmap order. Stop at its owner visual/product review gate; the later corpus/experiment tracks remain queued behind that gate.

**Goal:** Close Megagoal 2 truthfully from the live dirty worktree, then rebuild Lab around causal developer tasks using the real M2 evidence, verify the actual browser surface proportionally, and leave the later natural-workload/multi-agent experiments queued behind owner acceptance.

**Architecture:** Keep `pysolate.causal-evidence.v1` non-authoritative and Host-projected. Preserve the strict physical `production_rollback` profile and bounded/private `experiment_full` profile. M3 is a read-only deterministic projection: Session → Turn → Step → Tool/Run causal tree, source/workspace/evidence inspectors and honest absence states. It never reconstructs missing truth or gains authority. M4 corpus and physical-sharing experiments remain specified but blocked until M3 owner review.

**Tech stack:** Go, CPython/WASI real Guest, Labstore, TypeScript/Vite/Vitest/Playwright, Git signed commits.

---

## User Intent

Yuzhe is going to sleep and does not want to keep sending “continue.” The night run should:

1. finish the current M2 closeout rather than leaving a dirty evidence/schema checkpoint;
2. stop wasting large token budgets on Codex CLI whole-repository reviews;
3. implement Megagoal 3 Web/Lab redesign after M2 in the roadmap's intended order;
4. stop at owner visual/product review before beginning M4 corpus or multi-agent experiments;
5. keep making verified progress until the executable queue is exhausted or a real product/resource/risk decision is required.

## Unattended Authority Envelope

This is unattended work.

- Work only in `/Users/yuzhe/projects/agent-python-runtime` on `feat/programmatic-hot-approval`.
- Never merge the branch.
- Never deploy, publish packages, mutate production, use production accounts/data, or activate remote infrastructure.
- Make signed local commits for coherent verified slices. Do **not push during the unattended run unless Yuzhe's next goal explicitly grants unattended push authority**.
- Never expose or commit private artifacts under `~/.hermes/evidence/`.
- Do not use Codex CLI for review or exploration.
- Do not launch multiple writers against this worktree.
- At most one independent `delegate_task` reviewer may be used for a sharply bounded exact-diff question. Give it an explicit finding checklist and no broad gates. If it times out, do not recursively respawn reviewers; perform controller-owned source inspection and targeted adversarial tests instead.
- Local Docker, paid cloud, production access and external account mutations remain permission-gated.

## Value Filter and Explicit Non-Goals

### Prefer

- closing known correctness gaps with small adversarial tests;
- truthful artifact regeneration from exact signed behavior commits;
- controller-owned focused review of identity, authority, body and lifecycle boundaries;
- deterministic local gates and canonical checked-in public artifacts;
- concise roadmap/evidence updates that eliminate stale state;
- a coherent browser-visible causal debugging loop over real M2 fixtures.

### Do not do overnight

- repeated pixel-polish loops, exhaustive browser matrices or screenshot churn after one representative desktop and one narrow-viewport QA pass;
- rollback/replay/compensator execution;
- COW/snapshot/restore/cold continuation/region execution;
- production scheduling or durable workflows;
- a new optimizer pass;
- arbitrary source rewriting or new execution authority;
- broad speculative refactors prompted only by an unbounded reviewer;
- repeated whole-repository reviews whose output is not decision-bounded;
- merge, deploy, release or publish.

## Repository and Sources of Truth

Read first:

1. `docs/plans/2026-08-15-source-bound-agent-program-roadmap.md`
2. `docs/plans/2026-08-15-dual-profile-causal-evidence-workspace-megagoal.md`
3. `docs/research/dual-profile-causal-evidence-v1.md`
4. this file
5. live `git status --short --branch`, `git diff --stat`, `git diff --check`, `git log -5 --show-signature --oneline`

M2 implementation anchors:

- `research/trajectory/contract_v1.go`
- `research/trajectory/observe_adapter_v1.go`
- `research/trajectory/*_v1_test.go`
- `runtime/observe/observe.go`
- `runtime/engine/wazero/{engine.go,observation.go}`
- `runtime/capability/broker.go`
- `runtime/workspace/`
- `integration/e2e/{observation_test.go,semantic_source_binding_test.go}`
- `apps/lab-web/src/trajectoryData.ts`
- `docs/evidence/dual-profile-causal-evidence-real-guest-*-v1.json`
- `apps/lab-web/public/lab-data/*.json`

Exact real Guest artifact:

```text
~/.hermes/evidence/pysolate/source-bound-mg1-e79e821/dist/agent-python-runtime.wasm
```

## Current Live State at Handoff

Trust live Git over this snapshot. At roadmap creation time:

- baseline for M2 remains `28d46c8ab116b091382334d192747c22a8d83736`;
- latest signed/pushed behavior commit is `c4b510051528d1469a4d40b2034a72974ec98632`;
- that commit adds non-source receipt identity reconstruction, honest source-bound error/timeout/ambiguous terminal representation, post-call receipt capture on runtime failure, strict mechanism/approval semantics, canonical/depth-bounded Lab ingestion and Go/Lab parity hardening;
- a real Guest capture was generated from `c4b5100` under `~/.hermes/evidence/pysolate/dual-profile-mg2-c4b5100/`;
- capture identity is `sha256:5560ede54e5a3f59c1c5864441a54397b3d6386d9c21cf2b05f62211e72b3cfb`;
- event counts remain private 31, public 21, production 9;
- public/production body leakage check returned 0 and effect order remains `intent → started → committed`;
- captured file hashes are:
  - private `c004053ff30e3359eb8e961c2ac6b211d65a15f67b4fabc719d84cc9894f711b`;
  - public `7833cd7ce414be3b7c5768a33aa0276b0976974bf85621bd3421c351ea4a982a`;
  - production `81605cef42d275cc9238d1d50b11ad932dd2687bd0e5e91578f8a3770a00608a`;
- checked-in evidence/Lab files and `docs/research/dual-profile-causal-evidence-v1.md` were updated in the worktree but were not yet committed at handoff;
- full Go race, focused real Guest race, `go vet`, 95 Guest Python tests and 7 scripts tests passed after `c4b5100`;
- Lab gate currently has one expected test assertion drift: query `source_bound` now returns both `tool.decision` and `source.decision` because `ToolDecisionPayload.source_bound` became explicit. This is not an implementation failure; update the deterministic expectation, rerun Lab unit/build/E2E, and keep the new field because it is required to distinguish receipt v1/v2 from source-bound receipt v3 validation;
- earlier exact-target reviewer findings about missing body requirements and Lab byte bounds were fixed after their stale target;
- a later Codex CLI review over-expanded into a whole-runtime audit. Do not use Codex CLI again. Its actionable findings were reduced to concrete tests/fixes in `c4b5100`; architectural demands that standalone evidence cryptographically prove Host truth exceed M2's explicit non-authoritative Host-projection boundary and must not trigger an authority redesign.

## Desired M2 Closeout State

M2 is complete only when all are true:

- one current `pysolate.causal-evidence.v1` contract exists; no active v0 parser/fixture/dual-read path remains;
- private body-only events require private Labstore objects with kind/privacy/content identity validation;
- canonical export decode is bounded by bytes, payload, parents, events and JSON depth;
- Go and Lab reject unknown fields, noncanonical parents/raw JSON, invalid enums/ranges and relation/identity mismatches;
- source admission is independent of Broker terminal outcome and can represent pre-dispatch rejection plus committed/failed/timed-out/ambiguous receipt terminals;
- source-bound receipt v3 and non-source receipt v1/v2 identities are recomputed from typed Host fields;
- effect lifecycle is per-call, terminally reconciled, and a complete trace cannot leave intent/start/ambiguous outstanding;
- runtime failures after a capability call emit minted receipts before terminal failure evidence;
- subagent descriptor and workspace-root identities are recomputed from complete typed documents, and the named E2E derives them from actual Host descriptor/join/root objects;
- default production recording physically avoids private raw observation/resource/tool/body capture;
- named real Guest artifacts are canonical, exact-commit-bound, body-safe where checked in, and hash-documented;
- Lab remains read-only/non-authoritative;
- master and extracted M2 roadmaps contain no stale “M2 not started” or old capture identity;
- final working tree is clean after signed local closeout commits;
- no M3 code was started.

## Stop Conditions

Continue automatically after each verified slice. Stop only when:

1. M2 is closed and M3 is implemented, built, browser-smoked and ready for owner review;
2. M3 reaches its owner visual/product review gate;
3. a required resource/permission is unavailable;
4. a relevant gate repeatedly fails after bounded diagnosis and alternatives, and proceeding would require a risky broad rewrite;
5. the only remaining work is M4 experimentation, merge, deployment, release or another explicitly gated transition.

Do not bypass the M3 owner review gate merely to keep the run busy. Tracks E–H are the approved post-review queue, not executable during this unattended run unless Yuzhe explicitly accepts M3 first.

## Global Gates

Use focused checks while iterating. Run this full set once for M2 closeout and once for final M3 closeout; do not repeat the whole matrix after every small slice:

```bash
cd /Users/yuzhe/projects/agent-python-runtime

env -u AGENT_RUNTIME_GUEST -u PYSOLATE_EVIDENCE_OUTPUT_DIR -u PYSOLATE_EVIDENCE_SOURCE_COMMIT \
  go test -race ./... -count=1

go vet ./...

PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'

env -u PYSOLATE_EVIDENCE_OUTPUT_DIR -u PYSOLATE_EVIDENCE_SOURCE_COMMIT \
  AGENT_RUNTIME_GUEST="$HOME/.hermes/evidence/pysolate/source-bound-mg1-e79e821/dist/agent-python-runtime.wasm" \
  go test -race ./integration/e2e \
  -run '^(TestRealGuestObservationCorrelatesCapabilityCall|TestRealGuestProgrammaticReceiptBindsExactVerifiedSourceSpan)$' \
  -count=1 -v

cd apps/lab-web
npm test -- --run
npm run build
npm run test:e2e

cd ../..
git diff --check
```

Do not claim a Guest-enabled `go test -race ./...` gate; the intended evidence is no-Guest full race plus focused real Guest race.

## Autonomous Execution Queue

### Track A — Finish the Dirty M2 Checkpoint

**Product promise:** The current correctness hardening lands as one coherent, truthful evidence closeout rather than an uncommitted schema/artifact mismatch.

- [x] Inspect live status/diff and confirm no sibling writer owns the worktree.
- [x] Update `apps/lab-web/src/trajectoryData.test.ts` so the `source_bound` query deterministically expects both `tool.decision` and `source.decision`; do not remove `source_bound` from the typed tool payload.
- [x] Run Lab unit/build/E2E.
- [x] Run focused trajectory and Wazero gates if any code changed beyond the expectation.
- [x] Verify the four checked-in public/production files are byte-identical to the final capture projections.
- [x] Recompute public/production hashes and assert zero body/body-reference leakage.
- [x] Verify the private capture directory/files remain `0700`/`0600` and are not tracked.
- [x] Run `git diff --check`.
- [x] Create a signed local commit containing only the final artifacts, Lab expectation and evidence documentation.
- [x] Verify commit signature and retain the commit locally unless the newest owner goal explicitly authorizes unattended push.

### Track B — Controller-Owned Final M2 Review

**Product promise:** M2 closes against its actual authority/privacy/state contract, not against an unbounded request for a different system.

Review exact final diff from M2 baseline and inspect:

- [x] production physical allowlist and absence of body/private telemetry;
- [x] private body required/store/privacy/kind/content validation;
- [x] source decision admission vs terminal receipt outcomes;
- [x] receipt v1/v2/v3 reconstruction and raw call/Plan/source joins;
- [x] effect ordering, outstanding reconciliation and failure-path receipt retention;
- [x] subagent descriptor/context/source/workspace-root joins derived by the named Host E2E;
- [x] export byte/event/parent/payload/depth/canonical limits;
- [x] Go/Lab strict parity for checked artifacts;
- [x] recorder/Lab non-authority;
- [x] v0 clean break.

Rules:

- Do not use Codex CLI.
- Optional: one bounded read-only `delegate_task` with exact files and checklist. No broad gates, no full architecture archaeology, no stale target. If it times out, do not dispatch another.
- Findings must name an observable malformed export/runtime path accepted by current code and include `file:symbol`. Reject requests that merely demand cryptographic proof from a non-authoritative local evidence projection or require M3/M4 behavior.
- Fix blocker/high/medium findings with RED tests; low suggestions do not block closeout.

### Track C — Reconcile M2 Roadmaps and Stop Gate

**Product promise:** Future sessions see one accurate M2 status and cannot accidentally restart it or enter M3.

- [x] Update `docs/plans/2026-08-15-dual-profile-causal-evidence-workspace-megagoal.md` Phase 1 text to remove the stale temporary-v0 statement.
- [x] Update its Phase 3 exact final capture commit/header/root/hashes.
- [x] Mark Phase 5 complete only after all final gates are green.
- [x] Update `docs/plans/2026-08-15-source-bound-agent-program-roadmap.md` status from “M2 not started” to M1+M2 complete.
- [x] Mark only evidence-backed M2 tracks complete; preserve any intentionally deferred conceptual item with a precise explanation rather than silently checking it.
- [x] Set the master execution pointer to “M2 complete; executing M3 Human-First Lab; M4 waits for owner visual/product review.”
- [x] Re-run document search for stale active v0/M2-not-started/final-capture claims.
- [x] Run final global gates and `git diff --check`.
- [x] Make a signed local roadmap closeout commit and verify signature/status.
- [x] Do not merge; enter M3 only after the M2 closeout commit is clean and verified.

### Track D — Human-First Source-Bound Lab Debugger

**Status:** Review-ready at the owner stop gate on 2026-08-16. Public real-Guest and production views are browser-verified; private Labstore body resolution and before/after diff remain explicit follow-up choices rather than fabricated content.

**Product promise:** A developer can start from one real trace, understand its task/turn/step structure, select generated Python, follow the exact source-bound decision into Host authority/tool/effect/runtime/workspace evidence, and return to raw canonical records without reading a flat ID wall.

**Primary areas:**

- `apps/lab-web/src/`
- `apps/lab-web/public/lab-data/`
- existing Vite/Vitest/Playwright setup

**Slices:**

- [x] Inspect the existing component/data architecture and preserve the strict v1 parser; no second transformed truth store was added.
- [x] Introduce deterministic selectors/view models for Session → Turn → Step → Tool/Run grouping, collapsed start/end atoms and explicit relation navigation.
- [x] Replace the flat primary presentation with a causal tree and a focused detail workspace. Raw evidence remains available as an inspector rather than the default reading surface.
- [x] Add Overview, Input/Output, Code, Timeline, Workspace, Evidence and Raw views only where the selected event/group has truthful data; unavailable/not-recorded states remain visibly distinct.
- [x] Add a source viewer for program ranges and `source_bound`; executed-line availability is shown without inventing instrumentation.
- [x] Add workspace checkpoint/result inspection and explicit before/after body-unavailable information without reconstructing absent file bodies.
- [x] Add actor swimlanes with honest point semantics and clickable typed relations.
- [x] Keep IDs/digests copyable in Evidence while reducing them in the primary summary.
- [x] Preserve body safety: production/public fixtures never fetch or imply private body objects; Lab remains read-only and non-authoritative.
- [x] Ensure native keyboard-reachable controls, useful missing-body/error states and a coherent narrow layout.

**Testing discipline:**

- Add focused selector/interaction tests for new deterministic behavior; do not snapshot every component or duplicate parser tests already covered by M2.
- Run `npm test -- --run` and `npm run build` at coherent checkpoints, not after each CSS edit.
- Run Playwright once after the full interaction loop is connected.
- Start the built/local app and perform one real browser walkthrough against the real public and production fixtures. Check console errors and capture one representative desktop plus one narrow screenshot. Fix functional, overflow, hierarchy and accessibility blockers; do not spend the night on subjective micro-polish.
- Run repository-wide Go/Python gates only once at final M3 closeout unless M3 unexpectedly changes non-Web code.

**Required stop gate:** Leave a review-ready local surface, route, screenshots and concise review notes, then stop for Yuzhe's visual/product acceptance. Do not begin Track E before that acceptance.

### Track E — Freeze the Natural-Program Corpus Contract

**Product promise:** Subsequent measurements begin with bounded slices of existing public agent datasets, using explicit provenance/privacy/oracle classes; local collection supplements only missing Pysolate joins.

**Primary areas:**

- `research/trajectory/`
- `research/evaluation/` and existing workload/census packages
- `scripts/` for local-only collection entry points
- `docs/research/`

**Slices:**

- [x] Inspect dataset cards, licenses, schemas and small streamed samples before downloading; select CodeAct and Open-SWE for complementary executable-Python and task-oracle fields.
- [x] Select and identity-lock a deterministic bounded subset before bulk download: 50 CodeAct rows and the first ten Open-SWE rows, retaining the latter's mixed-language denominator rather than post-hoc filtering.
- [x] Audit existing local workload/corpus/evaluation schemas; reuse their body-safe/digest/oracle principles while keeping this internal pilot in one small script rather than expanding the fixed three-workload package.
- [x] Define `pysolate.natural-corpus-manifest.v1` with source digest, provenance class, collection adapter, oracle class, privacy class, authority requirements, expected Guest/backend and inclusion/rejection reason.
- [x] Require recomputed stable item identity, deterministic ordering, bounded sources/items and explicit `included`, `rejected`, `unclassifiable`, `truncated` states.
- [x] Keep raw dataset responses, selected code and probe stdout/stderr private under `~/.hermes/evidence/`; no local conversation was needed for this pilot.
- [x] Add adversarial tests for duplicate identity, digest mismatch, unbounded source, unknown class, private path/body leakage and denominator-dropping.
- [x] Write `docs/research/natural-corpus-pilot-v1.md` with exact source hashes, denominators, probe identity and claim limitations.

**Gate:** focused package tests, manifest round-trip/canonicalization tests and privacy scan; then proportional global gates and signed local commit.

### Track F — Capture Real Harness and Runtime Evidence

**Product promise:** Every selected program can be joined from the agent/Harness trajectory to Guest-accurate source, frozen Run authority, real execution/effect receipts and workspace terminal result.

**Slices:**

- [ ] Inspect locally available Hermes session/agent artifacts and choose only bounded examples with reconstructable provenance; never fabricate missing model/Harness data.
- [ ] Add a local-only importer/adapter that records source and external trajectory references by digest while keeping raw bodies outside Git.
- [ ] Execute a bounded cohort through the exact named CPython/WASI Guest and current Host tool plane.
- [ ] Record complete join coverage: corpus item → Harness/model step → source document/range → capability occurrence → Plan/receipt/effect → Run/attempt → workspace result.
- [ ] Record `not_recorded`, `unavailable` and `truncated` distinctly; never convert source-bound ranges into executed-line claims.
- [ ] Retain rejected and unclassifiable programs in denominators.
- [ ] Generate a canonical private result bundle and a body-safe aggregate report with exact source commit, Guest hash, environment identity and skip/failure reasons.
- [ ] Add replayable local commands/tests for the importer and aggregation logic; live external collection must remain explicit and bounded rather than a default test dependency.

**Stop rather than fake:** If no real local program artifact has enough provenance to join, preserve the importer/tests, document the exact missing source, and continue to any independent synthetic-control work while labeling it control-only.

### Track G — Falsifiable Logical-Agent/Physical-Guest Sharing Experiment

**Research question:** Can two or more logical child-agent requests safely share a physical Guest computation when—and only when—the Host proves equivalent frozen authority, source, prepared image, immutable input and workspace base, while preserving independent logical identities and terminal evidence?

**This is an experiment, not production enablement.** Do not change default scheduling, create a durable worker pool, inherit parent live state, or replay any call after an effect may have started.

**Cohorts:**

1. independent fresh Guest baseline;
2. identical logical children eligible for existing prepared-image/single-flight physical sharing;
3. near-miss authority mismatch;
4. workspace-base mismatch;
5. source/program mismatch;
6. cancellation/follower detachment;
7. started/ambiguous effect case that must not replay or coalesce unsafely.

**Required measurements:**

- logical child runs and physical Guest executions;
- task/oracle success;
- model turns/tokens when available from real Harness evidence;
- physical execution count, single-flight producer/follower relation and critical path;
- authority/freshness/workspace/source near-miss rejection reason;
- effect terminal/reconciliation state;
- private workspace result isolation;
- explicit evidence availability and truncation.

**Slices:**

- [ ] Write a versioned experiment manifest and deterministic control oracle before changing runtime code.
- [ ] Reuse existing source-bound pass/runner/single-flight seams; if no safe seam exists, run a read-only spike and stop before introducing a scheduler or new execution authority.
- [ ] Add RED adversarial tests proving every near-miss cohort is rejected from sharing.
- [ ] Implement only minimal experiment instrumentation/adapters needed to observe existing behavior; keep it behind explicit research configuration.
- [ ] Run real Guest paired cohorts with repeated bounded trials and preserve raw private evidence.
- [ ] Produce one aggregate JSON/Markdown report with denominators, confidence/variance where meaningful, negative results and exact artifacts.
- [ ] Independently verify report calculations from raw manifests using a second deterministic script/test path.

### Track H — Decision Record and Natural Stop

**Product promise:** The night run ends with evidence that determines the next discussion, not with a speculative production feature.

- [ ] Summarize natural corpus coverage and every provenance/oracle limitation.
- [ ] State whether physical sharing was observed, safe only under narrower conditions, unsupported by current seams, or not beneficial.
- [ ] Separate measured facts from proposed mechanisms.
- [ ] Apply the master pass acceptance gate to observed opportunities, but do not implement a new pass overnight.
- [ ] Recommend at most two next candidates; explicitly recommend “no new pass” if evidence is weak.
- [ ] Update the master roadmap with Current/Observed/Rejected status while preserving the completed/accepted M3 state.
- [ ] Run all relevant gates, sign the final local commit, verify status and stop at the owner decision gate.

**Required stop gate:** Do not cross from evidence into a new optimizer, scheduler, VM sharing mechanism, production default, M3 UI or paper claim without Yuzhe reviewing the measured result.

## Per-Slice Checklist

For every executable code slice:

1. inspect live state and relevant source;
2. write a RED test, or record why a generated-artifact/docs-only change cannot have one;
3. implement the minimum fix;
4. run focused gates;
5. update this roadmap immediately;
6. run proportional global gates;
7. create a signed local commit;
8. verify signature and Git status;
9. continue to the next executable slice.

A clean checkpoint, successful commit, reviewer timeout or context compaction is not a stop condition.

## Roadmap Tracking Rules

- This file was the night-run execution source of truth and is now closed.
- After each slice, change only evidence-backed `[ ]` to `[x]`.
- Add a completion-log line with date, commands/results and commit.
- Trust live Git over historical handoff text.
- Keep failures and stale reviewer targets visible until classified.
- Never mark M2 complete while artifacts/docs correspond to a different behavior commit.
- Never mark an experiment complete without real artifact identities, denominators and failure/skip accounting.

## Completion Log

- 2026-08-16 handoff: behavior hardening signed/pushed at `c4b5100`; final capture generated under `dual-profile-mg2-c4b5100`; Go/Python/real-Guest gates green; Lab has one deterministic expectation drift to update; final artifacts/docs are dirty and uncommitted. No M3 work started.
- 2026-08-16 M2 closeout: artifact/test checkpoint `104937b`; Lab 11/11 unit tests, build and 8/8 Playwright passed; public/production projections byte-match capture with zero body/private-event leakage; private root/files are `0700`/`0600`; controller review found no blocker/high/medium and removed only ignored local v0 runtime artifacts.
- 2026-08-16 M3 review surface: deterministic causal tree and truthful task inspectors implemented over the real 21-event public and 9-event production views; one focused desktop and one 390px visual pass found no blocked interaction or overflow; final Lab gate passed 14/14 unit tests, production build and 8/8 Playwright cases. Private body resolution/diff remains open at the owner stop gate; M4 was not started.
- 2026-08-16 M4 dataset-first pilot: downloaded 2.68 MiB privately (50 CodeAct + first ten Open-SWE rows), retained 137 CodeAct actions and ten mixed-language trajectories in denominators, selected eight top-level no-import programs deterministically, and observed 8/8 local plus 8/8 Host-profile-bound real Guest completion. No task-oracle, performance, sharing or optimizer claim was made.
- 2026-08-16 M4 corpus/sharing gate: strict 147-item body-safe manifest records 22 included and 125 rejected items, with all four terminal states exercised by tests; opportunity census found zero cross-record CodeAct duplicate groups and zero parallel Open-SWE bash messages. Sharing verdict is `insufficient_evidence / do_not_implement_sharing_pass`; no scheduler, coalescer or continuation mechanism was implemented.
- 2026-08-16 M4 next-experiment research: public multi-agent candidates inspected do not provide natural overlap plus Host authority/workspace/physical-execution joins; another internal sharing cohort would duplicate the existing shared-Guest and 20-program campaigns. `docs/research/m4-next-experiment-decision.md` selects only an `attrs-770` native RED/GREEN and Guest import/profile feasibility spike, with explicit stop gates before any package-shard design.
- 2026-08-16 M4 `attrs-770` spike: pinned base and exact Agent patch reproduced RED/GREEN under native CPython and an unbound private real-Guest probe; both Guest workspaces were unchanged and discarded with zero Host capability calls. The current verified artifact/profile rejected `attr` at artifact binding and undeclared package imports at source comparison before runner creation. Verdict is PARTIAL; no package/shard profile was implemented.
- 2026-08-16 M4 exact package profile: the separately authorized `attrs-770` artifact profile binds the locked source archive, private patch digest, final 20-file VFS tree, profile/import inventory and restricted-body operation qualification. The profile-bound natural oracle passed twice with zero Host calls and discarded empty workspaces; base-profile and undeclared-source controls failed before Guest start, while the declared sealed importer rejected an undeclared reflective `os` import. Verdict is SUPPORTED for this exact profile only; no generic installer, resolver, shard scheduler or sharing pass was added.

## Reporting Format When Finally Stopping

Report concisely:

1. exact satisfied stop condition;
2. signed local commits and whether any push was explicitly authorized/performed;
3. final evidence root/header/counts/hashes;
4. gates and real counts/results;
5. independent review verdict or controller review evidence;
6. clean/dirty Git status;
7. M3 route/screenshots and exact owner review gate;
8. confirmation that no merge/deploy/M4 experiment work occurred.

## Short Prompt to Start This Mega-Goal

```text
Read docs/plans/2026-08-16-source-bound-program-night-run-autonomous-megagoal.md fully and execute it in /Users/yuzhe/projects/agent-python-runtime from live Git state. Finish M2, then implement Megagoal 3's Human-First Source-Bound Lab through its review-ready browser surface; do not stop after one clean checkpoint, but stop at the owner visual/product review gate before Tracks E–H. Use focused tests while iterating and run full gates only once per Megagoal closeout—avoid repetitive test and screenshot churn. This is unattended: no Codex CLI, merge, deploy, publish, production access or push; do not start M4 experiments, a new optimizer/scheduler, or any production-default change.
```
