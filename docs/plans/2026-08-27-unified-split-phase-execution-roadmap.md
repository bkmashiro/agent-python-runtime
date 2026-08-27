# Unified Split-Phase Execution Roadmap

Status: **Proposed; awaiting owner approval. No implementation is authorized by this document yet.**

This is the sole proposed execution roadmap for replacing Pysolate's overlapping source-streaming, semantic pre-dispatch, Python Future-proxy and split-phase call paths with one statically planned execution model. Historical roadmaps remain evidence records and must not be treated as competing queues once this roadmap is approved.

## Goal

Converge Pysolate on one execution model:

1. source streaming incrementally analyses complete source regions and may physically issue explicitly admitted Host operations, but never executes Guest Python;
2. source seal triggers one whole-program, target-CPython AST lowering pass;
3. the derived program is compiled once and executes once as ordinary synchronous Python;
4. fixed `issue_or_reuse` and `collect` sites express all latency hiding;
5. one Run-private Host attempt table serves both source-time pre-issue and runtime issue;
6. the Host owns authority, physical truth and lifecycle, but no Python dependency graph, continuation or runtime scheduling policy.

The change deliberately retires retained-prefix Guest execution. It preserves the measured external-read pre-dispatch mechanism while giving up the unmeasured ability to overlap source generation with local Python computation and private filesystem mutation.

## Desired end state

```text
source chunks
  -> incremental target-Python analysis
  -> optional admitted Host pre-issue
  -> no Guest Python execution

source seal
  -> final source and AST verification
  -> one deterministic split-phase lowering pass
  -> fixed derived source/code object

one synchronous Guest execution
  -> issue_or_reuse(static site, dynamic occurrence, concrete request)
  -> ordinary Python control flow and computation
  -> collect(handle) at the original logical call by default

one Host attempt table
  -> request identity and lifecycle
  -> physical running/completed/failed/uncertain truth
  -> Broker-owned logical admission and receipts
  -> consumed/cancelled/discarded terminal disposition
```

A disabled, rejected or unsupported optimization follows ordinary complete-source sequential execution without widening authority.

## Frozen architecture decisions

These decisions require an explicit owner revision before implementation may contradict them.

1. **No Guest execution before source seal.** Streaming performs analysis and optional Host pre-issue only.
2. **No runtime AST mutation.** The final AST is lowered once after seal and compiled once before execution.
3. **No Python Future proxy in the retained design.** Compiler-generated handles remain internal; ordinary Python receives concrete values.
4. **No Host dependency graph.** The analyser/compiler may use transient CFG/dataflow facts, but lowering encodes dependencies into ordinary Python control flow.
5. **Named attempts, not named dependencies.** The Host tracks static site, dynamic occurrence and request identity, never `A -> B` Python dependency edges.
6. **Issue moves; collect stays conservative.** V1 moves physical issue to the earliest policy-legal point and keeps collect at the original logical call site. Collect sinking is deferred.
7. **Argument evaluation remains Python-semantic.** An issue cannot move before its arguments are concrete and safe to evaluate at that point.
8. **Control activation remains ordinary Python.** A runtime issue inside a branch or loop occurs only when CPython reaches the precomputed site, unless a separate Host contract explicitly admits path speculation.
9. **External writes do not speculate.** V1 covers selected Host reads and other operations with an explicit safe-unconsumed contract. Writes retain the sequential effect path.
10. **Broker semantics remain authoritative.** Permission, budget, logical operation order, result validation and receipts are not inferred from physical completion.
11. **One table, two issue opportunities.** Source-time pre-dispatch and AST-scheduled runtime issue populate the same Run-private attempt table.
12. **Local streaming is a retired experiment, not a hidden fallback.** Its historical implementation and negative economics remain reproducible evidence.

## Non-goals

- arbitrary Python automatic parallelisation;
- a general SSA optimizer, bytecode compiler or second Python runtime;
- Host-side continuations, Future arguments or dependency DAGs;
- automatic execution of `B` when `A` completes before the Python thread reaches the next fixed issue site;
- general collect sinking across opaque, effectful or may-raise Python;
- speculative external writes, compensation or replay of uncertain effects;
- general branch speculation before an active path is known;
- generic Python object, heap or interpreter-state transport;
- preserving historical feature flags or adapters solely for compatibility;
- pooling unrelated benchmark workloads into one speedup;
- editing thesis, report or defence artifacts before the runtime and evidence gates close.

