# Semantic Speculation Optimizer Autonomous Mega-Goal

> **For Hermes:** This is the active long-running `/goal` handoff for the semantic-speculation research lane. Read it fully, inspect live Git state, and continue through successive verified slices. Do not stop after a test, commit, phase, or negative micro-result. Stop only at a named architecture/evidence gate that genuinely requires Yuzhe's decision, an unavailable resource/permission, an unsafe broad rewrite, or complete closeout.

**Goal:** Determine and implement the largest evidence-backed set of safe generation-time optimisations that Pysolate's source-bound semantic layer can support beyond syntax-level EAGER gating, while preserving target-CPython authority, Host-owned effect policy, explicit physical/logical evidence, and fail-closed fallback.

**Architecture:** The exact target CPython remains parser and executor. Guest analysis produces authority-free, source-located semantic facts. The Host joins those facts with frozen capability, effect, freshness, privacy, grant and speculation contracts. Overlay-only consumers use existing dynamic Host boundaries and leave final Python unchanged. A pure-region consumer may be introduced only after the opportunity gate; it must use an exact source-bound Guest-emitted execution patch over a fully parsed final program, with the original source retained as authority and provenance.

**Tech stack:** Go Host/runtime, CPython/WASI Guest, Python AST in the exact Guest, existing semantic/Broker/workspace/observation packages, strict JSON evidence, repository gate scripts, signed Git commits.

**Project:** `/Users/yuzhe/projects/agent-python-runtime`

---

## User intent

Yuzhe wants one sustained execution run rather than repeated `continue` messages. The run should attempt all approved safe optimisation tracks in dependency order. After each verified slice it must update this roadmap, make a signed commit, push, and immediately continue.

The run is also deliberately falsifiable. If an experiment shows that a later architectural phase is not justified, if a safe implementation would require arbitrary Python heap continuation or an unsafe broad rewrite, or if a result changes the intended architecture, record the evidence and stop for discussion rather than manufacturing a positive result or silently weakening the gate.

This roadmap prepares the run only. Implementation has not started.

## Value function

Prioritise, in order:

1. mechanised correctness and authority boundaries;
2. a fair matched comparison with EAGER's syntax-level gate;
3. mechanism-revealing workload cases and net critical-path benefit;
4. minimal reusable semantic/pass contracts;
5. typed, bounded result transport;
6. performance only after correctness/equivalence gates;
7. concise evidence and truthful claim boundaries.

Prefer a small complete vertical slice over a broad framework. A negative preregistered result is successful research. Do not add infrastructure merely so every proposed optimisation survives.

## Approved optimisation families

The run may implement these families when their preceding gates pass:

1. richer speculative-safe Host read preparation;
2. semantic-readiness decisions combining source dependencies and Host effects;
3. matched EAGER-style syntax gating for comparison, not as a production default;
4. run-scoped pure-region materialisation through a verified derived AST;
5. typed scalar/bytes/array/table result capsules when transport economics pass;
6. exact single-flight or durable cache only when repeated computation identity is observed and independently justified;
7. deterministic private-workspace file preparation with selected-content publication;
8. a minimal pass/consumer registration seam only after a second real consumer exists;
9. integrated ablations and real-Guest evidence.

## Explicit non-goals

Do not implement or claim:

- arbitrary Python automatic parallelisation;
- general executable IR, SSA, bytecode compiler or second Python interpreter;
- persistent partial Python heap continuation merely to match EAGER;
- dynamic injection/tracing/monkey-patching as a generic pure-region boundary;
- generic object cache or `pickle` transport;
- external HTTP POST, email, payment, deletion or other authority-bearing write speculation;
- compensation as rollback;
- commit of an irreversible effect merely because final source parses;
- universal filesystem rollback or Shimmy Image COW inside Pysolate;
- post-effect fallback/replay unless the Host proves `not_started/not_started`;
- production-default scheduling changes;
- a general pass manager before two measured consumers exist;
- paper, thesis, defence deck or rendered PDF changes;
- deployment, package publication, cloud mutation or manually triggered GitHub Actions.

If these become necessary to satisfy an approved phase, stop at the architecture gate with the smallest demonstrated counterexample and alternatives.

## Repository and source of truth

Read first:

1. this file;
2. `docs/research/semantic-speculation-roadmap-v0.md` for design rationale;
3. `docs/research/verified-semantic-overlay-v0.md`;
4. `docs/research/python-candidate-region-graph-v0.md`;
5. `docs/research/python-region-census-v0.md`;
6. `docs/research/source-prefix-opportunity-census-v1.md`;
7. `docs/research/effect-aware-related-work-matrix.md`;
8. `docs/plans/2026-08-17-source-prefix-overlap-mechanism-experiment.md`;
9. `docs/development.md`;
10. live implementation and tests under `runtime/semantic`, `runtime/capability`, `runtime/streaming`, `runtime/agentfunction`, `runtime/engine/wazero`, `runtime/workspace`, `guest/bootstrap/agent_runtime`, `research`, and `integration/e2e`.

Trust live Git and regenerated evidence over baseline prose. Keep this roadmap as the execution queue and completion log. The v0 research roadmap becomes design context, not a second active queue.

## Current state discovered on 2026-08-20

Baseline before this megagoal file:

```text
branch              main
HEAD                5bb4e20830d93908f20ba643c19d31c5894afeb3
upstream            origin/main aligned
worktree            clean
Host focused tests  runtime/semantic, runtime/capability, integration/e2e PASS
```

The focused baseline command completed successfully:

```bash
go test ./runtime/semantic ./runtime/capability ./integration/e2e -count=1
```

Current mechanism facts:

- source chunks append into a visible document;
- selected complete prefixes are reparsed by the exact target-Guest CPython AST analyser;
- analysis records source occurrences, dependencies, candidate regions, effects and barriers;
- Host policy/effect facts remain distinct from AST facts;
- `semantic_pre_dispatch` is the only enabled `SourceBoundPass` consumer;
- qualified fixed-input reads can begin as Host physical work before final source completion;
- unchanged final Python starts from line one and claims only an exact matching prepared observation;
- unsupported syntax, writes, dynamic inputs and mismatches follow ordinary execution;
- there is no general pass registry, executable region consumer, source rewrite, arbitrary region execution or general completed-result cache;
- Pysolate has private workspaces and selected file-content transfer, not Shimmy's Image COW or arbitrary process rollback.

Current evidence facts:

- the authored source-prefix mechanism demonstrates overlap for one controlled early read;
- live timing studies establish bounded provider-source windows but not full natural end-to-end uplift;
- the frozen 36-event tau2 READ cohort has zero structural source-prefix opportunities because every read is in the only/final region;
- the v0 19-program region census contains 69 candidates but zero statically materialisable regions and no repeated materialisable fingerprint;
- v0's whole-module effect-free requirement is intentionally coarse and may create false negatives;
- existing public related-work evidence states EAGER executes complete generated chunks in a persistent interpreter and serialises network/filesystem/process/timing-sensitive operations under its conservative gate.

Exact historical Guest artifact currently available:

```text
path    ~/.hermes/evidence/pysolate/source-bound-mg1-e79e821/dist/agent-python-runtime.wasm
sha256  664077c1d63445ec267b1b30e30ce31c72e7038d62a08fe1682c675a64cff257
```

It is not an exact artifact for this future implementation. Build and identity-bind a new Guest before final real-Guest claims.

## Desired future state

The megagoal is complete only when every non-blocked approved family has a real implementation or an evidence-backed rejection, and the final state has:

- a typed Host contract for unconsumed speculative physical work, freshness, privacy/billing partition, coalescing and terminal disposition;
- adversarial whole-program oracles including invalid suffix, unreachable call, earlier exception, cancellation and unknown effects;
- a matched serial/EAGER-style/Pysolate/oracle comparison over identical source schedules and capability delays;
- a frozen, mechanism-revealing region-local case matrix aligned with the comparator paper's workload shape;
- if admitted, a target-Guest-owned source-bound scalar materialisation consumer with a fully validated derived AST;
- if economically admitted, typed large-result capsules one codec/type at a time;
- if identity repetition is observed, narrowly justified single-flight/durable reuse with separate identities;
- if admitted, deterministic private-workspace output preparation with no official-state mutation before acceptance;
- a minimal typed pass/consumer seam after at least two consumers exist;
- exact real-Guest positive/adversarial evidence, matched timing and independent aggregate validation;
- explicit Current / Measured / Rejected / Deferred documentation;
- no stale claim that all final Python remains unchanged if an execution-patch consumer is retained;
- all relevant local gates green, roadmap closed, signed commits pushed, and a clean branch.

