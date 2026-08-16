# Pysolate Effect-Compiler Paper and Evaluation Source of Truth

> **For Hermes:** Treat this document as the only active roadmap for paper claims, benchmark selection, and evaluation closeout. Do not add runtime features unless a failed preregistered test exposes a claim-blocking implementation defect and the owner explicitly reopens feature work.

**Goal:** Close a paper-grade, natural-task evaluation of Pysolate's authority-bound effect compilation without adding speculative product features.

**Architecture:** Agent-written Python remains the executable program. The exact target Guest emits bounded source/effect facts without authority; the Host verifies those facts against artifact/profile/import identity, joins them with a sealed capability Plan and frozen Run context, and alone may select physical execution. Evaluation must preserve logical result/effect/workspace semantics while measuring physical execution, placement and causal evidence separately.

**Tech Stack:** Go Host runtime, CPython/WASI Guest, Wazero, optional native sandbox probes, strict JSON evidence, private raw benchmark artifacts and body-safe public aggregates.

---

Status: **Canonical active source of truth**

Decision date: 2026-08-16

Implementation baseline inspected for this reset: `cc321f2952730651141ec73032c6f5a24cf84333`

This file supersedes the active/future-work status in:

- `docs/plans/2026-08-15-source-bound-agent-program-roadmap.md`;
- `docs/plans/2026-08-16-source-bound-program-night-run-autonomous-megagoal.md`.

Those files remain historical implementation and evidence records. Live code and accepted evidence override stale historical prose, but new paper/evaluation work is tracked only here.

## 1. Frozen governing decisions

1. **Feature freeze.** No new optimizer, scheduler, worker pool, generic package installer, dependency resolver, source-bound call-level coalescing pass, general workflow engine, or production external-write plane is currently justified.
2. **Evaluation-only next phase.** Remaining work is benchmark qualification, natural-task execution, adversarial mutation, differential trace validation, causal evidence joining, and paper claim freeze.
3. **Lifestyle/workflow tasks are the primary natural workload.** Bounded read-dominant and approval-gated everyday tasks better exercise tools, authority, effects, waiting and evidence than repository engineering tasks.
4. **Coding tasks are mainly placement controls.** A task requiring a mutable checkout, shell, Git, package manager, native dependency, service or broad test environment should select a native/VM/Computer backend before execution. It is not a failure that Pysolate does not admit it.
5. **`attrs-770` remains one narrow case study.** It proves an exact pure-Python package profile and real semantic oracle, not general coding-task coverage or a package manager.
6. **No perfect rollback claim.** Real-world reversal is adapter-specific forward compensation. Each effect adapter may offer a typed `revert`/compensate operation, but that operation can fail, be partial, or create new effects. It never rewinds time.
7. **No new streaming feature without measured value.** Preserve and test the narrow current streaming mechanisms; do not restore literal eager preflight or generalize incremental execution unless a natural benchmark shows material overlap.
8. **Negative evidence is a result.** Rejected and unclassifiable cases remain in denominators. Sparse natural sharing opportunity means the call-level sharing pass remains unimplemented.

## 2. Paper thesis and strongest claims

### Thesis

> **Pysolate compiles effect facts from agent-written Python without compiling authority into the program: the Host seals capabilities, qualifies physical execution changes, and preserves causal truth across logical effects and physical execution.**

The contribution is a conjunction, not generated code, sandboxing, AST inspection, tool schemas, caching, or receipts by themselves.

### Claim A — Authority-bound effect compilation

**Status:** Current, bounded.

The exact accepted Python program is parsed by the target Guest. The Guest emits source spans, call occurrences, conservative effect summaries, barriers, candidates and rejection reasons. The Host strictly validates and binds those facts to:

- exact source;
- Guest artifact and execution profile;
- import closure;
- sealed capability Plan;
- frozen Run context.

Compiler/analyzer output contains no provider handle, grant or execution authority. Original Python remains executable authority; the overlay is a verified side table, not executable IR.

### Claim B — One Host-owned capability/effect ABI from declaration to execution

**Status:** Current, bounded.

A canonical Host-authored `capability.Spec`, opaque Grant and Handler are registered and sealed into a per-Run Plan. The same contract drives:

- registration and Plan identity;
- generated direct/programmatic Python presentation;
- compiler effect projection and legality;
- Broker membership, schema, budget and approval enforcement;
- handler identity and playback treatment;
- receipts and effect lifecycle evidence.

Python wrappers and Agent metadata are presentation only. Direct and programmatic calls re-enter the same Broker. Tool declarations alone are not the claim; Host enforcement and cross-layer identity are.

### Claim C — Authority-preserving physical execution

**Status:** Current for exact bounded mechanisms; not a general optimizer.

Qualified mechanisms may change physical work while preserving the original logical boundary:

- semantic pre-dispatch moves one exact read earlier and lets unchanged Python claim it at its original Broker call;
- whole-Run single-flight lets concurrent exact invocations share one in-flight producer;
- retained exact-result reuse serves a later qualified invocation;
- unqualified sharing/cache decisions fail closed.

Evaluation must compare result/exception, ordered effects, cancellation, workspace disposition and logical/physical operation counts. Physical savings alone are insufficient.

### Claim D — Causally complete Host-owned evidence

**Status:** Current, bounded; full natural-task join remains the main evaluation gap.

Evidence distinguishes source occurrence, Plan/authority, logical operation, physical producer, consumer, Run, workspace attempt and terminal effect disposition. Sharing, pre-dispatch, cancellation, rejection and placement must remain reconstructible without inferring truth from UI layout or log prose.

### Supporting claim — Typed non-replay placement/promotion

**Status:** Current library/orchestrator contract and tests; not generally production-wired transparent fallback.

The Host may select WASM/native/unavailable before execution from requirements, imports, state and policy. A fresh native child attempt after WASM is permitted only for a typed unsupported outcome proving:

```text
workspace = not_started
and effects = not_started
```

Ordinary exceptions, text matching, started/ambiguous effects or started workspace block promotion. `apyrun` currently emits the typed unsupported outcome; it does not itself launch the native backend. Do not claim universal automatic fallback.

## 3. Mechanism status matrix

Statuses separate primitive, legality, activation, production wiring and observed evidence.

| Mechanism | Primitive | Legality | Activation/wiring | Observed boundary | Current decision |
|---|---|---|---|---|---|
| Semantic pre-dispatch | Implemented | Exact source-bound `CanPreissue` implemented | Default-off/explicit consumer | Real Guest and differential tests | Keep; evaluate on natural read tasks |
| Reach-gated streamed call | Implemented | Normal Python control flow + Broker | Experimental streaming Run | Real Guest streaming fixture | Keep; no expansion |
| Literal eager stream preflight | Historical primitive | Successor legality withdrew it | Disabled | Historical fixture only | Do not restore |
| Whole-Run single-flight | Implemented | Exact qualified invocation identity | Experimental/caller-wired | Constructed campaign and tests | Keep as mechanism control |
| Retained whole-Run result | Implemented | Exact qualified invocation identity | Experimental/caller-wired | Constructed campaign and tests | Keep as mechanism control |
| Source-bound call-level coalescing | Runtime join concept exists | `CanCoalesce` rejects `contract_missing` | Not wired | Natural corpus gate negative | Unimplemented; do not add |
| Source-bound call-level cache | Retention primitive exists | `CanCache` rejects `contract_missing` | Not wired | Natural corpus insufficient | Unimplemented; do not add |
| Pre-execution placement | Implemented | Static requirements/import/state policy | Package/probes; not one universal product router | Tests and native probes | Evaluate placement correctness |
| Typed L2 native promotion | Implemented orchestrator | Only `not_started/not_started` | Library/test path | Unit/integration evidence | Evaluate non-replay safety |
| Transparent automatic fallback | Backends exist | Safe protocol exists | Main CLI/service not universally wired | Not established | Do not claim |
| Hot approval suspension | Implemented | Plan-bound lease and dispatch commit gate | Experimental, same-process | Real CPython/WASI Guest | Keep; test bounded wait semantics |
| Workflow wait/fresh resume | Implemented | Explicit immutable state/observation identity | Experimental | Real composable E2E | Keep separate from continuation |
| Cold-I/O continuation | Implemented on COW Host-call path | Host policy and bounded timers | Experimental/outcome-qualified | Linux/Zao pageout/refault evidence | Supporting mechanism only |
| Generic arbitrary user-input suspension | Broker can block, but no canonical typed user-input product surface | Not defined | Not wired | Not established | Deferred until a benchmark requires it |
| Production external writes/reconciliation | Approval can gate a live handler | No production effect journal/reconciliation contract | Denied | Not established | Out of current phase |

