# Pysolate Workload Evaluation and Lab Contract Roadmap

> **For Hermes:** Read this file fully and use its checkboxes as the source of truth. Continue across verified slices; a successful slice is not a stop condition.

**Goal:** Use real local CPython/WASI workloads to test whether playback, branching, observation and CAS evidence improve reproducibility and diagnosis without expanding Guest authority, while freezing a stable Lab v1 read contract for a parallel Web UI.

**Baseline:** `d0788415238dc669ce10e3ad459f0fcdb700c5a2` on `main`, with the prior research-substrate roadmap closed 92/92.

## 1. Main claim and evidence class

Falsifiable main claim:

> For a versioned cohort of bounded local workloads, Pysolate can reproduce captured executions, run Host-selected counterfactuals and explain output/workspace differences from bounded evidence while preserving fresh-per-Run and Host-owned authority.

The first report is **mechanism-only**. It may support control-flow carriage, strict offline playback, branch lineage, evidence reuse and task-oracle claims. It must not claim model quality, token or latency benefit, placement share, Computer replacement, production readiness, arbitrary determinism or economic advantage.

## 2. Non-negotiable boundaries

- Preserve fresh-per-Run, sealed typed capabilities and Host-owned artifact/profile/workspace/plan/grants.
- No shell, subprocess capability, generic HTTP, Agent-selected URL/method/header/credentials, external writes, heap/WASM-memory snapshots, Docker, paid cloud or public-network tests.
- Use real CPython/WASI Guest executions, loopback curated sources and local files only.
- Do not revive archived provider/routing harnesses unless a selected workload proves a concrete blocker.
- Runtime core must not depend on Lab storage, Web UI, provider traces or conversation semantics.
- Evidence must not expose prompt/code bodies, provider bodies, workspace file bodies, Host paths, endpoints or credentials. Paths may appear only where the observation contract explicitly permits normalized Guest workspace metadata.
- Every code slice follows RED → minimal GREEN → focused race/full proportional gates → signed commit.
- Do not push merely because one slice passed. A schema milestone may be pushed once so the parallel Web session can consume it; later pushes are milestone-only after gates and review.

## 3. Parallel Web/Lab ownership

The Web session must use a separate Git worktree or separate repository. It may own UI code, generated client types and fixture-driven presentation. This roadmap owns Runtime evaluation, canonical Lab v1 schemas, Go producers/validators, fixtures and CLI.

The Web session must not edit:

- `runtime/**`
- `research/labstore/**`
- `research/operator/**`
- `schemas/lab/**`
- this roadmap

Before schema v1 is frozen, the Web session may prototype only against explicitly labelled draft fixtures. After freeze, additive compatible fields require fixtures and validation updates; required-field removal, meaning changes or enum changes require a new schema version.

## 4. Definition of done

- [ ] Three versioned workload families execute through the real Guest with exact task oracles.
- [ ] Each workload has direct/live capture, strict offline replay and at least one meaningful counterfactual treatment where semantically applicable.
- [ ] A canonical experiment plan and report schema bind Host commit, Guest artifact, corpus, treatment, evidence and prohibited claims.
- [ ] Lab v1 read schema is frozen, machine-validated and demonstrated by canonical fixtures.
- [ ] Lab v1 supports overview, run detail, timeline, branch DAG, workspace diff, comparison, object reference and bounded pagination/error metadata.
- [ ] The research CLI can emit or export the Lab v1 view without exposing protected bodies.
- [ ] Reports quantify oracle outcomes, replay equivalence, branch differences, evidence reuse, storage cost and execution phases without overclaiming.
- [ ] Exact final commit passes full Go/race/Python/real-Guest gates and independent review.

## 5. Track A — Freeze experiment and corpus contracts

- [x] Inventory reusable current fixtures, Bundle/observation/branch schemas and workload gaps.
- [x] Define `pysolate.workload-corpus.v1`: workload ID/version, code/input identities, required capabilities, workspace seed, treatments and oracle.
- [x] Define `pysolate.evaluation-plan.v1`: exact Host/Guest/corpus/profile identities, treatment order, repetitions, ceilings and prohibited claims.
- [x] Define `pysolate.evaluation-report.v1` with strict unknown-field rejection, exact row identity and conservation rules.
- [x] Add canonical positive and adversarial fixtures: unknown field, trailing JSON, duplicate ID, missing oracle, incompatible treatment and identity drift.
- [x] Document evidence vocabulary: `current`, `mechanism_only`, `qualified_workload`, `experimental_partial`, `not_measured`.