## Autonomy and repository rules

- Main controller owns implementation and integration. Delegate only one bounded read-only source review or frozen-diff review when it has independent value; no second writer in this worktree.
- Use TDD for executable behavior. Record RED command and expected missing behavior before implementation.
- Make small coherent signed commits and push after each verified phase or independently useful slice.
- A commit, push, test pass or context compaction is a checkpoint, not a stopping condition.
- Prefer local tests. Do not manually trigger CI. Use the ICL `gpu31` workstation through `ssh shell2` only for a bounded Linux/Guest build or validation that macOS cannot establish; do not deploy or start unbounded jobs.
- Local Docker, paid cloud, production accounts and external writes require separate approval and are excluded here.
- Keep private source/program/result bodies and credentials under protected local evidence roots. Commit only body-safe aggregates and preregistrations.
- Never rewrite preregistration after viewing final results.
- Do not modify the thesis repository, report, slides, PDFs or review packs during this megagoal.

## Hard stop conditions

Continue automatically until one of these is proved with tools and recorded below:

1. all executable tracks are complete and final closeout gates pass;
2. a named opportunity/economics gate fails and the result changes whether a later architecture should exist;
3. safe implementation requires arbitrary heap/process continuation, a general Python interpreter/compiler, or an unsafe broad rewrite outside this contract;
4. an exact target-Guest parser/executor invariant cannot be preserved by the proposed patch after one bounded lower-risk alternative;
5. the EAGER-distinct workload slice has no measurable opportunity across the frozen matched mechanism cases;
6. required real Guest/toolchain/evidence input is unavailable after bounded local and documented workstation alternatives;
7. focused/global gates repeatedly fail and the remaining alternatives require Yuzhe to select a semantic/product trade-off;
8. an operation would require deployment, paid resources, production access, external authority-bearing writes or another permission not granted here.

When stopping early, do not continue into an independent-looking later optimisation merely to stay busy if the failed gate changes the semantic architecture. Record:

- exact gate and evidence;
- modified files and current Git status;
- tests run and real output;
- signed commits already pushed;
- the smallest alternatives and recommended decision.

## Global gates

Use focused gates during RED/GREEN. Run full gates at Phase 3, Phase 6 and final closeout, or whenever shared contracts change broadly.

```bash
cd /Users/yuzhe/projects/agent-python-runtime

git diff --check
go test ./... -count=1
go vet ./...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
```

For final concurrency/lifecycle evidence:

```bash
env -u AGENT_RUNTIME_GUEST go test -race ./... -count=1
```

For Guest work:

```bash
guest/build/build-guest.sh
AGENT_RUNTIME_GUEST=/absolute/path/to/exact/agent-python-runtime.wasm \
  scripts/track-f-gate.sh guest
```

Use `scripts/track-f-gate.sh focused/full/lab` where applicable rather than rebuilding long gate commands manually. Run Lab gates only if the Lab or its fixtures change. Bind final evidence to exact source commit, artifact, manifest, import inventory, profile, tool plan and preregistration identities.

---

## Autonomous execution queue

### Phase 0: Baseline, freeze and roadmap activation

**Promise:** The run starts from one clean, exact baseline and cannot reinterpret historical evidence as current implementation proof.

- [x] Inspect live status, recent signed history, active processes and worktree ownership.
- [x] Mark `docs/research/semantic-speculation-roadmap-v0.md` as superseded for execution by this file while retaining it as design rationale.
- [x] Inventory exact current semantic contracts, pre-dispatch identities, capability fields, result terminal states, candidate-region fields, Guest analyser entry points and streaming integration tests.
- [x] Inventory existing research/evidence generators that can be reused without altering historical preregistrations.
- [x] Freeze an implementation-neutral experiment schema and claim boundary before behaviour changes.
- [x] Create a protected private evidence root and body-safe public preregistration for Phases 1–3.
- [x] Run focused baseline gates and record exact counts/duration.
- [x] Update this roadmap, signed commit, push, verify signature/upstream, continue.

**Stop:** sibling writer, dirty unowned work, unverifiable active generated artifacts, or inability to preserve historical evidence immutability.

### Phase 1: Whole-program semantics and adversarial oracle

**Promise:** "Safe" is decomposed into language, authority and physical-work outcomes and can be checked for invalid or never-reached programs.

Primary areas:

- `runtime/semantic/`
- `runtime/capability/`
- `runtime/observe/`
- `research/` new bounded package or existing appropriate experiment package
- `integration/e2e/`

RED cases must include:

- valid fixed-input read with trailing valid code;
- later syntax error where serial whole-file Python performs no logical call;
- later runtime error after a logically reached call;
- earlier exception before a candidate;
- branch not taken;
- custom wrapper/unknown call that syntax alone cannot classify;
- allowed immutable/freshness-bounded read;
- denied write and unknown effect;
- cancellation before logical claim;
- ready-but-unclaimed, late and orphaned physical work;
- plan/grant/freshness/privacy/source mismatch.

Tasks:

- [x] Define strict versioned baseline outcome, logical-call, physical-attempt and workspace/authority projections.
- [x] Build a full-file target-CPython baseline oracle rather than a line-by-line imitation.
- [x] Add mutation/tamper tests for every identity and terminal outcome.
- [x] Prove syntax-error source produces no Python execution/logical call in baseline.
- [x] Prove any Pysolate physical preparation remains separately recorded and cannot masquerade as a logical call.
- [x] Record provider-visible physical work as observable cost; do not call it semantic nothing.
- [x] Run focused/race gates, update roadmap, signed commit, push, continue.

**Gate P1:** Every adversarial row is classifiable without invented evidence. Unknown/unclassifiable is a failed gate, not an excluded sample.

### Phase 2: Host speculative-preparation contract

**Promise:** AST structure cannot silently grant speculation authority; every unconsumed physical attempt is covered by a frozen Host contract and budget.

Design a minimal orthogonal contract rather than a universal effect lattice. Reuse existing identities where they already prove the property. Candidate properties:

- safe/forbidden when physically executed but never logically consumed;
- freshness or immutable snapshot identity;
- billing/quota/privacy partition;
- coalescing permission distinct from idempotency;
- result-size and speculation-count budgets;
- cancellation/late/orphan disposition;
- no authority-bearing external writes.

Tasks:

- [x] Write RED registration/plan/legality tests for absent, inconsistent and tampered contract fields.
- [x] Extend the canonical capability/plan identity minimally.
- [x] Thread verified fields through legality, pre-dispatch, Broker/observation and evidence without widening Guest authority.
- [x] Enforce positive admission; name-based absence from a denylist never admits a Host operation.
- [x] Add the motivating invalid-suffix real path: a physical read may be authorised, the failed Run records it as cancelled, logical call stays absent, and no official workspace is touched. A successful Run with a ready unclaimed result remains orphaned.
- [x] Prove read-only/idempotent alone is insufficient when speculative-safe/freshness/privacy contracts are absent.
- [x] Run focused/race and exact Guest tests, update roadmap, signed commit, push, continue.

**Gate P2:** No missing/unknown Host metadata can fail open. The added contract remains small enough to review and canonicalise.

### Phase 3: Matched EAGER comparison

**Promise:** The project measures the exact work that EAGER-style syntax gating loses and Pysolate Host semantics recover, without comparing unrelated published headline numbers.

Treatments over identical frozen source/chunk schedules, target Python, Host handler delays and limits:

1. whole-file serial CPython;
2. EAGER-style complete-statement execution with the pinned paper's syntax/name gate;
3. Pysolate current semantic Host pre-dispatch;
4. analysis-only perfect-effect oracle upper bound.

The EAGER arm is a research comparator, not a production mode. Do not claim source parity with the authors' implementation unless their exact public artifact is used and verified. Otherwise label it `eager_style_gate`.

Tasks:

- [x] Freeze comparator semantics and mutation-resistant treatment identities before measurement.
- [x] Implement the minimum complete-statement/name-gate comparator over the same source stream.
- [x] Preserve persistent-interpreter behavior only inside the comparator lane.
- [x] Include valid suffix, invalid suffix, external read, pure local work, unknown wrapper, branch and cancellation cases.
- [x] Compute independently validated medians/pairwise deltas, safe overlap coverage, false-conservative critical-path time, orphaned work/bytes/billing units and logical/physical counts.
- [x] Keep the perfect oracle analysis-only and excluded from achieved speedup.
- [x] Run exact Guest matched pairs with deterministic lane order and a body-safe aggregate.
- [x] Run full gates and independent frozen-diff/evidence calculation review.
- [x] Record the sealed result, update the roadmap decision, sign, push and continue under the approved remediation phase.