## Current baseline

Live implementation currently contains four overlapping paths:

1. `semantic_pre_dispatch` can start a qualified fixed-input external read before final source completion and lets later unchanged Python claim it.
2. `source_streaming_execution` retains a private Guest and executes supported local suites while later source arrives.
3. `capability_future_projection` injects `_CapabilityFuture` proxies and resolves them through Python dunder operations and final result traversal.
4. `split_phase_sources_read` emits `_pysolate_call_submit` and `_pysolate_call_materialize` for a narrow `sources.read` execution patch.

Relevant implementation facts:

- `runtime/capability/split_phase.go` already separates physical `Submit` from Broker-routed `Materialize` and finalizes unclaimed attempts;
- `guest/bootstrap/agent_runtime/__init__.py` already assigns dynamic occurrence identities for split-phase slots;
- `guest/tests/test_source_pass.py` and `integration/e2e/split_phase_source_pass_test.go` cover straight-line overlap, active branch/loop occurrences, failure and discard behavior for the narrow pass;
- `runtime/mechanisms.go` currently forbids `SplitPhaseCalls` and `SemanticPreDispatch` from composing;
- `runtime/capability/registry.go` still generates the Future-proxy projection;
- the local streaming path has correctness evidence but no separate latency study in the dissertation evidence package;
- the existing semantic-speculation campaign preserves substantial negative evidence about repeated prefix analysis, persistent-prefix execution and prepared pure-region economics.

## Status vocabulary

- **Pending:** admitted by this roadmap but not started.
- **Active:** the one slice named by the execution pointer.
- **Verified:** exercised through its named gate on the integrated tree.
- **Rejected:** attempted and stopped by recorded evidence without weakening the gate.
- **Deferred:** deliberately outside this roadmap; not a failed slice.
- **Blocked:** requires a named owner, permission, resource or architecture decision.

Only the execution pointer identifies current work. Checkboxes and historical notes do not authorize parallel mutation of shared contracts.

## Workstream ownership

The main controller owns architecture, integration, the execution pointer and every shared contract. One writer is allowed per worktree.

| Workstream | Primary ownership | May run in parallel after | Must not modify |
|---|---|---|---|
| Contract and failure oracle | Main controller | Immediately after approval | Production behavior before Gate Contract |
| Host attempt-table unification | Runtime worker in an isolated worktree | Gate Contract | Guest lowering files except agreed ABI fixtures |
| Seal-time compiler/lowering | Compiler worker in an isolated worktree | Gate Contract | Host lifecycle implementation except agreed ABI fixtures |
| Differential and economics evidence | Evidence worker; body-safe artifacts only | Stable Gate Contract oracle, then the integrated tree | Production semantics or preregistration after results |
| Independent review | Read-only reviewer | Frozen diffs at Gate Integration, Gate Retirement and Gate Closeout | Any mutable source |
| Documentation and paper framing | Main controller | Gate Evidence | Runtime claims not established by evidence |

Integration rules:

- shared ABI/type edits are controller-owned and land before parallel work begins;
- runtime and compiler workers rebase onto the frozen contract and return independently runnable slices;
- the controller verifies each slice against its immediate parent and integrates in dependency order;
- no worker edits this roadmap's execution pointer;
- a review result is advisory until reproduced or confirmed by the controller;
- use signed coherent commits and local verification; do not manually trigger GitHub Actions.

## Dependency graph

```text
Gate Contract: semantic and ABI contract
        |
        +------ Lane H: Host unification ------+
        |                                      |
        +------ Lane C: compiler lowering -----+--> Gate Integration
                                               |
                                               +--> Lane R path retirement
                                               |
                                               +--> Lane E evidence/economics
                                               |
                                               +--> Lane D docs/paper closeout
```

Lane H and Lane C may proceed in parallel only after Gate Contract freezes their shared fixtures and interfaces. All later lanes depend on the integrated vertical slice.