## 4. Suspension and waiting semantics

Do not use one word, “pause,” for three different contracts.

### 4.1 Hot approval suspension — same Guest retained

**Current, experimental, same-process.**

A real Guest reaches a Plan-bound capability call. The Broker submits an approval proposal and blocks before handler dispatch. While waiting:

- the same CPython/WASM invocation, stack and memory remain resident;
- the Host controller waits on approve/reject/expiry/cancellation;
- approval crosses one explicit dispatch-commit linearization point;
- reject/expiry and cancellation that wins the gate dispatch zero handlers;
- the bounded body-safe audit can outlive Guest teardown.

This currently supports an approval decision, not arbitrary free-form user input.

### 4.2 Cold-I/O continuation — same continuation, memory may become cold

**Current, experimental, platform/outcome-qualified.**

On the Linux COW Host-call path, Pysolate can preserve the same module, private mapping and continuation while the Host call blocks. After bounded thresholds it may apply `MADV_COLD` and `MADV_PAGEOUT`, then refault and resume before returning the Host response. The slot is never returned to a shared pool.

Observed evidence showed 96 MiB private dirty memory moved to swap and refaulted intact on the named Zao experiment. This is memory-pressure handling, not durable suspension or process migration.

### 4.3 Workflow wait — Guest released, fresh re-evaluation

**Current, experimental.**

At an explicit workflow wait, the Guest closes. Resume creates a new Guest and recomputes from small explicit immutable state and refreshed observations. No Python heap, stack or WASM continuation is retained.

Use this for long waits where holding memory is undesirable. Do not describe it as continuation replay.

## 5. Streaming source and input decision

### Current narrow mechanism

The streaming Runner accepts append-only Python source chunks. The target Guest executes only complete top-level statements or closed compound suites in one private namespace while later source is still arriving. It freezes the import preamble, rejects dynamic escape, keeps filesystem changes private, and validates the complete final source before publication. A later invalid suffix discards unpublished state.

This can overlap:

```text
model continues producing Python
∥ earlier complete suites execute
∥ a reach-gated read blocks/returns through the Broker
```

### What is disabled

The historical path that saw a literal read in incomplete source and dispatched it even if Python might never reach it is disabled. Capability metadata no longer activates eager stream dispatch. The current safe alternatives are:

1. unchanged streaming Python dynamically reaches the exact call; or
2. a complete verified source plus Host legality qualifies semantic pre-dispatch.

### Current decision

Do not generalize streaming now. It is operationally complex because later source can change syntax validity, imports, control flow, exception order and publication eligibility. During evaluation:

- include one existing narrow streaming differential lane;
- measure actual overlap with model/source arrival and read latency;
- keep outputs and workspace unpublished until final seal;
- do not add eager speculative reads, arbitrary partial-expression execution or a new streaming-input API unless measured natural benefit is material.

## 6. Effect reversal and compensation

Pysolate adopts the realistic contract:

```text
execute effect
→ observe/provider readback
→ if policy requests reversal and adapter supports it, issue a new typed revert
→ verify the revert outcome independently
```

Rules:

- compensation/revert is a new forward effect, not rollback or time reversal;
- each tool/adapter owns its own optional revert semantics;
- a revert may fail, be partial, race with later state, or itself be ambiguous;
- an irreversible or unknown effect has no fabricated revert;
- workspace discard does not reverse external effects;
- provider idempotency does not imply exactly-once execution;
- ambiguous dispatch blocks blind retry until authoritative reconciliation.

This matches real connector systems conceptually, but Pysolate does not currently have a production external-write journal/reconciliation/compensation plane. It remains Deferred and is not part of this evaluation phase.

## 7. Natural benchmark strategy

### 7.1 Why coding is not the primary benchmark

Most natural coding tasks require one or more of:

- mutable repository checkout and Git;
- shell/subprocess;
- package installation or broad dependency resolution;
- native extensions;
- project services, browsers or databases;
- long-lived mutable workspace state.