**Gate P3-S (semantics/evidence): passed.** The complete predeclared campaign must preserve final result or exception, logical calls, authority state and workspace disposition across all achieved lanes; seal all coordinates canonically; keep the perfect oracle analysis-only; and pass independent aggregate verification.

**Gate P3-E (economics diagnostic): not passed by the original cold-analyzer implementation.** At least one non-trivial, predeclared workload family matching the comparator paper's execution shape should show syntax-level gating serialising a Host-qualified operation while Pysolate recovers positive net overlap after analyser, Broker and cancellation/orphan accounting. Authored fixtures are admissible because this gate establishes mechanism behavior, not population-level benchmark prevalence; the fixture must not branch on treatment or encode the expected result.

**Observed result and approved decision (2026-08-20).** The immutable 35-coordinate Exact Guest campaign passed P3-S but produced 0/35 positive semantic-versus-serial coordinates and 0/35 prepared results ready before finalization. Inspection localized the cost to the implementation: every cumulative prefix called `AnalyzeSemantic`, which instantiated, initialized and destroyed a fresh CPython/WASI module; two-chunk cases paid two analyzer Guests plus one formal Guest, and three-chunk cases paid three plus one. The campaign used neither `PreparedRuntime` nor Linux memory COW for analysis. This result remains a permanent negative baseline and the original matrix is never changed.

The owner approved continuing. Phase 4 is now an explicit remedy phase rather than a claim that P3-E passed: reduce exact analyzer invocations, attach analysis to single-use prepared/COW lifecycle where supported, add region-local precision, and then run a new preregistered economics gate. No later result may rewrite or relabel the original failure.

### Phase 4: Region-local precision and cold-prefix analyzer remediation

**Promise:** Pysolate removes repeated cold-prefix work without introducing a reusable mutable interpreter, then proves region-local admission and bounded economics under a new preregistered protocol. Host screening may suppress unnecessary analysis but can never mint semantic authority; exact target-Guest analysis remains the qualification boundary.

Lifecycle constraints:

- `PreparedRuntime` means one initialized, never-served, single-use module; it is consumed once and retired, never reset into a pool;
- Linux memory COW may derive a private single-use analyzer instance from a sealed initialized baseline; every clone is discarded;
- one private analyzer instance may operate as a bounded REPL-like RPC session for the lifetime of one source-generation Run, accepting multiple canonical `runtime_analyze_source` requests without restarting CPython between prefixes;
- the analyzer session executes analyzer code only, never Agent source, has no Broker or workspace, retains no source/result bodies after close, and is never reused by another Run;
- production must not retain or reuse mutable Python heap, frames, globals, GC/refcount state, FDs, WASI state or workspace state across source-generation Runs or Agent executions; the bounded analyzer RPC calls inside one Run are the only scoped exception and cannot execute Agent source;
- the research-private EAGER persistent interpreter remains comparator-only;
- formal Agent execution remains fresh and executes original source/AST unchanged;
- provisioning time, resident memory and discarded capacity are measured separately and never hidden from total-cost reporting.

Tasks:

- [x] Write RED timing/lifecycle instrumentation that proves the current two-chunk treatment performs two analyzer instantiations plus one formal execution and the three-chunk treatment performs three plus one. Record instantiate, `_initialize`, `runtime_init`, target analysis, admission, provider and formal execution spans without source/result bodies.
- [x] Freeze a versioned remediation preregistration before implementation measurements. Preserve the original seven-case matrix byte-for-byte; add a separate extension matrix with multiple predeclared source-gap/operation-latency regimes, more than one computation/effect shape and negative controls. Never tune a row after observing its result.
- [x] Add a conservative Host-owned prefix-readiness/change filter that can only skip analysis. It may request exact target-Guest qualification when a candidate statement/region first becomes complete or its relevant binding changes; uncertainty analyzes or rejects, never admits.
- [x] RED-test that two- and three-chunk frozen cases invoke exact analysis only on predeclared candidate transitions, while syntax failure, unknown wrappers, changed bindings and opaque control remain fail-closed.
- [x] Add a bounded `SemanticAnalysisSession`: acquire one fresh or never-served prepared analyzer at source-generation start, serialize multiple prefix requests through that same private interpreter, enforce request-count/byte/time limits, and close it at finalization/cancellation. RED-test repeated-call parity against fresh one-shot analysis and reject any cross-Run session reuse.
- [x] Route the authority-free session through an analyzer-specific single-use prepared instance where portable. Missing/unready/consumed/mismatched preparation falls back to one fresh per-Run analyzer session, not one fresh module per prefix and not cross-Run mutable reuse.
- [x] Add the Linux-only analyzer COW path behind capability detection: sealed initialized baseline, one private single-use clone per source-generation Run, bounded memory, unconditional close/unmap, and explicit fallback evidence. Do not make Linux COW necessary for semantic correctness.
- [x] Preserve exact artifact/profile/import/plan/source binding across Host screening, prepared/COW selection and target-Guest analysis. No prepared lifecycle choice may broaden capability authority.
- [x] Write RED Guest tests showing a pure top-level region remains locally classifiable when an unrelated later region has a Host effect, while unknown calls/heap mutation/`may_raise`/opaque control still reject.
- [x] Improve only region-local top-level effect/dependency coverage needed by the tests; do not build SSA or arbitrary interprocedural proof.
- [x] Preserve exact byte spans, target-Guest AST identity, canonical live-ins/live-outs, barriers and explicit unknown reasons.
- [x] Freeze a deterministic multi-region case matrix before observing region eligibility. Include positive scalar/local-compute shapes plus effects, `may_raise`, alias/identity, opaque-control and transport-negative controls; do not switch runtime behavior on case IDs or expected outcomes.
- [x] Add bounded cost-shape estimates from actual execution of constructed regions, not AST node count alone.
- [x] Run two clearly separated matched profiles: cold end-to-end and equivalently pre-provisioned capacity. Apply the same capacity boundary to serial, EAGER-style and semantic treatments; report provisioning and steady-state measurements separately.
- [x] Report analyzer invocation counts, candidate/admitted/rejected counts, lead-time availability, canonical inputs/outputs, result shapes, same-run opportunity, exact-repeat opportunity, prepared/COW hits/fallbacks, memory and discarded capacity.
- [x] Independently validate aggregate calculations, extension/multi-region matrix identities and the unchanged identity of the original Phase 3 matrix.
- [x] Update roadmap, signed commit, push, continue only if both P4 mechanism and economics gates pass.

**Gate P4-M (mechanism):** Multiple predeclared cases contain expensive, straight-line, effect-free, transportable regions with a usable source-generation lead window; exact target-Guest analysis is invoked only for admissible candidate transitions; each source-generation Run uses at most one bounded private analyzer session; prepared/COW analyzer instances remain single-use across Runs; nearby negative controls reject; and an ordinary fresh per-Run analysis session plus fresh formal execution remains the fallback.

**Gate P4-E (economics):** Under at least one non-trivial regime frozen before remediation measurements, achieved semantic pre-dispatch recovers positive net overlap after analyzer, provisioning, Broker, memory, cancellation/orphan and discarded-capacity accounting. The original Phase 3 matrix may remain negative and must be reported unchanged. Results support only the named synthetic regime and never natural-workload prevalence or production-general speedup.

**Observed result (2026-08-20): P4-M and P4-E passed.** The complete frozen campaign achieved 360/360 canonical trial records and 120/120 matched profile/case/trial cells with the original P4 matrix and preregistration identities. Independent aggregate validation found whole-program outcome, logical-call, authority, workspace, bounded-session, fresh-formal-Guest, zero-orphan and prepared/private-COW lifecycle parity. Both profiles passed economics: 3/5 eligible coordinates in cold end-to-end and 3/5 in equivalently pre-provisioned capacity met the frozen `>=100 ms` median-saving and `>=4/5` ready-before-finalize thresholds. Passing median savings were 432.9--460.6 ms cold and 2,435.4--2,471.4 ms pre-provisioned. Two earlier network-workspace attempts remain retained as failed infrastructure evidence; the complete run used host-local tmpfs without changing sources, coordinate order, treatments or gates. Evidence: `docs/evidence/semantic-speculation-phase4-campaign-evidence-v1.json`. These results authorize only the narrow Phase 5 spike below; they do not establish natural prevalence or production-general speedup.

### Phase 5: Run-scoped scalar region materialisation

**Promise:** One narrowly proven pure region can execute early and be consumed by a fully validated final program without preserving arbitrary Python heap state.

Architecture constraints:

- target Guest reparses and validates exact final source;
- Host never constructs or interprets Python AST semantics;
- early compute occurs in a scratch Guest under an exact execution profile;
- result is a bounded typed capsule, initially canonical JSON scalar/small structure;
- full final source must parse before any execution patch is selected;
- ready capsule is pinned before derived-program compilation;
- exact source/AST/region/live-in/environment/pass/codec identity is required;
- original source path is selected before Guest execution on miss;
- no generic runtime hook, monkey patch, tracing, bytecode interception or `pickle`;
- no `may_raise`, heap mutation, import side effect, capability call, opaque control or object-identity-sensitive output in v1.

Tasks:

- [x] Freeze a source-bound `prepared_region` decision/capsule/patch identity and strict decoder.
- [x] RED-test positive scalar materialisation plus every source/input/environment/codec/pass mismatch.
- [x] Add one trusted Guest helper, conceptually `__pysolate_materialize_value__(opaque_decision)`, backed by a Host-owned per-Run materialisation table. The AST embeds no blob handle, cache key, Host path, body or authority-bearing credential.
- [x] Make the helper claim only an already pinned exact decision and reconstruct a typed local value. Missing, stale, consumed, mismatched or unready decisions fail closed; derived execution never turns a claim failure into recomputation or another physical attempt.
- [x] Reserve the helper binding and reject source forms that can shadow, overwrite or dynamically mutate its execution binding. Keep the v1 patch to one exact RHS/single-assignment form and preserve source locations.
- [x] Implement a target-Guest-owned narrow AST patch emitter/validator preserving source locations.
- [ ] Implement scratch-Guest execution and bounded capsule publication with typed terminal states.
- [ ] Select original or derived program before final Guest execution; no racy runtime fallback.
- [ ] Prove invalid suffix, cancellation, earlier exception and unclaimed capsule paths leave no logical region consumption or official workspace mutation.
- [ ] Compare result, exception class, logical calls, authority state and traceback/source-location boundary against baseline.
- [ ] Measure analysis + scratch execution + transport + final validation + patch compile/load costs.
- [ ] Run focused/race/exact Guest gates and matched positive/negative trials.
- [ ] Update roadmap, signed commit, push, continue only if P5 passes.

**2026-08-20 bounded helper slice:** Decision, capsule and execution-patch contracts now bind the exact source, AST, analyzed region/span/bytes, live-ins, environment, execution profile, import closure, capability plan, pass config and scalar codec. A run-private Host table exposes one separate bounded `agent_runtime_v1.materialize_value` import; the trusted Guest helper accepts only canonical JSON `bool`/`int64`, and successful claim consumes the pinned capsule exactly once. Missing, unready, mismatched, stale and repeated claims fail without Broker calls, workspace state, recomputation or another physical attempt. Exact Guest artifact `sha256:bb3cd9464f54b242ec908e143ee3fbb359a05b7b9db6fe8f30053aed5dc0366c` passed macOS and Linux positive/missing/repeated claim cases. This does not yet execute a candidate region or emit/select a derived AST; that remains the next boundary.

**2026-08-20 target-Guest patch-emitter slice:** Analyzer v7 reserves `__pysolate_materialize_value__` across the whole source and marks every candidate in a source containing direct helper use/binding or dynamic namespace access with `reserved_helper_binding`. The private authority-free target Guest now validates the Go-sealed decision, exact final-source region bytes/span/identity and one top-level single-name `Assign`, replaces only its RHS, copies the original RHS source location, and emits a canonical payload-free patch binding containing the original and derived AST identities. Unknown/noncanonical contracts, all relevant identity drift, helper shadowing, multi-target/annotated/augmented assignments and dynamic namespace mutation fail closed. Artifact `sha256:154bdc3058000cfc5acee4d80dfc4a29547651a78c7eee0738cea6b878fe8dbe` passed macOS and Linux Exact Guest emitter controls without Broker/workspace authority. Candidate-region execution, capsule publication and original-versus-derived final execution remain unimplemented; P5 has not passed.

**Gate P5:** The admitted mechanism case matrix has positive net critical-path benefit and exact declared outcome parity. Stop if the patch requires arbitrary heap transfer, broad Python semantics or unsafe exception substitution. A win on the frozen authored matrix establishes bounded mechanism feasibility, not production prevalence or expected end-to-end uplift.

### Phase 6: Minimal pass and consumer architecture

**Promise:** Two real consumers share provenance/admission machinery without turning Pysolate into a generic compiler framework.

Consumers now eligible for refactoring:

- overlay-only `semantic_pre_dispatch`;
- execution-patch `prepared_pure_region`.

Tasks:

- [ ] Extract only the duplicated typed pass registration, version/config identity, deterministic decision and consumer lookup seams demonstrated by both consumers.
- [ ] Preserve opaque verified-analysis handles and Host authority separation.
- [ ] Make overlay-only versus execution-patch class explicit in types/evidence.
- [ ] Require per-pass legality, exact consumer, fallback, evidence and default-off configuration.
- [ ] Reject unknown pass/version/config/consumer combinations.
- [ ] Preserve existing `semantic_pre_dispatch` behavior and evidence identities or version them deliberately with migration tests.
- [ ] Add conformance tests proving both passes use target Guest facts, Host contracts and exact source binding.
- [ ] Run full tests/race/vet/Python/real Guest gates.
- [ ] Update roadmap, signed commit, push, continue.

**Stop:** A clean shared seam requires an executable IR, dynamic plugin system or broad rewrite. Keep the two explicit consumers instead and stop for decision if duplication cannot be bounded.

### Phase 7: Typed result capsules and cache decision

**Promise:** Larger outputs are added only where typed transport beats recomputation; run-scoped preparation, single-flight and durable cache remain separate mechanisms.

Subphases in order:

1. bytes/text/JSON structures with Host-owned immutable blob storage;
2. NumPy array only under an exact profile containing NumPy;
3. DataFrame/Arrow/Parquet only if the selected Guest profile contains the required package and a predeclared mechanism case demonstrates favorable transport economics.

Transport boundary:

```text
producer Guest typed payload
  -> one bounded copy into a Host-owned immutable blob
  -> Host retains one canonical body plus typed descriptor
  -> each consuming Guest performs one bounded copy into its private local object
  -> Python/NumPy/pandas wrappers remain interpreter-local
```

Tasks for each type:

- [ ] Freeze schema/codec/version/size bounds and semantic limitations.
- [ ] RED-test malformed data, decompression/shape/size bombs, version/profile mismatch, object-identity assumptions and unsafe path access.
- [ ] Implement a generation-bound Host blob handle and typed descriptor. The Host owns body lifetime, privacy partition, computation identity, quota, expiry and teardown; raw pointers, FDs and Host paths never enter Guest-visible state.
- [ ] Add a bounded binary Host/Guest copy seam if the existing JSON tool response would require base64 or redundant whole-body encodings. Producer publication copies Guest bytes once into Host-owned storage; consumer materialisation copies Host bytes once into a newly allocated Guest-local destination.
- [ ] Reconstruct local `bytes`/buffer/NumPy/DataFrame wrappers only after bounds, dtype, shape, strides, profile and exact computation identity validate.
- [ ] Keep pointer-bearing `dtype=object`, arbitrary extension arrays, pickle and generic Python object graphs rejected.
- [ ] RED-test stale/cross-project handles, eviction during claim, producer/consumer cancellation, body tampering, partial reads/writes, concurrent claims, quota exhaustion and instance teardown.
- [ ] Measure early compute, producer-to-Host copy, hash/store, Host-to-Guest copy, wrapper reconstruction, peak Host/Guest memory and recompute baseline across predeclared sizes and fan-out counts.
- [ ] Retain a type only over a measured profitable range and document fallback.
- [ ] Keep result bodies private; commit body-safe aggregates.

Explicitly deferred:

- no transferable arena inside linear memory;
- no `MAP_FIXED` subrange remapping;
- no zero-copy Host buffer exposed as a Python buffer;
- no arbitrary dirty-page promotion or Python heap transfer;
- no shared mutable memory, raw FD transfer, general CPython allocator replacement or wazero fork.

Cache and multi-agent reuse branch:

- [ ] Keep AST qualification and Host reuse authority separate. The semantic analyser may identify an exact pure producer/consumer region, but only the Host subagent descriptor/plan may authorize lineage, privacy scope and value delivery.
- [ ] Support automatic lineage-scoped reuse only for an exact computation identity repeated by parent/children or sibling children. Multi-agent context alone is never a cache key or admission proof.
- [ ] For a parent-produced value consumed by different child source, use a Host-authored typed `ValueRef` bound to parent lineage, producer computation, typed blob, child input identity, artifact/profile and privacy partition. Materialize it as an ordinary child input; do not ask the model to carry a blob handle or special sharing protocol.
- [ ] Use the Phase 4 census to distinguish run-scoped occurrences, coalescible simultaneous duplicates and durable cross-session exact repeats.
- [ ] Add single-flight only when Host coalescing authority and one physical/multiple logical evidence are present.
- [ ] Add durable cross-session cache only when exact repeated computation identity is observed naturally and binds source region, canonical live-ins, immutable dependencies, artifact/profile/imports/packages, pass/emitter, codec, privacy and policy epoch.
- [ ] Define durable reuse as fresh-interpreter value materialisation, not persistent-interpreter continuation. Every claim constructs a new local Python object; `id()`, aliases, weakrefs, module globals, heap, descriptors and in-place mutations do not cross Runs.
- [ ] Treat every modified consumer value as private. Persistence requires explicit publication of a new immutable blob generation; never mutate a retained canonical blob in place.
- [ ] Never use session ID, multi-agent status, overlay digest, natural-language task text or equal arguments alone as cache identity.
- [ ] Add expiry/invalidation/quota/body-size, process-reopen, stale lineage, cross-session/cross-project denial, mutation isolation and corrupted-entry tests before enabling completed-result reuse.

**Gate P7:** Each retained codec/cache mode has positive measured economics and strict identity. The Host owns one immutable canonical body; every consuming Guest receives one bounded private copy and reconstructs interpreter-local wrappers. If scalar succeeds but every large type loses to recomputation, record scalar-only support and continue. If safe transport requires arbitrary heap transfer, pointer-bearing values, generic pickle semantics, shared mappings, broad allocator replacement or a wazero fork, stop.

### Phase 8: Private-workspace file preparation

**Promise:** A deterministic file-producing region may execute in a private attempt and publish selected content only after exact final acceptance, without claiming Shimmy COW or external rollback.

Tasks:

- [ ] Select one natural or independently justified deterministic file-producing workload.
- [ ] Freeze exact input-file identities, output allowlist, byte/count/path limits, executable region and final-source binding.
- [ ] RED-test traversal, symlink/special file, undeclared output, base drift, invalid suffix, branch not reached, earlier exception, cancellation and rejected publication.
- [ ] Execute in a private Pysolate workspace attempt; no official workspace mutation before accepted consumption.
- [ ] Import only selected regular-file content through existing workspace transfer semantics.
- [ ] Keep Python heap, WASM memory, descriptors, sockets and process state out of transfer.
- [ ] Record changed/materialised bytes and accepted/discarded roots.
- [ ] Compare against ordinary execution and result/workspace oracle.
- [ ] Run focused/race/exact Guest gates, update roadmap, signed commit, push, continue.

**Gate P8:** Invalid/rejected/unreached cases leave official workspace unchanged and accepted cases publish only allowlisted content. Stop if correctness requires arbitrary process checkpoint/rollback or external compensation.

### Phase 9: Integrated campaign and ablation

**Promise:** The project can quantify what each semantic layer contributes without pooling incompatible workloads into one universal speedup.

Frozen arms, where implemented:

```text
whole-file serial
complete-statement syntax gate
+ region dependency facts
+ Host capability semantics
+ speculative authority/freshness
+ scalar region materialisation
+ retained typed capsule(s)
+ private-workspace materialisation
```

Tasks:

- [ ] Pre-register workload classes, denominators, lane order, repetitions, failure treatment and promotion thresholds.
- [ ] Separate external-read, pure scalar, typed large result and workspace-file workloads.
- [ ] Run exact real Guest matched trials; use Linux workstation only for a named platform-specific mechanism.
- [ ] Report safe overlap coverage, critical path, analysis/patch/transport cost, logical/physical counts, orphaned/wasted work, memory/bytes, workspace disposition, eligibility and rejection reasons.
- [ ] Validate final results/exceptions/logical calls/authority/workspace outcomes with independent oracle paths.
- [ ] Generate body-safe JSON and Markdown aggregates from protected raw evidence.
- [ ] Independently recalculate every headline metric from raw manifests.
- [ ] Request one bounded frozen-diff review of source identity, pass legality, patch/capsule loading, Host authority, workspace publication and aggregate maths.
- [ ] Fix blocker/high/medium findings with RED tests; rerun affected gates.
- [ ] Update roadmap, signed commit, push, continue.

**Claim rule:** Never aggregate these workloads into one "Pysolate speedup." Report mechanism and prevalence separately.

### Phase 10: Truthful closeout

**Promise:** Runtime, evidence and documentation agree on what is Current, Measured, Rejected and Deferred.

- [ ] Update runtime/research docs and examples for retained consumers and exact limitations.
- [ ] Mark rejected/no-go optimisations with evidence rather than leaving stale future promises.
- [ ] Update `docs/research/semantic-speculation-roadmap-v0.md` to point to final results.
- [ ] Audit product direction and development docs for stale unchanged-source, cache, rollback or current-pass claims.
- [ ] Do not edit thesis/report/deck artifacts.
- [ ] Build an exact final Guest and verify artifact/manifest/profile/import identity.
- [ ] Run all global gates, exact real Guest gates, sensitive/private-body checks and `git diff --check` on the final behavior commit.
- [ ] Verify all relevant commits are signed and pushed and branch is clean/upstream-aligned.
- [ ] Search this roadmap for unchecked executable items. Classify every remainder as completed, rejected with evidence, deferred by explicit non-goal, or blocked by a named owner decision.
- [ ] Make and push a final roadmap-only closeout commit only after final behavior/docs gates are current.

## Per-slice protocol

For every executable slice:

1. inspect live code, tests, evidence and Git state;
2. write a RED test or preregistered failing probe, or state why a docs/generated-artifact-only slice has no meaningful RED;
3. run it and capture the expected failure;
4. implement the minimum complete behavior;
5. run focused GREEN and adversarial tests;
6. inspect the diff for authority, source identity, fallback and private-body regressions;
7. update roadmap checkbox/current pointer/completion log;
8. run proportional global gates and `git diff --check`;
9. signed commit and push;
10. verify signature, upstream and clean status;
11. immediately start the next unchecked executable item.

Do not leave an intentionally failing RED test across unrelated work. If a context limit approaches, finish or revert the slice and leave a clean signed checkpoint, then continue from live roadmap state in the next execution window. Context/window exhaustion is not a project blocker.

## Roadmap tracking rules

- This file is the active execution source of truth for this lane.
- Add a `Current execution pointer` immediately below this section when execution begins and update it after each slice.
- Change `[ ]` to `[x]` only with real gate evidence.
- Record negative gates as `[blocked]` or `[rejected]` with exact evidence; do not erase them.
- Append completion entries with date, phase/slice, RED/GREEN/global gates and commit.
- Trust live Git over historical prose.
- Do not overwrite historical preregistration/evidence files; create versioned successors.
- A successful phase is not permission to stop while the next phase is still admitted.

### Current execution pointer

`Phase 4: run the frozen 360-trial cold and equivalently-preprovisioned campaign, then independently validate P4-M/P4-E aggregates.`

## Completion log