**Gate:** strict schema/model tests plus deterministic fixture regeneration.

## 6. Track B — Freeze Lab v1 read schema early

Create canonical JSON Schemas under `schemas/lab/v1/` and matching Go projection types outside Runtime core.

Required envelopes:

- [x] `lab-index.v1`: schema/version, source identity, generated-at policy, bounded links and capability flags.
- [x] `study-summary.v1`: cohort, workload/treatment counts, status totals, evidence class and prohibited claims.
- [x] `run-detail.v1`: invocation/execution refs, status, profile/artifact/plan refs, result/workspace refs and completeness.
- [x] `timeline-page.v1`: ordered bounded events, cursors, truncation and evidence-complete state.
- [x] `branch-dag.v1`: typed nodes/edges, fork operation, suffix mode, lineage refs and truncation.
- [x] `workspace-diff.v1`: normalized relative paths, change kind, sizes/executable bits/digests; never file bodies or Host paths.
- [x] `run-comparison.v1`: same/different dimensions, bounded call/workspace deltas and reason codes.
- [x] `object-ref.v1` and `problem.v1`: typed references plus fail-closed structured errors.
- [x] Define pagination, sorting, maximum sizes, optionality and forward-compatibility rules.
- [x] Add canonical fixtures for empty, ordinary, branched, incomplete-evidence, truncated and private-object cases.
- [x] Add cross-schema/link validator and deterministic fixture hashes.
- [x] Publish `docs/research/lab-v1-contract.md` with Current/Experimental/Proposed labels and Web integration rules.
- [x] Independent schema review passes before the first Web-consumption milestone push.

**Schema rule:** v1 is a read/presentation contract, not the LabStore disk format and not an authority token. Digests are references, not authorization.

## 7. Track C — Three real workload families

### C1 Structured source synthesis
- [ ] Combine both curated sources, filter/rank records and write a deterministic workspace report.
- [ ] Oracle verifies canonical result, workspace content identity and source-call count.
- [ ] Branch changes one captured source result and proves expected result/workspace delta.

### C2 Stateful local analysis
- [ ] Start from a seeded workspace dataset, perform multi-step local transformation and emit summary/index files.
- [ ] Oracle verifies exact final tree and semantic summary.
- [ ] Offline replay proves no network; deterministic profile is used only if the no-mounted-workspace restriction permits a separate equivalent probe.

### C3 Bounded planning/search
- [ ] Run deterministic multi-candidate scoring or bounded search using ordinary Python and typed inputs.
- [ ] Oracle verifies selected candidate, score trace summary and final evidence identities.
- [ ] Counterfactual treatment changes a Host-owned input/captured suffix, not Guest authority.

For every family:
- [ ] real Guest smoke and race coverage;
- [ ] exact corpus identity and negative tamper tests;
- [ ] direct/live, capture/offline and applicable branch treatments;
- [ ] no-body/no-credential evidence checks;
- [ ] explicit unsupported-treatment result rather than silent omission.

## 8. Track D — Evaluation runner and truthful measurements

- [ ] Deterministically expand plan rows with unique IDs and bounded repetitions.
- [ ] Execute setup outside timed phases and record lifecycle phases separately.
- [ ] Record offered/started/completed/failed/timed-out rows with exact conservation.
- [ ] Measure wall time as diagnostic only; do not present local macOS timings as universal performance claims.
- [ ] Derive replay equivalence, oracle pass rate, branch divergence, reused-object ratio, logical/stored bytes and evidence completeness.
- [ ] Preserve raw bounded rows privately and emit a portable digest-only summary.
- [ ] Encode `prohibited_claims` in every report and reject their removal or weakening.
- [ ] Rebuild the report independently from raw rows and compare exact canonical identity.

## 9. Track E — CLI and Lab projection bridge

- [ ] Add bounded local commands to ingest an evaluation report and emit Lab v1 index/study/run/timeline/DAG/diff/compare JSON.
- [ ] Add branch execution only if it can consume an explicitly sealed Host plan without adding authority; otherwise keep the API boundary and record the reason.
- [ ] Ensure read commands are read-only and mutation commands publish with exclusive protected semantics.
- [ ] Add `--json` output contracts, maximum output/cursor limits and structured problem responses.
- [ ] Verify canonical Web fixtures are generated through the same producer, not hand-maintained parallel shapes.