These are valid pre-execution native/VM/Computer placement signals. Forcing them into a bounded WASM profile would weaken Pysolate's authority boundary and manufacture adoption.

Coding remains useful only as:

1. a **placement control**: the Host correctly routes environment-heavy work to VM/native before execution;
2. a **narrow admitted case**: `attrs-770` demonstrates one exact pure-Python artifact profile with a public RED/GREEN semantic oracle;
3. an optional exact authority-free compute subworkflow inside a larger VM task, but only if measured duplicate-compute value exists.

### 7.2 Primary workload class

Select natural lifestyle/workflow tasks whose core is bounded Python composition over Host tools, for example:

- calendar and availability reads;
- email/thread search and summarization inputs;
- notes/document lookup and transformation;
- travel/product/catalog comparison;
- map/route/structured-data lookup;
- form preparation or draft generation;
- multi-source read workflows;
- approval-gated intent preparation with no production write dispatch.

Prefer read-dominant tasks first because current effect semantics are strongest for pure/workspace/external reads. For state-changing benchmark tasks, use deterministic fake/protocol-real adapters and an authoritative final-state oracle; do not connect production writes merely to improve benchmark realism.

### 7.3 Benchmark qualification gate

Do not select a benchmark merely because it is called an agent benchmark. It must provide or permit capture of:

- natural task and initial state;
- tool/API schemas and stateful semantics;
- executable Agent Python, not only prose or opaque direct calls;
- final task oracle independent of Pysolate receipts;
- logical tool/effect trace;
- deterministic fake or controlled provider where writes are present;
- bounded privacy/licensing path;
- enough read latency or waiting to test pre-dispatch/streaming honestly.

A direct-tool benchmark may be used only with a preregistered interface treatment: direct baseline versus programmatic Python through the same sealed Plan and Broker. Changing the model interface must remain explicit.

### 7.4 Mixed denominator

The cohort must retain:

- admitted bounded tasks;
- correctly routed native/VM tasks;
- rejected authority/profile/import cases;
- unclassifiable cases;
- task failures after admission.

A task routed to VM is a correct placement outcome, not a Pysolate failure. Report both admission share and admitted completion share.

### 7.5 Initial bounded cohort

Target 10–20 natural tasks after a tiny canary. Include:

- at least one pure computation;
- several multi-read lifestyle workflows;
- at least one approval wait;
- at least one workspace-local draft/transformation;
- one streaming-source eligible task if the harness exposes real source arrival;
- several intentional native/VM placement controls, including coding/environment-heavy work;
- `attrs-770` as a separate case study, not part of a broad lifestyle success denominator.

Do not use an LLM to relabel deterministic source/effect/placement fields. Preserve raw bodies privately and publish only body-safe manifests and aggregates.

## 8. Evaluation questions and required evidence

### RQ1 — Can the compiler bind real Agent programs conservatively?

Measure:

- parse/profile compatibility;
- source occurrence coverage;
- admitted versus rejected candidates;
- barriers and explicit unknown reasons;
- artifact/profile/import/Plan mutation rejection.

### RQ2 — Does Host enforcement prevent declaration or wrapper bypass?

Test:

- undeclared capability;
- forged effect metadata;
- direct bridge call bypassing Python wrapper;
- schema mismatch;
- Plan/grant/handler identity mismatch;
- budget exhaustion;
- approval rejection/expiry/cancellation;
- undeclared import/profile.

### RQ3 — Do physical execution changes preserve logical semantics?

Compare baseline versus enabled lanes for:

- canonical result or exception;
- ordered logical effects;
- physical producer count;
- logical consumer count;
- cancellation and timeout;
- workspace start/final identity and disposition;
- staged result claimed/orphaned/late disposition;
- latency only where the workload has real overlap.

Include semantic pre-dispatch, whole-Run single-flight and retained exact reuse as separate mechanisms. Do not infer call-level coalescing.

### RQ4 — Is waiting handled without authority or lifecycle ambiguity?

Cover separately:

- approval wait with same Guest retained;
- reject/expiry/cancel dispatching zero handlers when they win the gate;
- cold-I/O continuation with same continuation and bounded memory disposition;
- workflow wait with Guest destruction and fresh re-evaluation;
- no claim of arbitrary user-input suspension unless a typed surface is added later.