## Lane 0: Freeze semantics, oracle and minimal ABI

**Outcome:** One small executable contract states what may move, what remains logical and what each failure means.

Reference model: [`pysolate-issue-collect-formal-report.md`](../research/pysolate-issue-collect-formal-report.md) is a Proposed paper-level proof sketch, not a current implementation claim. Gate Contract consumes only its conservative V1 model; its listed proof obligations must close before any stronger manuscript claim.

Tasks:

- [ ] Freeze a bounded differential corpus covering:
  - static source-time read pre-issue;
  - runtime argument becoming concrete after a prior collect;
  - independent calls that may overlap;
  - branch taken/not taken;
  - zero and repeated loop occurrences;
  - argument mismatch and changed final source;
  - earlier Python exception;
  - physical success followed by logical denial;
  - cancellation, late completion and unused pre-issue;
  - denied write speculation;
  - sequential fallback for unsupported source.
- [ ] Define exact baseline observations separately:
  - final result or exception;
  - ordered logical Broker calls and receipts;
  - terminal workspace state;
  - physical starts/completions;
  - orphaned/cancelled/discarded attempts.
- [ ] Freeze the V1 ABI names and meanings. The preferred shape is:

```text
issue_or_reuse(slot, request) -> opaque compiler-owned handle
collect(handle) -> ordinary typed result or original logical error
finalize() -> terminal attempt dispositions
```

- [ ] Define static-site and dynamic-occurrence identity without dependency edges.
- [ ] State that collect remains at the original logical call site in V1.
- [ ] Define the sequential fallback before changing production code.

**Gate Contract:** The corpus and contract distinguish language, authority and physical-work observations; no field represents a Python dependency edge or continuation.

## Lane H: Unify the Host attempt lifecycle

**Outcome:** Source-time and runtime issue share one bounded Run-private table while Broker semantics remain unchanged.

Tasks:

- [ ] Extend or replace `SplitPhaseTable` so it can accept:
  - source-time preissued attempts;
  - runtime-issued attempts;
  - exact bind/reuse at final execution;
  - dynamic occurrences;
  - one terminal materialization/disposition path.
- [ ] Preserve positive Host admission for speculative physical work, including freshness, privacy/billing partition, cost/result limits and unconsumed policy.
- [ ] Keep logical permission, budget, call ordering, schema validation and receipts on the Broker path.
- [ ] Remove the configuration-level mutual exclusion between semantic pre-dispatch and split-phase calls only after the shared table proves both paths.
- [ ] Prove duplicate, mismatched, consumed, stale and unknown handles fail closed.
- [ ] Prove cancellation and uncertain completion never trigger implicit replay.
- [ ] Add race/lifecycle tests for concurrent issue, collect and finalize.
- [ ] Keep table evidence body-safe and bounded; do not add a scheduler graph or Python-value trace.

**Gate Host:** Unit and race tests show one physical attempt can originate before seal or during runtime and be consumed by exactly one matching logical call, with identical Broker receipts and no dependency representation.

## Lane C: Generalize seal-time split-phase lowering

**Outcome:** One target-CPython whole-program pass emits fixed issue/collect sites for typed Host operations without Future proxies.

Tasks:

- [ ] Generalize the narrow `split_phase_sources_read` patch through sealed capability projections and explicit pass registration.
- [ ] Keep the legality envelope intentionally small:
  - direct typed Host capability projections;
  - exact source locations;
  - literal or already-concrete arguments;
  - active branch/loop lowering;
  - no dynamic rebinding, opaque wrappers or unsupported call shapes.
- [ ] Compute earliest legal issue points from argument availability, control activation, Python evaluation safety and Host policy.
- [ ] Preserve original collect/logical-call positions and source locations.
- [ ] Emit stable static sites and runtime dynamic-occurrence handling.
- [ ] Emit `issue_or_reuse` for sites that may have a source-time attempt and ordinary issue behavior on a miss.
- [ ] Reject unsupported or ambiguous programs before Guest execution and select the unchanged sequential source.
- [ ] Compile the derived program once; perform no runtime AST analysis or rewriting.
- [ ] Add source, AST and derived-program identity checks sufficient for correctness without building a general optimizer framework.

