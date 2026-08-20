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
3. natural-workload opportunity and net critical-path benefit;
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
- an improved region-local opportunity census over a frozen natural corpus;
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
5. the EAGER-distinct workload slice has no measurable opportunity after the frozen matched experiment and one independently chosen natural corpus;
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

- [ ] Define strict versioned baseline outcome, logical-call, physical-attempt and workspace/authority projections.
- [ ] Build a full-file target-CPython baseline oracle rather than a line-by-line imitation.
- [ ] Add mutation/tamper tests for every identity and terminal outcome.
- [ ] Prove syntax-error source produces no Python execution/logical call in baseline.
- [ ] Prove any Pysolate physical preparation remains separately recorded and cannot masquerade as a logical call.
- [ ] Record provider-visible physical work as observable cost; do not call it semantic nothing.
- [ ] Run focused/race gates, update roadmap, signed commit, push, continue.

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

- [ ] Write RED registration/plan/legality tests for absent, inconsistent and tampered contract fields.
- [ ] Extend the canonical capability/plan identity minimally.
- [ ] Thread verified fields through legality, pre-dispatch, Broker/observation and evidence without widening Guest authority.
- [ ] Enforce positive admission; name-based absence from a denylist never admits a Host operation.
- [ ] Add the motivating invalid-suffix real path: physical read may be authorised and orphaned, logical call stays absent, official workspace remains unchanged.
- [ ] Prove read-only/idempotent alone is insufficient when speculative-safe/freshness/privacy contracts are absent.
- [ ] Run focused/race and exact Guest tests, update roadmap, signed commit, push, continue.

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

- [ ] Freeze comparator semantics and mutation-resistant treatment identities before measurement.
- [ ] Implement the minimum complete-statement/name-gate comparator over the same source stream.
- [ ] Preserve persistent-interpreter behavior only inside the comparator lane.
- [ ] Include valid suffix, invalid suffix, external read, pure local work, unknown wrapper, branch and cancellation cases.
- [ ] Compute independently validated medians/pairwise deltas, safe overlap coverage, false-conservative critical-path time, orphaned work/bytes/billing units and logical/physical counts.
- [ ] Keep the perfect oracle analysis-only and excluded from achieved speedup.
- [ ] Run exact Guest matched pairs with deterministic lane order and a body-safe aggregate.
- [ ] Run full gates and independent frozen-diff/evidence calculation review.
- [ ] Update roadmap, signed commit, push, continue only if P3 passes.

**Gate P3:** At least one non-trivial, non-authored-only workload family shows syntax-level gating serialising a Host-qualified operation while Pysolate recovers positive net overlap after analyser, Broker and cancellation/orphan accounting. If only authored fixtures pass and the independently frozen natural corpus contains zero opportunities, stop for Yuzhe rather than redesigning the workload.

### Phase 4: Region-local semantic precision and natural census

**Promise:** Pure-region implementation begins only after source analysis finds useful natural candidates without relying on the v0 whole-module false-negative rule.

Tasks:

- [ ] Write RED Guest tests showing a pure top-level region remains locally classifiable when an unrelated later region has a Host effect, while unknown calls/heap mutation/`may_raise`/opaque control still reject.
- [ ] Improve only region-local top-level effect/dependency coverage needed by the test; do not build SSA or arbitrary interprocedural proof.
- [ ] Preserve exact byte spans, target-Guest AST identity, canonical live-ins/live-outs, barriers and explicit unknown reasons.
- [ ] Freeze a natural multi-region agent-program corpus before observing region eligibility. Reuse existing private Hermes/natural corpus only through body-safe manifests; retain all rejected/unclassifiable denominators.
- [ ] Add bounded cost-shape estimates from actual measured local regions, not AST node count alone.
- [ ] Report candidate/admitted/rejected counts, lead-time availability, canonical inputs/outputs, result shapes, same-run opportunity and exact-repeat opportunity separately.
- [ ] Independently validate aggregate calculations and corpus identity.
- [ ] Update roadmap, signed commit, push, continue only if P4 passes.

**Gate P4:** Multiple naturally generated programs must contain expensive, straight-line, effect-free, transportable regions with a usable source-generation lead window. If no such cohort exists, stop. Do not proceed using only a hand-authored expensive expression.

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

- [ ] Freeze a source-bound `prepared_region` decision/capsule/patch identity and strict decoder.
- [ ] RED-test positive scalar materialisation plus every source/input/environment/codec/pass mismatch.
- [ ] Add one trusted Guest helper, conceptually `__pysolate_materialize_value__(opaque_decision)`, backed by a Host-owned per-Run materialisation table. The AST embeds no blob handle, cache key, Host path, body or authority-bearing credential.
- [ ] Make the helper claim only an already pinned exact decision and reconstruct a typed local value. Missing, stale, consumed, mismatched or unready decisions fail closed; derived execution never turns a claim failure into recomputation or another physical attempt.
- [ ] Reserve the helper binding and reject source forms that can shadow, overwrite or dynamically mutate its execution binding. Keep the v1 patch to one exact RHS/single-assignment form and preserve source locations.
- [ ] Implement a target-Guest-owned narrow AST patch emitter/validator preserving source locations.
- [ ] Implement scratch-Guest execution and bounded capsule publication with typed terminal states.
- [ ] Select original or derived program before final Guest execution; no racy runtime fallback.
- [ ] Prove invalid suffix, cancellation, earlier exception and unclaimed capsule paths leave no logical region consumption or official workspace mutation.
- [ ] Compare result, exception class, logical calls, authority state and traceback/source-location boundary against baseline.
- [ ] Measure analysis + scratch execution + transport + final validation + patch compile/load costs.
- [ ] Run focused/race/exact Guest gates and matched positive/negative trials.
- [ ] Update roadmap, signed commit, push, continue only if P5 passes.

**Gate P5:** The natural admitted cohort has positive net critical-path benefit and exact declared outcome parity. Stop if the patch requires arbitrary heap transfer, broad Python semantics, unsafe exception substitution, or only wins on an authored toy.

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
3. DataFrame/Arrow/Parquet only if the selected Guest profile contains the required package and a natural workload justifies it.

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

`Phase 1: whole-program semantics and adversarial oracle.`

## Completion log

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