### RQ5 — Does placement avoid unsafe replay?

Test:

- pre-execution WASM/native/unavailable decisions;
- `not_started/not_started` promotion;
- workspace-started rejection;
- effect intent/started/ambiguous rejection;
- ordinary Python exception rejection;
- malformed outcome rejection;
- fresh child decision with parent identity;
- no state migration or continuation claim.

### RQ6 — Is the causal account complete end to end?

For every retained task, join:

```text
Harness/model step
→ exact source and source occurrence
→ analysis and decision
→ sealed Plan/authority
→ logical capability/effect operation
→ physical execution producer
→ workspace attempt
→ terminal result/disposition
→ independent task oracle
```

Missing joins remain `not_recorded`; they are never inferred from adjacent timestamps or Lab layout.

## 9. Execution roadmap — testing only

### Phase T0 — Truth and benchmark selection

Decision record: [Natural Agent Benchmark Qualification v1](../research/natural-agent-benchmark-qualification-v1.md)

- [x] Audit candidate lifestyle/workflow benchmarks against Section 7.3.
- [x] Select `tau2-bench@c3398666e6559e3a063da3fc04b5acf7f941464e` as the primary source and freeze version/license/interface.
- [x] Define paired direct baseline and programmatic-Python treatment without changing task, user, tool or oracle semantics.
- [x] Define an isolated task-local upstream environment as the protocol-real provider and preserve the official final-state evaluator as an independent oracle.
- [x] Freeze mixed-denominator inclusion/rejection/placement schema, including `unsupported_effect_class`.

**Gate status:** achieved by the authored fresh-turn `airline/3` canary in [the body-safe report](../evidence/tau2-airline-3-canary-v1.json). Two tool Runs and one authority-free aggregation Run used fresh real Guests; source digests, two distinct exact source occurrences, Plan, Broker receipts and the independent official oracle are recorded. The result is `SUPPORTED` for adapter/runtime/oracle wiring, but remains an authored lane rather than a natural-model score.

### Phase T1 — Deterministic canary

- [x] Run the first authored `airline/3` read-only adapter/Guest/oracle wiring canary.
- [x] Close 2–3 bounded rows covering natural multi-read, authored pure/no-authority, and a frozen natural coding placement control; retain `NO_ELIGIBLE_PURE_NATURAL_TASK` rather than inventing a pure tau2 success.
- [x] Verify task oracle independently from Runtime receipts.
- [x] Verify body-safe report determinism and private evidence permissions.
- [x] Prove direct/programmatic calls cross the same Broker.
- [x] Attempt the same model/task/seed pairing with `deepseek/deepseek-chat`; retain the `PAIR_NOT_COMPARABLE` interface-qualification failure without imputing a treatment score.
- [x] Qualify `deepseek/deepseek-v4-pro` for upstream-valid direct tool calls and strict treatment JSON program actions.
- [x] Run the qualified same-model/task/seed pair: both direct and treatment obtained official reward `1.0`; treatment recorded two source-bound Guest/Broker READs.
- [x] Audit `airline/11` without executing WRITE; retain it as `unsupported_effect_class` because no current attempt-world binding, persistent WRITE adapter, matching effect contract, final-state disposition join or cancellation discard proof exists.
- [x] Preserve that audit as the pre-implementation boundary, then close it with one exact benchmark-private `workspace_write` canary: accepted/rejected/expired/failure controls, real WASM Guest/Broker, atomic candidate install, disposition evidence and official DB/COMMUNICATE `1.0`.
- [x] Rebuild the WRITE aggregate from private raw source, canonical Plan/Grant evidence, Guest bodies, receipt identities, workspace disposition events, final state and strict oracle artifacts; reject independent identity/body/oracle tampering rather than trusting manifest joins.
- [x] Qualify the pure/no-authority row against a deterministic no-op-sensitive upstream component. Retain `NO_ELIGIBLE_PURE_NATURAL_TASK`: the sole candidate issued two READs in the direct probe, while a separate authored zero-capability Guest control recorded zero semantic call sites, Broker calls and receipts.
- [x] Route one frozen resolved Python Open-SWE trajectory with 31 shell calls and 15 workspace-editor calls to `native_sandbox` before Guest/workspace/effect start; retain a native-unavailable control with zero backend calls.