**Gate Compiler:** Target-Guest tests prove straight-line, branch, loop and `A -> Python -> B` cases; unsupported cases run unchanged; no ordinary Python value is a Future proxy.

## Gate Integration: Integrated vertical slice

**Outcome:** One real Guest run demonstrates both issue opportunities through one table.

Required scenarios:

1. a literal read physically starts during source streaming, then `issue_or_reuse` binds it after seal;
2. a second call with a runtime-derived argument issues immediately after the Guest computes that argument;
3. independent Python work overlaps the second physical operation where the source program supplies a safe interval;
4. collects preserve original logical call order and Broker receipts;
5. a prior failure prevents later runtime issue by ordinary Python control flow;
6. an unused source-time issue receives an explicit terminal disposition;
7. disabling the pass produces ordinary sequential execution with equivalent result, exception, logical calls and workspace state.

Verification:

- [ ] exact CPython/WASI Guest E2E;
- [ ] differential oracle across enabled and sequential fallback;
- [ ] focused Go and Guest tests;
- [ ] targeted race run;
- [ ] independent read-only review of authority, identity, failure and cleanup paths.

**Gate Integration:** All scenarios pass on one integrated tree. If the slice requires a Host dependency graph, Python continuation or runtime AST mutation, stop and revise the architecture rather than adding one.

## Lane R: Retire overlapping execution paths

**Outcome:** The unified path is the only retained early-execution model; disabling it restores the sequential baseline.

Order matters:

- [ ] First route existing semantic pre-dispatch callers through the unified table.
- [ ] Then remove or hard-disable `CapabilityFutureProjection` and `_CapabilityFuture` projection code after parity tests pass.
- [ ] Then remove `SourceStreamingExecution` production wiring and retained-prefix Guest lifecycle after its negative/baseline evidence is sealed.
- [ ] Remove obsolete mutual-exclusion rules, counters, helpers and tests that assert retired architecture rather than observable behavior.
- [ ] Preserve research comparators only behind explicit research packages when required to reproduce negative results.
- [ ] Audit mechanism names, pass registry, config, docs and examples for stale activation paths.
- [ ] Do not retain compatibility adapters that make the runtime support both architectures indefinitely.

**Gate Retirement:** Searches and tests show no production path retains a partial Guest, emits Python Future proxies or maintains a separate pre-dispatch result table. Sequential fallback and the unified split-phase path both pass the corpus.

## Lane E: Evidence and economics

**Outcome:** The project measures what the simplification keeps, loses and improves without turning incompatible workloads into one headline.

Treatments:

1. ordinary complete-source sequential execution;
2. historical retained-prefix/local-streaming comparator;
3. historical Future-proxy/split-phase comparator where runnable;
4. unified source-time plus runtime split-phase execution.

Workload classes remain separate:

- fixed-argument external read during source generation;
- runtime-derived Host call with independent Python work after argument readiness;
- dependency-critical `A -> Python -> B` chain with no artificial overlap claim;
- branch/loop activation and unused speculation;
- pure local computation, demonstrating the deliberately removed source-generation overlap;
- failure, cancellation and mismatch controls.

Metrics:

- source arrival, seal, final Guest start and result time;
- analysis/lowering/compile overhead;
- issue lead time and wait at collect;
- logical and physical operation counts;
- unused/cancelled/discarded work and provider cost units;
- result, exception, receipt and workspace parity;
- peak/runtime resources only when a mechanism claim depends on them.

Rules:

- [ ] Freeze the bounded matrix and thresholds before final measurements.
- [ ] Reuse existing evidence when identities and treatment definitions still match; do not rerun merely to accumulate logs.
- [ ] Call an optimization net-negative only when matched evidence shows it.
- [ ] Report the lost local-streaming opportunity explicitly rather than claiming literal zero loss.
- [ ] Independently recalculate headline metrics from protected raw evidence.
- [ ] Keep committed aggregates body-safe and concise.

**Gate Evidence:** The unified path preserves external-read overlap and logical semantics, quantifies its overhead, and truthfully records any capability or performance loss from retiring local streaming and Future proxies.