## 10. Track F — Fault and compatibility probes

- [ ] Lab v1 producer rejects corrupt, missing, private or incompatible relations fail-closed.
- [ ] Observation `required` failure invalidates evidence; `best_effort` remains visibly incomplete.
- [ ] Missing privacy metadata remains private and sidecar-before-object publication remains race-safe.
- [ ] Schema compatibility tests distinguish additive optional v1 changes from v2-required changes.
- [ ] Crash/orphan and cross-process-writer limitations are measured and documented; do not add SQLite until evidence justifies it.

## 11. Track G — Report, closeout and next decision

- [ ] Generate one private full report and one portable body-free summary from exact final source/artifact identities.
- [ ] Write `docs/research/workload-evaluation-v1.md` with methodology, results, negative evidence and threats to validity.
- [ ] Update product direction and Lab boundary without promoting Experimental work to Current.
- [ ] Decide from measured evidence whether the next step is filesystem recovery hardening, SQLite metadata, more workloads or a minimal Lab service.
- [ ] Full tests/vet/build/race/Python/real-Guest/docs/privacy gates pass on exact final candidate.
- [ ] Independent final review reports no blocker or unresolved major finding.
- [ ] Roadmap checkboxes and completion log are closed, signed commits are verified and remote/worktree state is exact.

## 12. Global gates

Use proportional focused gates after each slice. Before each milestone/final push run:

```bash
go test ./... -count=1
go vet ./...
go build ./cmd/...
go test -race ./... -count=1
python3 -m unittest discover -s tests -p 'test_*.py'
python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m compileall -q guest tests
AGENT_RUNTIME_GUEST=<qualified-wasm> go test -race ./integration/e2e -count=1 -timeout 20m
git diff --check
```

Also validate every Lab/evaluation JSON fixture against its schema, canonical regeneration hash, prohibited-claim policy, privacy scan and local Markdown links/fences.

## 13. Per-slice and tracking rules

For each code slice: inspect live state; write RED; run it and retain the expected failure; implement minimum GREEN; run focused race; update checkbox/completion log; run proportional global gate; create a signed commit; verify signature/status; continue.

Do not mark a checkbox complete from design intent or a zero-test match. Completion entries must name the command and real result. Keep private artifacts under ignored protected storage; never commit Host paths or sensitive bodies.

## 14. Stop conditions

Continue automatically after every verified slice. Stop only when:

1. all executable checkboxes and final gates are complete;
2. a genuine product/schema decision needs Yuzhe;
3. required artifact/resource/permission is unavailable with no safe local alternative;
4. repeated gate failure requires an architecture choice; or
5. proceeding would require authority expansion, unsafe rewrite or conflicting worktree ownership.

A successful slice, signed checkpoint, long test, context boundary or pending Web work is not a stop condition. On blocker, report exact files, tests, Git state and safest next decision.

## 15. Completion log

- 2026-08-12: Roadmap created from clean synchronized baseline `d078841`; implementation not started. First pointer: Track A inventory, then Track B Lab v1 schema freeze before parallel Web consumption.
- 2026-08-12: Track A frozen with mechanism-only corpus/plan/report contracts, typed captured-read requirements, fixed family admission invariants, bounded strict decoders, deterministic positive/adversarial fixtures and evidence vocabulary. Full Go/race/Python gates passed; independent review blockers were reproduced, fixed and post-fix reviewed PASS.
- 2026-08-12: Track B Lab v1 read contract frozen with nine closed schemas, matching strict Go projection/cross-set validator, six-case 54-document canonical fixture matrix and SHA manifest. Review probes found and closed timeout-enum, Windows Host-path and Go/schema bound/vocabulary drift; final post-fix review passed with zero findings.

## 16. Short prompt

```text
Read /Users/yuzhe/projects/agent-python-runtime/.hermes/plans/2026-08-12_095911-workload-evaluation-lab-contract-megagoal.md fully, then execute it on main in /Users/yuzhe/projects/agent-python-runtime. Do not stop after one slice: TDD, update checkboxes, signed commits, milestone-only pushes, and continue until complete or genuinely blocked. Freeze Lab v1 schema early for the parallel Web worktree; preserve fresh-per-Run and Host-owned authority, with no shell, generic HTTP, writes, paid cloud or public-network tests.
```