**Gate status:** achieved. All canary rows have exact terminal status and complete causal joins or explicit `not_recorded` fields. The placement report is [natural-placement-open-swe-v1.json](../evidence/natural-placement-open-swe-v1.json), SHA-256 `a6a757041694a5971941b37753f6cd9d33839543f69c2187bd2dfd54ce8fd49a`; it proves pre-Guest routing only, not coding execution or native-backend correctness.

### Phase T2 — Natural bounded cohort

- [x] Freeze an exact 16-task airline READ denominator (`1,2,3,4,5,6,9,27,36,38,41,43,45,47,48,49`) before provider execution; retain 32 paired cells and prohibit post-provider reruns or denominator dropping.
- [x] Preserve default tau2 DB/COMMUNICATE evaluation while adding the official upstream `ActionEvaluator` as a separately labelled no-op-sensitive diagnostic; do not call it leaderboard overall.
- [x] Run provider-free preflight for all 38 exact reference-action envelopes, all three READ tool shapes through real WASM Guest/Broker, and one out-of-scope negative. Bind raw Guest bodies, canonical Plan/Grant and source-bound receipt identities.
- [x] Consume the single preregistered pre-provider repair budget on path canonicalization: data paths resolve absolutely while the venv Python executable remains an absolute path without symlink dereferencing.
- [x] Run all 16 preregistered tasks with no post-hoc denominator dropping: 32/32 terminal rows retained.
- [x] Preserve all completed, unscorable and not-recorded rows; never project missing or failed evaluations as reward `0`.
- [x] Record direct and programmatic-Python traces. Direct completed 16/16; treatment completed 7/16, was unscorable 8/16 and not recorded 1/16.
- [x] Keep the `airline/11` approval/WRITE wait as a separate authored mechanism row; do not mix it into the natural READ denominator. The optional source-stream lane was not run.
- [x] Keep `attrs-770` as a separately labelled case study.

**Gate outcome:** `FAILED_FOR_TREATMENT_CAUSAL_EVIDENCE`. The body-safe aggregate is byte-stable and preserves all 32 rows, but treatment raw Guest files reused shared `turn-XX-*` paths across tasks. Later tasks overwrote earlier bodies. Six completed treatment rows are therefore labelled `not_recorded_shared_raw_path` with zero source joins; only one completed treatment row is independently reconstructed. Frozen `post_provider_reruns=0` forbids repair-and-rerun. This cohort supports the recorded model/task outcomes but cannot upgrade the Runtime mechanism claim beyond the earlier valid canaries.

**Recorded run:** known provider invocations `328`; one externally interrupted treatment cell has unknown invocation count. No post-provider reruns occurred. Direct official SimulationRuns completed 16/16. Of eight unscorable treatment cells, seven rejected an AssistantMessage carrying neither content nor tool calls and one exhausted the 20-call per-cell budget. The externally interrupted cell is `not_recorded`, not reward `0`. Aggregate: [tau2-t2-cohort-v1.json](../evidence/tau2-t2-cohort-v1.json), SHA-256 `1b39af87cffe8daffb13f422cca6af5e8162652abdd265433e7d0a9aba85d252`.

**Remediation v2 (separate, authorized run):** a new treatment-only preregistration retained the same 16 tasks and parent identities, fixed task-scoped raw paths, treated whitespace-only invalid model actions as a non-empty terminal assistant message, and raised the per-cell provider guard from 20 to 64. It did not rerun direct and does not replace T2-v1. All 16/16 treatment cells completed; 241 provider calls were recorded, with no unknown counts. There are 36 logical READ events but 35 unique receipt-bound physical/source joins: task `49` repeats one receipt identity across two events and is explicitly `partially_reconstructed_duplicate_identity` with `1/2` joins; the other 15 rows are fully reconstructed. All 16 rows independently rebuild the complete upstream `EvaluationType.ALL` RewardInfo from fixed-source tasks and recorded SimulationRun messages, and rebuild the ActionEvaluator diagnostic from source-bound program events. Each upstream Message runs its semantic validator; roles must follow the frozen half-duplex sequence; the fixed greeting is checked; and every subsequent assistant message is bound exactly to one terminal answer/invalid event. Semantic-empty, message/event mismatch, reward mutation and cleared-message controls are rejected. Shared raw-path collisions are zero, and raw references are constrained to relative task-prefixed paths under the evidence root. Default tau2 reward is `1` for 9/16 rows; the separately labelled official ActionEvaluator diagnostic is `1` for 13/16. Because surfaces remain unmatched, these are treatment observations rather than a performance comparison. Manifest SHA-256 `206957add70c75e2be890131ed770b580b1affa3308c8dcb80524f4dea4b06b0`; preflight SHA-256 `9a3639d8577e6c69d94ec4ac2390c05daab8afb9976b8fd1e59e2ab7ba4ed516`; aggregate [tau2-t2-remediation-v1.json](../evidence/tau2-t2-remediation-v1.json), SHA-256 `5f504a138084f933ba0fd4f3bec7aede7076924ec3c2a5cfb8f05db3dd9a513f`.