## Lane D: Documentation and paper framing

**Outcome:** Runtime, research narrative and paper claims describe one architecture and preserve negative results.

Tasks:

- [ ] Mark retained-prefix Guest execution as an explored, semantically delicate path whose evaluated optimizations failed to justify its lifecycle complexity when supported by matched data.
- [ ] Explain the replacement as static split-phase execution:

```text
discover -> earliest policy-legal physical issue -> original logical collect
```

- [ ] State that the compiler may analyse dependencies but the Host protocol does not represent them.
- [ ] Distinguish source-time pre-issue from runtime AST-scheduled issue as two opportunities feeding one table.
- [ ] State the deliberate loss: no pure local Python execution before source seal.
- [ ] Separate Current, Measured, Rejected and Deferred claims.
- [ ] Update runtime/development/research docs and examples before touching thesis or defence artifacts.
- [ ] Treat thesis/report/deck reconciliation as an explicit owner decision after Gate Evidence, not an automatic roadmap step.

**Gate Documentation:** A source/code/doc audit finds one current architecture, no stale claim of unchanged final Python where a derived AST is used, and no claim that the unified model preserves source-generation overlap for local Python.

## Gate Closeout: Final closeout

Completion requires:

- [ ] one source-time and runtime issue mechanism over one Run-private attempt table;
- [ ] one deterministic seal-time lowering pass and one synchronous final execution;
- [ ] no production partial Guest continuation, Python Future proxy or Host dependency DAG;
- [ ] sequential fallback equivalence;
- [ ] exact real-Guest positive and adversarial evidence;
- [ ] race/lifecycle cleanup evidence;
- [ ] bounded economics and negative-result package;
- [ ] current documentation and explicit deferred items;
- [ ] full local gates, signed commits, pushed branch and clean upstream alignment.

Proportional final gates include:

```bash
git diff --check
go test ./... -count=1
go vet ./...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
env -u AGENT_RUNTIME_GUEST go test -race ./... -count=1
```

Build and bind an exact Guest artifact for final real-runtime claims. Use the bounded Linux workstation path only when a claim is platform-specific; do not manually trigger CI.

## Stop and owner-decision gates

Stop rather than silently expanding the design when:

- preserving required semantics appears to need Host dependency edges, arbitrary Future arguments or a Python continuation scheduler;
- useful collect sinking appears necessary but cannot be proven across a small private, non-observable, exception-transparent subset;
- general typed capability lowering requires an unsafe broad compiler or dynamic monkey-patching path;
- the unified path cannot preserve the existing external-read Study-A-style opportunity after proportional remediation;
- matched evidence shows lowering/runtime overhead dominates all admitted workload classes;
- retiring local streaming conflicts with a required current product consumer rather than a historical research comparator;
- exact Guest or required platform evidence is unavailable after bounded alternatives;
- thesis/report changes would alter submitted claims and require a separate owner decision.

## Deferred

- collect sinking beyond the original logical call;
- explicit `issue -> claim -> collect` three-stage authority protocol;
- speculative issue across unresolved branch paths;
- Host-side Future arguments or dependency graphs;
- arbitrary pure-Python code motion;
- external-write pre-issue;
- durable completed-result cache;
- general Python object transport;
- production-default enablement before natural-workload evidence.

## Tracking rules

- This file is the only execution source of truth for this refactor once approved.
- Maintain exactly one `Current execution pointer` below.
- Update lane status and the pointer after each coherent verified slice; do not append command transcripts or commit IDs.
- Link concise evidence artifacts or rejection summaries only when they change a gate.
- Remove stale future tasks rather than preserving multiple contradictory queues.
- A test pass, commit, review or phase completion is a checkpoint, not a stopping condition.
- Continue to the next independent unblocked slice until Gate Closeout, a stop condition or an owner decision.

## Current execution pointer

**Current:** Awaiting Yuzhe's approval of the desired state, frozen decisions, lane order and deliberate retirement of local streaming/Future proxies.

**Next after approval:** Lane 0, freeze the differential corpus and minimal ABI before production behavior changes.

**Blocked:** None. Implementation is intentionally not started until approval.