- 2026-08-20 Phase 4 bounded region cost study: added a fail-closed harness that first re-runs all 12 structural cases, derives only analyzer-proven dependency closures plus the exact frozen focus span, and then executes 20 seeded target-CPython/WASI trials in fresh authority-free Guests. Candidate time is measured inside the Guest with `perf_counter_ns`; whole-source time is the Host wall time for that constructed closure/focus program, including fresh Guest startup. The first evidence file (`semantic-speculation-phase4-region-cost-evidence-v1.json`, `sha256:9efab34298523f81ff1294c2199f3154792345bcc456fcca72657ff15e420083`) is retained but superseded because coordinates were collected from Go map iteration before seeded shuffling. The corrected deterministic v2 (`sha256:2fc79aa8d0974d1b47f46ea358574b0b83ae355e5b149aa0aff5990fcd016ad8`) records 16/64/128-operator medians of 3,458 / 14,041 / 28,292 ns and whole-source medians of 2.238 / 2.244 / 2.228 s. The preregistered monotonic opportunity gate passes, but these microsecond region costs do not establish P4 end-to-end economics; the frozen 360-trial cold/preprovisioned campaign remains required before Phase 5.
- 2026-08-20 Phase 4 frozen region mechanism gate, corrected evidence: an independent follow-up found that v1 validated only focus eligibility and therefore had not evidenced every frozen `required_control_tag`; specifically, `sinks.demo_publish` remained an unknown call because the frozen `mail.send` capability name had been projected to the wrong Python symbol. The strict Exact Guest gate now validates all 18 frozen control tags against effects, occurrence position and rejection reasons, and binds `mail.send` to the matrix's `sinks.demo_publish` symbol without changing the matrix. All 12 cases and all tags pass under the same remediation artifact. Evidence v2 (`semantic-speculation-phase4-region-mechanism-evidence-v2.json`, `sha256:121089d1885ee40466e52efefb8498588ddac1928afc466de0272ddf8dd9c0c7`) explicitly supersedes but retains v1. Bounded target-Guest cost measurement remains next.
- 2026-08-20 Phase 4 frozen region mechanism gate: the first Exact Guest run consumed the unchanged 12-case matrix and failed closed on exactly one control: `alias = items` was incorrectly locally reusable even though a later mutation made object identity observable. The minimum analyzer-local repair adds an explicit `identity_alias` rejection for non-scalar name aliases; it does not execute source or add consumer authority. Remediation artifact `sha256:8780338cf3b4330371b13f06a2846006077c3ff99ee89d7fb618ea19e252d242`, built from signed source `44574ddaf907181e9354b6e8c47c9a33a2657bf1`, passed all 12 frozen cases under the initial focus-only validator. Each case used one fresh bounded analyzer session with no Broker/workspace; there were no formal Guest executions. The initial evidence is retained as `semantic-speculation-phase4-region-mechanism-evidence-v1.json` (`sha256:2396df39411a53b36a428541ad9600ca9910a68684e934583cfd05dac2d8af27`) but is superseded by v2 because it was not sufficient to prove every required control tag. The matrix and preregistration were not changed.
- 2026-08-20 Phase 4 region census freeze: checked in canonical, strict, identity-locked `semantic-speculation-phase4-region-case-matrix-v1.json` (`sha256:fc3c3cdbf62eac9cde8c17625b6c60de1709d8a61b464872820021068813f6ee`) and independent `semantic-speculation-phase4-region-census-preregistration-v1.json` (`sha256:81a3110d66c8f84dc1be9bfea057049cbbe7af9214e52b6c4348ffabdedaf234`) before running the matrix. Twelve expanded source cases freeze three non-pilot scalar cost shapes plus effect-before/after/both, unknown call, input-derived type, division, heap mutation, alias/identity, opaque-control and non-JSON transport controls. The previously exercised two-operator scalar case is explicitly pilot-only and excluded from the opportunity gate. The preregistration freezes analyzer/artifact/source identities, five shuffled repetitions, actual authority-free execution timing after structural analysis, a fail-closed mechanism gate, and a no-go outcome unless all three non-pilot positives remain eligible with monotonic constructed cost. Strict decoders reject unknown fields, non-canonical bytes, source/hash drift and post-freeze observed-result fields.
- 2026-08-20 Phase 4 region-local precision slice: the target-Guest analyzer now carries a deliberately narrow scalar proof environment across top-level statements. Exact `bool`/`int` constants and `+`/`-`/`*` chains over previously proven scalar names may clear the broad expression-level `may_raise` rejection; no input subscript, division, arbitrary call, container/heap mutation, opaque control or unknown type is admitted. Candidate spans, source/AST/analyzer bindings, canonical live-ins/live-outs, producer-region dependencies, effects, barriers and explicit rejection reasons remain unchanged in shape. `CandidateRegion.LocallyReusable` is an analyzer-local predicate only and grants no execution, cache, transport or replacement authority. A new Exact Guest built from signed source `384327f413138434455af77a322f63afbace7384` (`sha256:cdb440e794b5865878e602eeebf4fe8198a20b33a140f7d4e87a679b1fa89191`) proved that `seed = 40; value = seed * 2 + 2` stays locally eligible before an unrelated `sources.demo_catalog()` effect, while unknown call, heap mutation, division-by-zero and opaque-control controls reject.
- 2026-08-20 post-review lifecycle hardening: restricted explicit capacity provisioning to the authority-free `PrepareSemanticRuntime` seam so a workspace-bound formal Engine cannot pre-initialize an unmounted module; added Engine-level semantic-session leases so concurrent close fails safely and prevents new sessions until the owner retries after session teardown; made treatment cancellation wait for bounded generation/session teardown and retryable analyzer close, eliminating the active-COW close leak; counted only the first real cached provisioning failure; and carried partial clone instantiate/initialize telemetry through COW failures before fresh fallback. Focused unit/race, macOS Exact Guest cancellation/fallback, Linux Wazero, Linux private-COW session and Linux preprovisioned treatment tests all passed.
- 2026-08-20 Phase 4 Linux private-COW analyzer slice: the bounded authority-free session now acquires one private clone from the existing sealed initialized Linux baseline, retains the baseline as equivalent-capacity readiness, and destroys each clone at session close. A research-only analyzer factory seam can provision identical capacity before source generation while still reporting provisioning in `BeginNanos`/`AnalyzerEngineNanos`; it cannot bypass session authority or semantic request bindings. Lifecycle v5 snapshots body-free prepared/image state before final discard, then closes the analyzer, so post-Run evidence records both capacity and teardown rather than observing an already-empty image. On `gpu31` the Exact Guest baseline was 128 MiB in a bounded 512 MiB virtual image with about 54.8 MiB allocated; a preprovisioned private clone took 28.4 ms, exact analysis 69.9 ms, and the three-prefix external-read treatment generated source in 324.2 ms with one COW hit, zero analyzer `runtime_init`, two skipped prefixes, one fresh formal Guest, and unchanged result/authority/workspace evidence. Cold COW provisioning remained visible at about 3.41 s. Linux COW isolation/discard and the full Wazero package tests passed; non-Linux COW selection still records a provision failure and falls back to one fresh private analyzer session.
- 2026-08-20 Phase 4 single-use prepared analyzer slice: the authority-free per-Run analyzer session now consumes the existing portable `PreparedRuntime` slot exactly once, with an explicit `PrepareSemanticRuntime` capacity-provisioning seam for the preregistered equivalent-capacity profile. A consumed or absent slot falls back to one fresh private session module, never one module per prefix. Semantic lifecycle v2 and the versioned treatment lifecycle (now v5) separately record provisioning, prepared/COW hits, fresh fallback, provision/clone time, prepared state and body-free image accounting. Exact Guest tests cover cold provision-plus-hit, preprovisioned hit, second-session fallback, and treatment wiring while formal execution remains a separate fresh Guest.
- 2026-08-20 Phase 4 bounded session slice: added a lazy-acquired, per-source-generation `SemanticAnalysisSession` over one private authority-free Exact Guest module. A session serializes requests, enforces request-count/cumulative-byte/time limits, closes on finalization, cancellation, limit breach or analysis failure, and clears diagnostic/body-bearing state on close. Exact repeated-call reports match fresh one-shot identities, a closed or exhausted session cannot be reused, and lifecycle v3 distinguishes sessions, requests and module initialization. The `unknown_wrapper` control now performs two exact requests through one module, one `_initialize` and one `runtime_init`; source generation measured 2.336 s, while fresh formal execution remained separate at 2.472 s. Pure-local still initializes no analyzer module.
- 2026-08-20 Phase 4 conservative readiness slice: added a Run-scoped Host filter over detached pre-dispatch Python projections from the sealed capability Plan. The filter can only request or skip exact analysis, fails open to exact analysis on non-monotonic source, detects completed projected calls, binding/dynamic risks and opaque-wrapper call transitions, and never admits authority. Skipped visible prefixes now advance a separate source binding without invalidating delayed exact-analysis results. Exact Guest probes reduced `external_read_valid_suffix` from three cold analyzer invocations to one (two skips), cutting source generation from the 6.475 s frozen baseline to 2.134 s, and reduced `pure_local` from two invocations to zero, cutting source generation from 4.330 s to 0.168 s; formal execution remained one fresh Guest over unchanged source. Exact Guest syntax-error and unknown-wrapper controls still passed, and target analysis emitted no eligible call sites for tool rebinding or opaque wrapper calls. Unit tests cover incomplete calls, changed bindings, non-monotonic fail-open behavior, all-skipped pure-local sealing and delayed-analysis/skipped-suffix ordering.
- 2026-08-20 Phase 4 remediation preregistration: froze a separate 12-coordinate extension matrix (`sha256:4cec92655c0f73578f96dc352be13e17aff3376645830ff89f0292e01d15af39`) and preregistration (`sha256:d17a78fa49fd8699f2d7ae3ec4f183e6e05e50a18d868f8fe54b26b87899676e`) before optimizer implementation measurements. The planned 360-trial grid predeclares direct-read and local-then-read positives across 0.3/3/6 s lead gaps and 0.25/2.5/6 s provider delays, plus runtime/syntax failures, earlier exception, branch-not-taken, unknown-wrapper and pure-local controls under cold and equivalently pre-provisioned profiles. It freezes candidate prefix indices, expected admission/disposition, body-free source/schedule/input hashes, profile clock/capacity/accounting boundaries, lifecycle/memory/authority metrics, mechanism requirements, and a P4-E threshold of at least one eligible coordinate with at least 100 ms median saving and readiness in at least 4/5 trials. The immutable Phase 3 matrix remains bound by its existing identity and was not modified.
- 2026-08-20 Phase 4 cold-lifecycle instrumentation: added body-free cumulative analyzer evidence for invocation/module/initialize/runtime-init/success/failure counts and monotonic phase times, plus treatment-level Begin engine provisioning, workspace, source generation, admission, provider and formal execution spans. Exact Guest RED→GREEN probes confirmed the two-chunk case performs two fresh analyzer modules plus one formal Guest and the three-chunk case performs three plus one. Representative committed-tree observations were: two chunks, 4.330 s source generation with 4.211 s in `runtime_init`, then 2.139 s formal execution; three chunks, 6.475 s source generation with 6.294 s in `runtime_init`, 252 ms provider work and 2.126 s formal execution. `runtime_init` accounted for about 97.2% of source-generation time in both rows; analyzer engine and formal engine construction added about 1.3 s each during Begin. The evidence schema contains no source, request, response or result fields.
- 2026-08-20 Phase 4 remediation decision: after inspecting the measured slowdown, the owner approved continuing rather than treating the cold-path economics result as an optimizer-wide no-go. The roadmap now preserves P3-S semantic/evidence success and P3-E cold-analyzer failure separately, records that `AnalyzeSemantic` bypassed `PreparedRuntime`/COW and repeated fresh CPython/WASI initialization per prefix, and activates a bounded remediation phase: lifecycle instrumentation, immutable extension preregistration, conservative skip-only Host readiness, exact analyzer invocation reduction, one bounded REPL-like private analyzer session per source-generation Run, single-use prepared/COW lifecycle across Runs, region-local precision and a new independently reviewed P4-E economics gate. Cross-Run mutable interpreter reuse and post-hoc mutation of the original matrix remain prohibited.
- 2026-08-20 Phase 3 sealed-campaign review: the committed driver completed all 35 frozen coordinates and 105 achieved Exact Guest trials, sealed manifest identity `sha256:a0397a28664675e5f450745a7315ea8f088f6a64cff864b69c2d66a8eeba1d33`, and removed its disposable workspace root. The independent Python review verified every canonical file, hash, identity, seeded order, binding, semantic outcome and aggregate. Semantic equivalence passed, with zero orphaned physical attempts, but Gate P3 did not: 0/35 coordinates recovered positive achieved overlap and 0/35 semantic results were ready before finalization. The frozen cases were not changed; Phase 4 was paused at that decision boundary until the owner approved the remediation path recorded above.
- 2026-08-20 Phase 3 full-campaign driver slice: added a SHA-256-ranked 35-coordinate schedule covering every frozen case and five trials, a canonical complete-grid manifest bound to source commit and all shared treatment identities, strict file/order/permission/hash/evidence verification, non-overwriting private manifest persistence, and the `semantic-speculation-campaign` CLI. The CLI requires fresh evidence/workspace roots, runs every coordinate through `ExactGuestCampaign`, exclusive-writes each envelope, verifies the complete set before sealing the manifest, and emits only the body-free manifest reference.
- 2026-08-20 Phase 3 executable-factory slice: extracted the verified treatment wiring into `ExactGuestCampaign`. It derives real artifact, manifest, import-inventory, execution-profile, capability-plan and canonical exact-partition privacy bindings; creates a fresh handler/plan/workspace per seeded lane; gives adapters only opaque hashed Run IDs; applies the frozen 250 ms provider latency; reconciles provider observations; computes the explicitly analysis-only oracle; and returns a sealed v2 envelope ready for exclusive persistence. Plan identity is handler-instance independent. An exact-Guest `pure_local` trial 2 ran through this executable and persisted successfully.
- 2026-08-20 Phase 3 live-fallback accounting slice: the frozen `unknown_wrapper` matched run exposed that semantic-pre-dispatch controller counters covered speculative issues and prepared claims but omitted ordinary live fallback calls. The adapter now reconciles controller evidence with total provider observations, counts only attempts outside the controller as live logical calls, marks them consumed and fails closed when provider counters lag controller evidence. Exact Guest matched evidence passed all three lanes with one physical attempt, one logical call, `read_consumed` authority and a published workspace.
- 2026-08-20 Phase 3 matched external-effect evidence slice: ran the seeded three-lane runner on frozen `external_read_valid_suffix` with the same source, inputs, chunk schedule and 250 ms handler latency in every achieved treatment. Each lane produced success, one physical attempt, one original logical call, `read_consumed` authority and a published workspace; the analysis-only oracle was computed by removing exactly the preregistered provider latency from serial elapsed time and remained excluded from achieved speedup. The v2 envelope was sealed and persisted through the private writer on the exact artifact. This is a matched synthetic mechanism result only, not a production-general speedup claim.
- 2026-08-20 Phase 3 private evidence-writer slice: added a fail-closed writer for one canonical matched-case envelope. It requires an existing non-symlink root with no group/world permissions, creates each deterministic case/trial filename once with `0600`, never overwrites even a partial prior file, fsyncs, reads back through the strict decoder, and returns body-free identity/SHA-256/size metadata for the future manifest. Unit tests cover private mode and overwrite refusal; the randomized exact-Guest `pure_local` campaign persisted and revalidated its real envelope.
- 2026-08-20 Phase 3 seeded-order slice: corrected the provisional canonical lane order before the full campaign. Each case/trial now ranks the three achieved treatments by SHA-256 over the preregistered seed `20260820`, case ID, trial index and treatment ID; the oracle locates the serial record by treatment rather than position. Matched-case evidence v2 records and validates the exact execution order, so reordering records or ignoring the seed invalidates the envelope identity. The randomized exact-Guest `pure_local` campaign still passed all three lanes and strict evidence round-trip.
- 2026-08-20 Phase 3 evidence-codec slice: added a strict canonical matched-case evidence envelope binding the three sealed achieved records, typed analysis-only oracle and recomputed aggregate under the frozen study/preregistration/matrix identities. The codec rejects duplicate/unknown fields, non-canonical JSON, aggregate or identity tampering and any attempt to set production generalization; every envelope states `synthetic_matched_mechanism_only` and `oracle_analysis_only=true`. The exact-Guest `pure_local` campaign now seals, encodes and decodes this body-free envelope successfully. Before the full run, v2 added the seeded execution-order binding.
- 2026-08-20 Phase 3 matched-runner slice: added a three-achieved-lane runner that creates a fresh treatment per lane, sends adapters only canonical inputs and frozen scheduled chunks, seals every `TrialRecord`, checks each result against the preregistered outcome/logical-call expectation before invoking the analysis-only oracle, and then applies the existing cross-lane binding/equivalence aggregate. Trial construction now rejects valid-looking post-hoc fixtures that are not byte-equivalent to a frozen matrix projection. An exact-Guest `pure_local` run sealed serial, EAGER-style and semantic-pre-dispatch records against the same artifact/manifest/import inventory/profile/plan/privacy bindings with zero capability calls; the oracle remained explicitly excluded from achieved speedup.
- 2026-08-20 Phase 3 syntax-outcome slice: the target semantic analyzer reports malformed final source as an invalid analysis rather than a typed syntax status. The research adapter now refuses to infer syntax from that ambiguous report alone: it performs a second exact-artifact parser probe with no Broker and no workspace mount, admits `syntax_error` only for `ErrAgentSourceInvalid`, and otherwise preserves the original fail-closed error. Exact-Guest `later_syntax_error` evidence recorded no Python start, no logical call, no consumed preparation, unchanged authority and discarded workspace; any already-issued physical operation remains visible in causal counters.
- 2026-08-20 Phase 3 non-success execution slice: added a one-shot generated-source outcome path that preserves the same exact final-source/controller binding as `ExecuteGeneratedSource`, publishes only validated success responses, and discards the private workspace for validated Python error responses. The body-bearing response is ephemeral Host input only; the campaign adapter persists just the error class and causal counters. RED reproduced the previous generic `streaming Guest did not produce a publishable response`; GREEN exact-Guest evidence for `later_runtime_error` recorded one prepared result consumed by one logical call, `RuntimeError`, no result body and discarded workspace.
- 2026-08-20 Phase 3 semantic-treatment slice: added a research-only scheduled adapter over the existing production `GenerateVerifiedSourceWithPreDispatch` → `ExecuteGeneratedSource` path. The adapter binds the exact artifact, execution profile, import closure and capability plan; keeps source/AST unchanged; admits only visible prefixes; executes the sealed original source in a fresh private-workspace Guest; and projects body-free result, authority, physical-operation and workspace evidence. The frozen `external_read_valid_suffix` row passed on the exact CPython 3.14 WASI artifact with one qualified physical read and one logical claim. At the scheduler's final-source boundary `ready_before_finalize=0`: with the frozen 250 ms provider latency, target-Guest prefix analysis dominated the 300 ms source schedule, so this row proves semantic equivalence and prepared-result consumption but does **not** claim overlap-derived economic benefit.
- 2026-08-20 Phase 3 serial/aggregate slice: added a research-only `serial_whole_file` treatment that receives the frozen chunk schedule but executes the complete source once in a fresh exact Guest, with private-workspace publish/discard, body-free result/error projection and Broker/provider counters. Exact Guest rows covered pure local work, the 250 ms external read and final syntax rejection before Python/capability execution. The EAGER adapter's matched external-read row confirmed the denied statement remained sealed until final source and then issued exactly one logical/physical call. Added an independent three-achieved-lane aggregate validator with a separately typed analysis-only perfect-effect oracle, strict cross-lane identity/equivalence checks and explicit oracle exclusion from achieved speedup.
- 2026-08-20 Phase 3 comparator-runtime slice: implemented a research-only target-Guest adapter with complete-statement compilation, target token lookahead, comparator-private persistent namespace, low-yield batching, static denied-name sealing, frozen prefix runtime failures, cancellation and body-free terminal projection. The first exact artifact (`e5b558d`, `sha256:261ce32c159d68dd416fde01d7b863d0749593f39797374b035eb8cd58a6089a`) exposed that trusted prepare namespaces are fresh and was rejected. The corrected module-private session at source `3e92cb4a0b3f9e9945a1d63933d3e8b6b93896ad`, artifact `sha256:12dbb89ec0d9ae1510c990539ab9316c0f4ab979f8d15d4320973ff4f3fcfb54`, passed artifact and supply-chain verification plus five exact-Guest E2E rows in 17.525 s. Private evidence manifest `sha256:cffba4ff7c5b3607166bdd225616ea7c49da0e74920cdef89b972b42b61e5cdf`. No normal runtime path activates the comparator; matched timing remains pending.
- 2026-08-20 Phase 3 synthetic-case slice: froze seven body-safe mechanism cases in `pysolate.semantic-speculation-synthetic-case-matrix.v1` (`sha256:f69c31c874d56b7563942bf889a798ed16b38a657fef18be90d4251f49fbee3f`; canonical file `sha256:ac19c0597e4b9dc3e40c847cf37770fb55803f798db94ea4cfdb74a30e70565c`). The matrix binds source/chunk/input digests, release offsets, expected outcomes, the 250 ms preregistered physical delay and EAGER comparator identity without carrying source bodies. Trial schema v2 adds required schedule/input/manifest/import-inventory/profile identities before the first timing record; no v1 timing records existed. These cases support mechanism and bounded-economics claims only, not natural prevalence.
- 2026-08-20 Phase 3 comparator-contract slice: froze body-free `pysolate.eager-style-gate-contract.v1` against arXiv `2604.00491` PDF `sha256:23af671ca94b7cbbc0866a37391520ae39e75c964320e7809b1612dfb3e023cb`, including target-CPython statement boundaries, one-token lookahead, comparator-private persistent interpreter, low-yield declarations, static denied module/dynamic-name matching and explicit invalid-suffix behavior. Contract identity `sha256:16e76c741749bddb61c68ae80902827f73e9dc7efdad6208d858f8edae100ab8`; checked-in canonical file `sha256:6fe4f615ac532d855b87be26bc785573c94291b8075dedc71125b44d042f263b`. Trial records bind the comparator identity only for `eager_style_gate`. Focused gate, semanticspeculation tests and vet passed. The evidence route now admits frozen synthetic mechanism cases without claiming natural-workload prevalence.
- 2026-08-20 Phase 2 contract slice: capability Plan v7 now requires positive pre-dispatch v1 declarations for exact partition privacy, forbidden coalescing, result-byte ceiling and provider cost units in addition to the existing resource/freshness/unclaimed-safety contract. Run-private admission reserves attempt count, cost units and worst-case result bytes atomically; evidence separates reservations, physical starts and returned physical bytes. Oversized Host results become typed `invalid_result` outcomes without exposing bodies. Historical source-prefix v6 evidence is recomputed with its frozen canonical document rather than rewritten. Full `go test ./...` (including 543.931 s integration/e2e), all 105 script tests, focused gates, targeted race, vet and exact Guest adversarial/pre-dispatch tests passed.
- 2026-08-20 Phase 1 complete: Gate P1 passed with every preregistered adversarial row mapped to a typed outcome and direct test. Exact source commit `1c07fc2b9a012abab9071abb777e9ba80f18ee66`; base CPython 3.14 WASI artifact `sha256:7be7bc7ea15951364427764d36fa6ac40b6f2ed68e71a5a6c639492a2f21df79`; private body-free evidence manifest `sha256:d2902898091a95c71da9214e6643673dfbb8d6289a6b1ec95a49bb8f2e675d35`. Three exact-Guest oracle tests passed in 27.024 s, 14-package focused gate passed, three-package race gate passed, vet and artifact/supply-chain verification passed.
- 2026-08-20 Phase 1 qualification slice: target-Guest analysis of a custom wrapper records the inner direct capability only in the function summary, emits no positive call-site occurrence for pre-dispatch, and marks the dynamic wrapper invocation region `unknown_effect`; the planner emits no decision and no physical work.
- 2026-08-20 Phase 1 outcome-contract slice: added strict body-free `pysolate.semantic-speculation-trial.v1` projections for final-program, prefix-Python, logical-call, physical-attempt, provider-cost, terminal-disposition, authority and workspace outcomes. Exact-Guest oracle tests cover whole-file syntax rejection, reached and unreached runtime errors, untaken control, ordinary success, and separately recorded pre-dispatch physical work on an invalid final suffix. The initial RED exposed two real semantics: full-file syntax rejection occurs as `ErrAgentSourceInvalid` before Broker creation, and invalid streamed suffix finalisation classifies an already issued pre-dispatch as cancelled rather than a logical call.
- 2026-08-20 Phase 0: froze `pysolate.semantic-speculation-preregistration.v1` at parent `f604ce16b5bc7135c92e1dc70f9b91b124cf9f2c`; contract identity `sha256:5c0ec80ded86f07784d51d74aa503108fbd4a587918bc483bd564b35bdc18a47`, public file SHA-256 `479b9c7fb7aa1f8fe70a34824cf1221b04d9d36ef2fd52fecdbfbc96da0f8ccb`, private manifest SHA-256 `00797778c209a875abf989029d4efa7103a0992c389eb9a1eb585810364c9b5c`. RED was an undefined contract API; GREEN covered strict canonical round-trip, mutation, unknown fields, ordering, duplicates, unsafe claims and deterministic construction. Focused gate passed 14 Go packages in 6.28 s; the new package/command passed in 0.28 s; Guest Python passed 108 tests and scripts passed 105 tests.
- 2026-08-20 preparation: froze this autonomous handoff from clean `main` after focused semantic/capability/e2e tests passed. No implementation, report or slide changes were started.

## Final stopping report

Report only when a hard stop condition is satisfied:

1. exact satisfied completion/blocker gate;
2. implemented, rejected and deferred optimisation families;
3. real benchmark/evidence identities and claim boundary;
4. tests/gates with real counts/results;
5. signed pushed commits;
6. final Git status;
7. whether report/slides remained untouched;
8. safest next owner decision if blocked.

## Short prompt to start this mega-goal

```text
Read docs/plans/2026-08-20-semantic-speculation-optimizer-autonomous-megagoal.md fully and execute it in /Users/yuzhe/projects/agent-python-runtime from live Git state. Continue through successive RED/GREEN, exact-Guest, evidence, signed-commit and push slices; do not stop after a phase or checkpoint. Stop only when the roadmap is completely closed or a named opportunity, semantics, resource, permission or unsafe-rewrite gate genuinely requires my decision. Preserve target-CPython and Host-authority boundaries, do not modify the thesis/slides, and do not deploy, use paid cloud/Docker, speculate external writes, or manually trigger CI.
```