**Current T2 evidence boundary:** T2-v1 remains the immutable failed first attempt. Remediation v2 preserves 16 independently rebuilt official oracle outcomes and 35 unique receipt-bound source joins across 15 fully reconstructed rows plus one partial row. It still does not support leaderboard claims, matched-surface direct/treatment deltas, model WRITE ability or production effects.

**Future-run identity repair (not retroactive):** the dynamic T2 harness now derives each Host Run identity from the frozen cohort identity, task ID and task-scoped turn index, rejects malformed cohort identities and task/output identity mismatches, and passes the complete identity into the Guest/Broker helper. The receipt contract already binds Run ID, so repeated identical source/tool/arguments in different future turns produce distinct receipt identities. This code repair does not alter or upgrade remediation-v2: its task `49` row remains partial and its aggregate remains the immutable 35-join result. Any later model cohort must use a newly preregistered protocol after the remaining planned harness changes.

### Phase T3 — Adversarial identity and lifecycle matrix

- [ ] Mutate source, artifact, profile, import, Plan, grant and handler identities one field at a time.
- [ ] Exercise approval expiry/cancel races and dispatch commit.
- [ ] Exercise staged observation orphan/late/cancel ownership.
- [ ] Exercise only-safe typed native promotion and all replay blockers.
- [ ] Verify workspace and effect dispositions stay independent.

**Gate:** every adversarial case either fails before physical start or produces the exact declared bounded terminal state.

### Phase T4 — Paper evidence freeze

- [ ] Freeze Current/Observed/Deferred claim matrix against one signed source.
- [ ] Generate one system mechanism figure and one `attrs-770` case-study figure.
- [ ] Generate natural-task admission/completion/placement table.
- [ ] Generate baseline-versus-enabled physical-work/equivalence table.
- [ ] Generate authority/adversarial negative-control table.
- [ ] Record sharing/coalescing natural gate as negative unless new preregistered evidence changes it.
- [ ] Audit prohibited terms: rollback, exactly-once, transparent fallback, general package support, arbitrary Python optimization and production external writes.

**Gate:** every paper sentence classified as Current, Observed, Deferred or limitation and linked to a named evidence artifact.

## 10. Stop rules

Stop and discuss before implementation if evaluation would require:

- a new production capability or external-write adapter;
- general arbitrary user-input suspension;
- general package installation/resolution;
- new call-level coalescing/cache contracts;
- a scheduler, worker pool or multi-wait workflow engine;
- ambient shell/network/credential authority inside the bounded Guest;
- semantic placement replacement;
- source rewrite or executable IR;
- rerun after a possibly started effect;
- publication of private source, prompts, tool bodies or credentials.

A benchmark that only works after weakening authority boundaries is unsuitable; it is not a reason to expand Pysolate.

## 11. Paper non-claims

The paper must not claim:

- invention of sandboxed generated code, AST analysis, tool schemas, approvals, single-flight, caching or compensation;
- general arbitrary-Python optimization;
- a general side-effect transaction system;
- perfect rollback or exactly-once external effects;
- production external-write reconciliation;
- general pip/package support from `attrs-770`;
- automatic transparent fallback in every execution surface;
- natural call-level sharing opportunity from constructed campaigns;
- that coding tasks should run in Pysolate when their environment requires VM/native execution;
- that a digest, trace or receipt proves external-world truth by itself.
