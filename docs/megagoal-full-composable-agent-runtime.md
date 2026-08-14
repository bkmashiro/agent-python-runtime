# Megagoal: build the Full Composable Agent Runtime

Status: **Historical/complete; superseded for active execution by the Semantic Execution Experimental Megagoal.**
Date: 2026-08-13
Owner: Yuzhe
Execution repository: `~/projects/agent-python-runtime`
Active successor: [`plans/2026-08-14-semantic-execution-autonomous-megagoal.md`](plans/2026-08-14-semantic-execution-autonomous-megagoal.md)
Predecessor proof: [`megagoal-streaming-authority-staged-execution.md`](megagoal-streaming-authority-staged-execution.md)
Supporting roadmap: [`proof-first-authority-roadmap.md`](proof-first-authority-roadmap.md)
Final decision matrix: [`full-composable-runtime-mechanism-matrix.md`](full-composable-runtime-mechanism-matrix.md)
Deterministic summary: [`composable-runtime-measurement-summary.md`](composable-runtime-measurement-summary.md)
Post-goal acceptance contract: [`real-agent-composable-runtime-acceptance.md`](real-agent-composable-runtime-acceptance.md)

> **For Hermes:** This is the long-running `/goal` handoff. Read this file fully,
> inspect live repository state, and execute it across multiple independently
> verified slices. A green slice, signed checkpoint, push, or context boundary is
> not a stopping condition.

Preparing or updating this file does not itself start implementation, schedule a
job, or authorize production/external side effects. Yuzhe starts execution
explicitly with `/goal`.

## Mission

Build and verify the smallest truthful **Full Composable Runtime** in which a
streaming parent Agent can stage fresh child Agents over immutable workspace
branches, explicit cacheable computations can be reused or single-flighted on
one trusted Host, and a waiting workflow can destroy its Guest and continue by
fresh re-evaluation—without changing Pysolate's authority, freshness,
publication, or terminal-disposition semantics.

The integrated deterministic north-star is:

```text
append-only streaming parent
→ exact Guest admission of a closed child descriptor
→ fresh child fork over immutable private workspace root
→ duplicate explicit cacheable compute requested concurrently
→ one physical evaluation under optional single-flight
→ child reaches a typed wait/refresh boundary and its Guest is destroyed
→ fresh Guest re-evaluates the explicit workflow
→ unchanged completed nodes resolve from immutable local outputs
→ changed observation invalidates only transitive descendants
→ parent explicitly joins/selects child results
→ complete source, authority, and workspace identities seal
→ selected derived root/result publishes; all other attempts are discarded
```

The corresponding negative fixture must cover parent/child invalidity, cache
identity mismatch, freshness/policy/privacy mismatch, branch conflict,
single-flight leader failure, cache eviction, cancellation, and every optional
mechanism disabled.

This Megagoal proves composability with deterministic fixtures. It does **not**
claim representative Agent performance. After this roadmap reaches its stop
condition, Yuzhe and Hermes will jointly design and accept a separate real
repository-shaped Agent workload evaluation.

## User intent and value function

Yuzhe wants a successor larger than the completed streaming proof, but not a
checklist-driven rewrite. For every mechanism:

1. inspect implementation and history first;
2. spike the smallest design that preserves current boundaries;
3. write deterministic RED evidence;
4. implement the smallest independently useful mechanism and explicit off-state;
5. test it alone and with its nearest dependency/fallback;
6. if implementation cost is unexpectedly high, requires broad redesign, harms
   another mechanism, or lacks a truthful contract, record the evidence and mark
   it `Deferred for joint review` rather than forcing it;
7. continue with independent executable work;
8. present all deferred decisions together after the feasible composition closes.

Prioritize, in order:

1. authority/publication correctness and fresh-Guest semantics;
2. a working deterministic end-to-end composition;
3. mechanism independence and truthful fallbacks;
4. simple, reversible designs with bounded state;
5. measurement that can decide retention or deletion;
6. implementation breadth.

A larger autonomy budget authorizes many safe slices, not speculative architecture
churn.

## Repository and sources of truth

Before editing, read and reconcile at least:

- `docs/megagoal-full-composable-agent-runtime.md` (this file);
- `docs/proof-first-authority-roadmap.md`;
- `docs/content-addressed-agent-functions.md`;
- `docs/wait-suspension-and-reuse-tradeoffs.md`;
- `docs/streaming-authority-staged-execution.md`;
- `docs/authority-lifecycle.md`;
- `runtime/streaming/`;
- `runtime/workspace/`;
- `runtime/capability/`;
- `runtime/engine/wazero/`;
- `runtime/playback/`, `runtime/observe/`, and `runtime/receipt/`;
- `research/evaluation*`, `research/labstore/`, and relevant integration tests;
- Guest bootstrap, ABI, build scripts, artifact manifest, and profile admission.

Trust live Git state and current source over this document's baseline. If a later
commit already implements an item, verify it against this contract before marking
it complete.

## Baseline at approval

The signed and pushed baseline is:

```text
e3715fc7e18febc0ed42de57eef9d6da0ba48744
feat: prove streaming authority-staged execution
```

At that baseline:

- append-only source sessions and exact target-Guest incremental admission exist;
- complete suites execute in one private Guest namespace before EOF;
- private workspace attempts publish only after successful final Guest response;
- qualified literal reads can eager preflight with bounded speculation accounting;
- other reads are dynamic-reach-gated;
- staged eager results are occurrence-bound and consumed once;
- streaming write capabilities are absent and Broker denies pre-seal writes;
- relative Guest timeline and eager dispatched/consumed/orphaned counters exist;
- a rebuilt real WASI Guest passes the single-program streaming north-star;
- recursive streamed child fan-out, general immutable branches, Agent Functions,
  single-flight, fresh re-evaluation, and current-path Prepared/COW do not exist;
- real external-write commit/reconciliation is deliberately outside this goal.

## Non-negotiable architecture boundaries

1. **Fresh means fresh.** Each parent/child execution and post-wait evaluation uses
   a fresh, single-use Guest. No Guest, Broker, capability handle, `/tmp`, frame,
   heap, module global, descriptor, or WASM memory becomes workflow continuation
   state.
2. **No ambient authority.** No shell, subprocess, native binary, ambient network,
   arbitrary Host filesystem, package installation, credentials, or Broker bypass.
3. **Ordinary Guest filesystem API.** Agent code uses Python stdlib `open`,
   `pathlib`, `shutil`, and related APIs against rooted filesystems. Do not restore
   a `pysolate.fs` or workspace facade.
4. **Host-owned authority.** Every live read/write/effect remains behind the typed
   capability Broker and frozen per-Run Plan. Child creation cannot clone grants,
   budgets, approvals, transactions, or provider handles implicitly.
5. **Portable state is explicit.** Workflow state may contain typed node/edge
   identity, canonical structured input, immutable observations/results,
   filesystem root lineage, policy/freshness decisions, and terminal status—not
   interpreter or Host-native state.
6. **Publication is separate from execution.** Child and parent branches remain
   private until explicit validated select/join/publication. Failure or abandonment
   discards unpublished roots/results.
7. **Caching never fabricates execution/effects.** Cacheable nodes cannot perform
   Host calls, live reads, external writes, shared workspace writes, undeclared
   filesystem reads, implicit clock/random access, or dynamic imports.
8. **Identity is conservative.** Unknown source, import closure, artifact/profile,
   input, root, output schema, partition, freshness, or policy state means a miss
   or rejection—not a guessed hit.
9. **COW is not semantics.** Immutable roots and explicit outputs define truth.
   Prepared runtime and Linux memory COW are optional worker-local accelerators
   with ordinary fresh execution as control and fallback.
10. **No hidden external writes.** This goal may stage a deterministic write intent
    only to prove the barrier. It must not dispatch a real provider write.
11. **No production or paid-provider experiment.** Use deterministic local fixture
    adapters. Do not deploy, publish, mutate production, use paid model calls, or
    trigger metered GitHub workflows.
12. **No overclaim.** Do not claim production readiness, arbitrary Python purity,
    distributed cache, complete deterministic replay, provider exactly-once,
    universal latency gains, or truthful heap continuation.
13. **Privacy partitioning.** No cache lookup existence, staged observation, result
    body, filesystem body, or evidence payload crosses configured user/project
    partitions.
14. **Minimal dependencies.** Prefer existing packages and the standard library.
    Do not add broad frameworks merely to represent a small state machine.

## Explicit non-goals

Do not implement without fresh approval:

- real provider external-write commit, approval, reconciliation, or compensation;
- arbitrary Python region purity inference or automatic hot-region extraction;
- Python frame/heap/dirty-page continuation snapshots;
- global, distributed, cross-machine, cross-project, or cross-user result caches;
- general workflow language/compiler or distributed scheduler;
- general CRDT or unbounded filesystem merge;
- automatic recursive fan-out of arbitrary source syntax;
- package installation, shell compatibility, arbitrary MCP, or Computer fallback;
- Lab/UI polish before Runtime evidence exists;
- automatic optimizer decisions beyond bounded retain/evict observations approved
  by this roadmap;
- real repository-shaped Agent benchmark before joint acceptance after mechanism
  completion.

## Exploration and deferral protocol

Before implementing each track, add a short entry to **Decision and Deferral
Ledger** below containing:

```text
mechanism / current source seam / simplest candidate / expected touched packages
hard dependencies / likely risks / focused RED gate / decision
```

Allowed decisions:

- `Proceed minimal`: bounded design, no material conflict;
- `Spike only`: uncertainty can be answered by a disposable bounded experiment;
- `Deferred for joint review`: evidence shows high cost, architecture conflict,
  missing backend contract, or poor expected value;
- `Rejected`: contradicts a non-negotiable boundary.

A deferral is not a Megagoal blocker while another independent track remains
executable. Preserve the spike, measurements, or source citations needed for the
final joint review; do not leave a vague “too hard” note. Throwaway spike code
must not enter production packages unless converted into tested minimal code.

Before adopting a complex design, compare it with deletion/no-op and the ordinary
fresh fallback. Prefer the smallest option that closes the deterministic claim.

## Mechanism matrix

Every implemented mechanism requires an explicit off-state and nearest-neighbor
composition tests. The final matrix must cover at least:

| Mechanism | Off-state/control | Required composition |
| --- | --- | --- |
| Streaming | complete-source fresh Run | private attempt + reach gating |
| Staged read | normal Broker dispatch at reach | source/plan/freshness/partition identity |
| Child fan-out | Harness starts child after parent seal | immutable branch + parent disposition |
| Immutable branch | direct/private-attempt fallback | recursive child lineage + select/conflict |
| Function cache | fresh function evaluation | immutable roots + partition + eviction |
| Single-flight | independent concurrent evaluations | cache disabled and retention disabled variants |
| Fresh re-evaluation | ordinary Harness-directed fresh Run | cache hit/miss + fresh live observation |
| Prepared runtime | ordinary fresh instantiation | cache/fan-out behavior unchanged |
| Linux memory COW | non-COW prepared or fresh path | exact artifact/profile and fallback reporting |

Invalid combinations must fail before Guest startup or deterministically select a
reported fallback. An optimization's off/on setting must not change semantic
output, allowed authority, publication, or effect disposition.

## Autonomous execution queue

Tracks are ordered by dependency, but independent work may continue around an
evidence-backed deferral. Do not start a later optimization if its semantic
substrate is still ambiguous.

### Track 0 — Baseline, feature set, and evidence vocabulary

**Promise:** Optional mechanisms are explicit, explainable, and removable.

- [x] Reinspect baseline source/history and reconcile stale roadmap claims.
- [x] Define a narrow internal feature-set/config object without freezing public
  CLI names.
- [x] Define typed selected/fallback/deferred mechanism evidence with no private
  bodies or Host paths.
- [x] Reject invalid feature combinations before Guest startup.
- [x] Add baseline and pairwise off/on tests for currently implemented streaming,
  private attempt, staged-read, and Broker boundaries.
- [x] Prove optimizations cannot widen a capability Plan.

**Exit gate:** fresh execution with all optional mechanisms disabled remains green;
selected/fallback modes are machine-readable and authority-equivalent.

### Track 1 — Complete staged-observation identity

**Promise:** A staged observation can be consumed only by the exact logical and
physical occurrence that owns it.

- [x] Freeze a versioned identity over stream/workflow epoch, source and suite
  range, dynamic occurrence, canonical args, capability spec/handler/grant/policy,
  freshness/expiry, privacy partition, and parent lineage.
- [x] Bind preflight records to final source identity at seal.
- [x] Add mismatch tests for every identity dimension.
- [x] Define terminal failure, timeout, cancellation, late response, orphan, and
  fallback-playback ownership.
- [x] Prove strict occurrence playback with any function cache disabled.

**Do not:** turn occurrence-bound staging into argument-only global memoization.

### Track 2 — Portable immutable workspace roots and branches

**Promise:** Child filesystem state can branch and move independently of live
Guests or Linux COW.

- [x] Explore the current manifest/Capsule/attempt substrate and choose the
  smallest immutable root and parent-lineage contract.
- [x] Implement child derived roots/deltas without requiring memory COW.
- [x] Prove branch-of-branch lineage across fresh Runs.
- [x] Implement explicit select and expected-base conflict detection.
- [x] Implement only the bounded compare/merge needed by the north-star; defer
  general three-way merge if it materially expands scope.
- [x] Keep `/tmp` excluded and direct/private-attempt fallback explicit.
- [x] Measure changed bytes, materialization, branch depth, and reachable garbage
  on deterministic fixtures.

**Do not:** serialize Host paths, authority references, credentials, or live handles.

### Track 3 — Streamed recursive Subagent fork/join

**Promise:** A parent can start useful child work before parent EOF without giving
unsealed work publication or write authority.

- [x] Define one versioned structured child descriptor and source occurrence
  identity; do not infer arbitrary children from free-form Python.
- [x] Stage two children before parent seal, each as a fresh Guest over a private
  immutable child root.
- [x] Derive child Plan explicitly; do not inherit parent grants, budgets,
  approvals, transactions, or provider handles by default.
- [x] Add bounded fan-out/depth/cancellation budgets.
- [x] Implement explicit join/select; use merge only if Track 2 provides the
  required bounded semantics.
- [x] Cascade parent invalid/cancel/timeout to child disposal and zero publication.
- [x] Prove child invalidity cannot corrupt siblings or parent base.
- [x] Record parent/child lineage and critical-path timeline without private bodies.
- [x] Extend from two-child to recursive depth only if the same contract composes
  without broad scheduler work; otherwise defer recursion depth for joint review.

**Exit gate:** deterministic valid and parent-invalid fan-out fixtures pass with
fan-out off/on and identical selected semantic result.

### Track 4 — Content-addressed Agent Functions

**Promise:** Explicit admitted computations can reuse immutable local results while
ordinary work remains a fresh Run.

- [x] Freeze binary `cacheable | not_cacheable` admission.
- [x] Freeze canonical invocation identity over function/source, artifact/profile,
  admitted import closure, canonical input, immutable roots, deterministic
  settings, output schema, privacy partition, and policy epoch.
- [x] Implement the smallest local project-private whole-function cache behind an
  internal toggle.
- [ ] Fail closed on Host capability calls, undeclared reads, shared writes,
  clock/random access, dynamic import, and unknown behavior. **Deferred for joint
  review for arbitrary Guest Python; current Host-instrumented Guard fixtures are
  bounded and must not be generalized.**
- [x] Store immutable output/result identity separately from execution evidence.
- [x] Support bounded retention and eviction followed by safe fresh recomputation.
- [x] Prove cache off/on output/root equivalence and no fabricated receipts/effects.
- [x] Record hit/miss/recompute/materialization evidence without revealing bodies
  or cross-partition existence.

**Do not:** claim arbitrary Python purity or cache LLM/provider calls.

### Track 5 — Concurrent single-flight independent of retention

**Promise:** Concurrent identical admitted computations may share one in-flight
physical evaluation without requiring durable cache retention.

- [x] Define leader/follower identity, cancellation, timeout, panic/error, and late
  completion ownership.
- [x] Implement single-flight behind a separate toggle from durable cache.
- [x] Prove retention off + single-flight on works.
- [x] Prove single-flight off permits independent fresh evaluations.
- [x] Prove one follower cancellation does not cancel a still-owned leader and a
  leader terminal failure does not strand followers.
- [x] Prove privacy/policy/root/source mismatches never coalesce.
- [x] Add race tests and bounded memory/entry cleanup tests.

### Track 6 — Explicit workflow and fresh re-evaluation

**Promise:** A waiting workflow releases its Guest and later continues from small,
explicit, immutable state—not a Python continuation.

- [x] Define a minimal synchronous workflow skeleton with explicit compute,
  observation, wait/refresh, join, and terminal nodes.
- [x] Persist only node identities, edges, immutable outputs/observations/roots,
  structured continuation input, freshness/policy, and disposition.
- [x] Destroy the Guest at a deterministic wait boundary and prove destruction.
- [x] Start a fresh Guest and re-evaluate unchanged compute nodes through local
  lookup.
- [x] Execute the next live fixture read under current freshness/policy.
- [x] Prove changed observation invalidates only transitive descendants.
- [x] Prove cache eviction causes safe recomputation.
- [x] Prove resume off falls back to an ordinary Harness-directed fresh Run.
- [x] Measure retained explicit bytes, lookup/recompute counts, re-evaluation
  latency, and Guest instance-time released in the deterministic model.

**Do not:** persist frames, heap, globals, FDs, `/tmp`, Broker state, WASM memory,
or a Sandbox/Guest identity.

### Track 7 — Integrated deterministic composability north-star

**Promise:** All feasible semantic mechanisms work together and fail closed.

Build a versioned deterministic fixture containing:

```text
streaming parent with controlled chunk delays
→ two staged children over sibling immutable roots
→ duplicate cacheable compute requested concurrently
→ single-flight treatment on/off
→ one child wait boundary and Guest destruction
→ fresh re-evaluation with unchanged and changed-observation variants
→ explicit child join/select
→ valid seal/publication or invalid/cancel/conflict discard
```

Required treatment matrix:

- [x] complete-source baseline;
- [x] streaming without fan-out/cache/resume;
- [x] fan-out off/on;
- [x] function cache off/on;
- [x] retention off with single-flight on;
- [x] fresh re-evaluation off/on;
- [x] invalid parent and invalid child;
- [x] changed freshness/policy/privacy/source/root identity;
- [x] branch conflict;
- [x] cache eviction;
- [x] cancellation at each active boundary;
- [x] every implemented mechanism disabled together.

Required machine-readable evidence:

- logical workflow/node and physical execution counts;
- Guest creation/destruction and no surviving waiting Guest;
- parent/child/root lineage and terminal dispositions;
- staged-read dispatch/consume/orphan counts;
- cache hit/miss/eviction/recompute and single-flight leader/follower counts;
- node invalidation lineage;
- relative monotonic critical-path timeline;
- changed/materialized bytes and bounded local retained-state bytes;
- selected/fallback mechanism modes;
- no private file/result bodies, Host paths, credentials, absolute Host time, or
  cross-partition lookup existence.

The deterministic harness controls delays. Fixture delay is not provider latency,
and test wall time is not a product-performance result.

### Track 8 — Prepared runtime and Linux memory COW exploration

**Promise:** Optional worker-local lifecycle optimization accelerates the same
semantics or is deferred/rejected with evidence.

This track begins only after the semantic north-star works through ordinary fresh
execution.

- [x] Re-audit historical prepared/COW source and current Wazero/artifact lifecycle.
- [x] Census Guest and Host-native state under the copy-or-broker rule.
- [x] Spike the smallest never-served, single-use prepared baseline behind
  capability detection and an off-switch.
- [x] If supported without weakening freshness, implement and test it; otherwise
  record exact blocker and defer.
- [x] On Linux only, probe whether exact prepared memories and fixed-memory artifact
  contracts support private COW safely.
- [x] If bounded and truthful, implement optional COW with fresh fallback;
  otherwise defer it with source/runtime evidence.
- [x] Verify exact artifact/profile, authority reset, private workspace, `/tmp`,
  Broker namespace, cancellation, replacement, and no stale Host handles.
- [x] Run deterministic fresh/prepared/COW treatment parity before any performance
  interpretation.
- [ ] Measure startup phase, request phase, teardown/refill, shared/private memory,
  and completion rate separately on an authorized Linux environment. **Deferred:
  the exact Linux probe rejected COW admission before performance interpretation;
  only the fixture preparation duration was retained as non-performance evidence.**

**Stop/reframe:** Do not recreate a VM, fork Wazero, or claim linear-memory-only
continuation merely to obtain COW. A well-supported deferral is an acceptable
outcome for this track.

### Track 9 — Evidence, hardening, and real-workload acceptance handoff

**Promise:** Final claims are independently checkable and the next real-workload
experiment is ready but not silently run.

- [x] Version records for every implemented mechanism and fallback.
- [x] Add negative corruption/identity-substitution fixtures.
- [x] Prove strict playback independently of function cache.
- [x] Extend independent verification only to claims backed by Runtime records.
- [x] Audit docs for Current/Proposed/Deferred truth and remove stale claims.
- [x] Produce a final mechanism matrix: implemented, rejected, deferred for joint
  review, exact reason, evidence, and smallest next decision.
- [x] Produce a deterministic measurement summary without extrapolating to real
  Agent workloads.
- [x] Draft the separate real repository-shaped Agent acceptance contract for
  Yuzhe/Hermes joint review; do not execute it in this Megagoal.
- [x] Run final local and real-Guest gates, verify signed commits/pushes and a clean
  tree.

## Deterministic experiment contract

All autonomous development experiments use deterministic local fixtures:

- canonical source/chunk/node/branch/input identities;
- injected monotonic clock or relative elapsed time;
- bounded fixture reads with declared delay and counters;
- no paid model, live network, production account, or external provider;
- strict JSON models with unknown-field and trailing-data rejection where they
  cross persistent/evidence boundaries;
- exact treatment plan and expected cardinality;
- semantic validation independent of JSON Schema;
- raw bounded samples/counters retained when derived metrics are reported;
- mechanism acceptance separated from product/performance qualification.

Before adding a benchmark surface, state the question, mechanism, control,
measured boundary, expected row count, and stop rule. Do not use ordinary parallel
CI test timing as latency evidence.

## Test and delivery cadence

For each executable slice:

1. inspect current source/history and update the decision ledger;
2. write a RED test, or record why a docs/source-only spike cannot start with RED;
3. implement the smallest bounded change;
4. run focused package/Guest tests and `git diff --check`;
5. update this roadmap checkboxes, current execution pointer, decision/deferral
   ledger, and completion log;
6. run risk-proportionate integration/race/static gates;
7. make a meaningful signed commit and push to `main` unless the current slice is
   an intentionally throwaway spike or remote operation is forbidden;
8. verify signature, `origin/main`, and tree state;
9. immediately continue to the next executable slice.

Use at most two concurrent subagents, only for genuinely independent lanes with
independent value and non-overlapping files. The main controller owns shared
contracts, integration, diff review, final gates, commits, and pushes. Do not add
ritual second-opinion reviews after ordinary slices.

Protect GitHub Actions quota: do not manually trigger Guest artifact or heavy CI
workflows by default. Prefer local focused/full gates and the explicitly approved
ICL/Slurm environment for necessary Linux artifact validation. A remote job is
not accepted without exact source/artifact identity, application-level success,
complete checksums, and retrieved evidence.

## Global gates

Run focused gates during development. Before each behavior commit, run the
relevant subset and `git diff --check`. At integration boundaries and final
closeout, run:

```bash
GOTOOLCHAIN=go1.26.5 go test ./... -count=1
GOTOOLCHAIN=go1.26.5 go test -race <changed-stateful-packages> -count=1
GOTOOLCHAIN=go1.26.5 go vet ./...
python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s tests -p 'test_*.py'
python3 -m compileall -q guest/bootstrap/agent_runtime examples/controller-boundaries
bash -n guest/build/build-guest.sh
git diff --check
```

Real Guest behavior changes additionally require a rebuilt Linux x86_64 Guest and
the relevant exact-artifact E2E. Prepared/COW claims require Linux-specific gates;
macOS compilation alone is not evidence. Do not run a mismatched artifact profile
against unrelated full E2E tests and report expected admission failures as
regressions.

## Stop conditions

Continue automatically after every successful slice. Stop only when one of these
conditions is proven against live state:

1. all feasible executable items are complete, all final gates pass, and every
   remaining item is explicitly `Deferred for joint review` or `Rejected` with
   evidence;
2. a product decision from Yuzhe is necessary and no independent executable track
   remains;
3. a required resource/permission is unavailable and no deterministic/local
   alternative or independent track remains;
4. gates repeatedly expose a conflict whose safe resolution requires a broad
   architecture rewrite or weakening a non-negotiable boundary;
5. continuing would risk external side effects, credentials, data, or concurrent
   worktree ownership not authorized here.

A difficult mechanism, one deferred item, a green checkpoint, signed commit,
push, context compaction, remote queue delay, or desire to summarize progress is
not a stop condition. If one mechanism is expensive/conflicting, record it and
continue all independent work.

If blocked, report the exact blocker, evidence, modified files, tests/gates,
current Git status, deferred matrix, and safest decision options. Never manufacture
completion by weakening an acceptance condition.

## Current execution pointer

`Complete — all feasible deterministic mechanisms are implemented and verified;
arbitrary Guest-Python purity, general merge/multi-wait scheduling, and Linux COW
are Deferred for joint review with explicit fresh/off fallback. The separate real
Agent acceptance contract is prepared but was not executed.`

Update this pointer after every verified slice. It must always name the next
concrete executable seam or the exact final blocker review.

## Decision and Deferral Ledger

Add entries here before each track implementation.

- 2026-08-13 — Goal formation: approved Full Composable Runtime scope. Use
  deterministic fixtures during autonomous implementation; defer real
  repository-shaped Agent workload to joint post-completion acceptance. Explore
  the simplest solution for each mechanism first. If cost, architecture conflict,
  or cross-mechanism harm is material, record evidence as `Deferred for joint
  review` and continue independent work. Real external writes remain excluded.
- 2026-08-13 — Track 0 / `Proceed minimal`: the narrowest seam is a Host-owned
  zero-default `runtime.MechanismSet` inside `RunConfig`, plus versioned
  `MechanismEvidence`. Dependencies are validated before Wazero parses the
  artifact; unavailable requested modes resolve to stable `fallback/unavailable`;
  mode records contain no paths or data bodies. `single_flight` intentionally has
  no cache dependency. Streaming now requires explicit streaming + private
  workspace selection, while the all-off path remains the default. Capability
  grants are neither derived nor mutated by mechanism resolution.
- 2026-08-13 — Track 1 identity contract / `Proceed minimal`: use a private,
  one-shot Host record rather than argument-keyed memoization. The versioned
  identity binds stream/workflow epochs, final source and suite range/digest,
  dynamic occurrence, canonical arguments, capability spec/handler/plan/grant,
  freshness/expiry, privacy partition, and parent lineage. Provisional records
  may omit only the unknown final source digest; seal binding is exact and
  one-way. Terminal failure/timeout/cancel/late/orphan clears the staged body.
- 2026-08-13 — Track 2 / `Proceed minimal`: model a portable sealed root as a
  digest-only lineage record layered over the existing Capsule and private copy.
  A child is mutable until seal; seal verifies the expected base, computes a
  portable workspace digest plus changed-entry/byte counters, and makes the root
  immutable. Sealed roots support branch-of-branch and Capsule transfer into a
  new Manager. Explicit select is sufficient for the north-star, so general
  three-way merge is `Deferred for joint review`: it would add conflict policy
  without a selected integrated fixture requiring it. Existing direct workspace
  and private Attempt paths remain unchanged fallbacks.
- 2026-08-13 — Track 3 / `Proceed minimal`: use an explicit versioned descriptor
  and a bounded Host orchestrator, not free-form Python discovery or a general
  scheduler. `Stage` starts child execution immediately on private branches;
  `Seal` performs explicit select and destroys unselected roots; invalid/cancel/
  timeout cascades cancellation and branch discard. Descriptor identity binds the
  child Plan but the runner factory receives no parent Plan/grants. A dedicated
  executor creates and retires one single-use Runner per child. Recursive depth 2
  composes without new scheduler machinery. Real Wazero Guest fan-out remains an
  integrated-fixture gate rather than being claimed from fake runners.
- 2026-08-13 — Tracks 4/5 / `Proceed minimal`: an Agent Function is an explicit
  binary-admitted Host invocation, not inferred pure Python. Its key binds source,
  artifact/profile/import closure, canonical inputs, immutable roots,
  deterministic settings, output schema, privacy partition, project, and policy.
  Results live in a bounded 0700 project-private store with digest verification,
  corruption-as-miss, atomic replacement, eviction, and body-free counters.
  Single-flight is a separate in-memory group: concurrent identical keys share a
  leader, completed values are immediately forgotten, follower cancellation does
  not own the leader, and panic/error always releases followers. The current
  `Guard` proves deterministic Host fixture boundaries only; actual Guest-level
  unknown-behavior enforcement stays open for the integrated fixture and must not
  be described as arbitrary Python purity.
- 2026-08-13 — Track 6 / `Proceed minimal`: implement exactly one explicit wait
  per versioned synchronous graph; a general multi-wait DAG scheduler is not
  required for the north-star. State contains graph/edge identity, node result
  digests and bodies, observation freshness/policy, immutable root identities,
  canonical continuation input, wait position, and disposition—never frames,
  heap, FDs, `/tmp`, Wasm memory, or Guest identity. The Guest is always closed
  at suspend and completion; resume requires an exact graph/root binding, creates
  a fresh Guest, refreshes live observations fail-closed, and invalidates only
  recorded transitive descendants. Multi-wait/general scheduler remains
  `Deferred for joint review` unless real acceptance work needs it.
- 2026-08-13 — Track 7 / `Proceed minimal`: one deterministic real-Wazero fixture
  now proves both child Guests complete before parent EOF, sibling-private roots,
  explicit select, cache/single-flight reuse, wait-time Guest destruction, fresh
  resume, typed digest-only evidence, and parent-invalid disposal. Mechanism-off
  equivalence and the remaining mismatch/cancellation/conflict treatments are
  covered by the same package matrix plus focused mechanism fixtures; fixture
  wall time is not performance evidence.
- 2026-08-13 — Track 8 prepared / `Proceed minimal`: use one never-served,
  single-use initialized module per Engine when explicitly selected. It has its
  own `/tmp`, is consumed at most once, is destroyed if unused, and all later
  Runs fall back to ordinary fresh instantiation. Do not restore or recycle it.
- 2026-08-13 — Track 8 COW / `Deferred for joint review` pending Linux probe:
  historical commits `b405c58` and `f6ced98` implemented sealed-memfd linear
  memory and a large prepared pool, but commit `2afd41b` deliberately removed the
  execution-strategy, artifact-census, pool, allocator, refill, and evidence
  stack during the core-PoC reduction. Current public Wazero state cannot prove
  reset of module-instance state, WASI host state, tables/passive segments, or
  unexported mutable globals. The current probe therefore records exact blockers
  and fresh fallback; re-importing the deleted subsystem would be a broad
  architecture reversal, not a minimal COW patch. The exact Linux x86_64 probe
  subsequently passed prepared/fresh parity but reported one non-imported,
  non-fixed memory and the same module/WASI/static-state blockers;
  `MemoryCOWCandidate=false`. Its checksummed body-free result is tracked at
  `docs/evidence/full-composable-linux-prepared-cow.json`; remote staging was
  deleted only after downloaded checksum verification.
- 2026-08-13 — Post-completion COW amendment / `Proceed outcome-qualified`:
  Yuzhe requires Linux COW to remain. The acceptance boundary now requires
  observable result/isolation correctness rather than proof-complete internal
  state reset. Restore only fixed-memory admission, sealed memfd baseline, and
  one MAP_PRIVATE mapping per request. A served, cancelled, failed, or
  shape-drifted slot is always closed/unmapped and never returned to a pool;
  memory growth is rejected by the fixed profile and discards the slot. The
  low-level Linux mapping contract passes; full CPython/WASI qualification is the
  remaining gate.
- 2026-08-13 — Post-completion COW qualification closure: an exact-source
  `cow-fixed` CPython/WASI artifact (2048 initial == maximum pages) passed
  Prepared parity and selected the Linux COW lane. Consecutive requests preserved
  result semantics and isolated Python globals, `/tmp`, and imported modules;
  later fresh slots recovered after cancellation and allocation pressure. All
  result bundles were checksum-verified before remote cleanup. COW is retained as
  Experimental, outcome-qualified, and mandatory in the real scenario matrix.

## Completion log

Append only gate-backed checkpoints. Each entry should include date, track/slice,
RED/GREEN evidence, focused/global gate result, signed commit, and next pointer.
Do not mark umbrella tracks complete from implementation presence alone.

- 2026-08-13 — Created and approved successor Megagoal from clean baseline
  `e3715fc7`. No successor implementation started.
- 2026-08-13 — Track 0 RED/GREEN: added zero-default mechanism selection,
  dependency validation, stable selected/fallback/off evidence, pre-artifact
  rejection, explicit streaming enablement, and grant non-widening coverage.
  Focused `runtime`, `runtime/streaming`, `runtime/engine/wazero`, and integration
  packages passed; race passed for the three changed Runtime packages; next is
  Track 1 identity.
- 2026-08-13 — Track 1 identity slice: added a versioned one-shot Host record,
  exhaustive identity mismatch tests, terminal disposition handling, exact
  SourceSeal suite binding, and Plan-derived spec/handler/grant identities. This
  freezes the contract but does not yet claim the existing Guest-local eager
  table is fully replaced; the next slice is its execution-path integration.
- 2026-08-13 — Track 2 mechanism slice: added portable immutable root records,
  expected-base seal conflict, recursive lineage, Capsule rebind in a fresh
  Manager, explicit select, and immutable-write rejection. Deterministic focused
  and race tests passed; aggregate reachability/materialization measurements stay
  open for the integrated fixture.
- 2026-08-13 — Track 3 mechanism slice: added structured descriptors, bounded
  concurrent staging, private branch ownership, cancellation/invalidity cleanup,
  explicit select, depth-2 recursion, digest-only relative timeline, sibling/base
  isolation, and a fresh single-use Runner executor. Focused and race tests pass;
  real Guest proof remains open for Track 7.
- 2026-08-13 — Tracks 4/5 mechanism slice: added binary Agent Function admission,
  full canonical invocation identity, bounded private disk retention, verified
  result records, corruption/eviction recomputation, authority guard fixtures,
  and independent single-flight with cancellation/panic cleanup. Focused and race
  tests pass; Guest-level unknown behavior enforcement stays open.
- 2026-08-13 — Track 6 mechanism slice: added a graph-bound single-wait workflow,
  explicit persisted state, fresh Guest factory lifecycle, observation refresh,
  transitive invalidation, eviction recomputation, resume-off fallback, and
  body-bounded metrics. Focused and race tests pass; aggregate wait instance-time
  measurement remains for Track 7.
- 2026-08-13 — Track 7 integrated slice: added a real-Wazero deterministic
  composable north-star, real child completion-before-EOF proof, digest-only
  evidence, branch materialization/garbage metrics, all-off fallback matrix, and
  real parent-invalid child disposal. Focused real-Guest tests pass with the
  recovered exact artifact; a fresh Linux rebuild remains a final provenance gate.
- 2026-08-13 — Track 8 prepared slice: added one optional initialized single-use
  module, prepared/fresh semantic parity, isolated `/tmp`, one-use accounting,
  unused-slot destruction, and a versioned COW blocker probe. Focused and race
  tests pass; exact Linux probe remains next.
- 2026-08-13 — Track 8 Linux closure: after one wrapper-only retry for this
  cluster's absent `SLURM_TMPDIR`, the checksummed clean-source-bound Linux test
  passed. It confirmed prepared/fresh parity and rejected COW candidacy because
  the one memory was non-fixed plus non-memory reset blockers. The evidence bundle
  was downloaded and verified before ACK/remote deletion. COW and its performance
  treatment are deferred for joint review.
- 2026-08-13 — Track 9 evidence slice: added strict body-free composable evidence
  decoding/claim verification, negative unknown-field/private-body/identity
  substitution fixtures, a Current/Experimental/Deferred mechanism matrix, a
  deterministic non-performance summary, and a separate real repository-shaped
  acceptance contract. Arbitrary Guest-Python purity remains explicitly deferred.
- 2026-08-13 — Final closure: full Go/vet/Python/syntax/diff gates, focused races,
  and the five-test real-Guest composable/streaming set passed on current source.
  Prepared cancellation consumes and retires its slot; replacement is an explicit
  fresh Engine, not an implicit pool refill. All feasible roadmap gates are closed.

## Final reporting contract

When the stop condition is satisfied, report concisely:

1. implemented mechanism matrix and integrated deterministic result;
2. exact gates and real-Guest/Linux evidence actually obtained;
3. deferred/rejected dossier with reasons and joint decisions required;
4. claims supported and claims explicitly unsupported;
5. signed commit/push/clean-tree state;
6. path to the proposed real Agent workload acceptance contract.

## Short prompt to start this Megagoal

```text
Read docs/megagoal-full-composable-agent-runtime.md fully, then execute it in ~/projects/agent-python-runtime. Do not stop after one successful slice: explore the simplest safe implementation for each mechanism, update the roadmap, run gates, signed commit and push, then continue. If one item is disproportionately costly or conflicts with another mechanism, record evidence as Deferred for joint review and continue independent work. Use deterministic fixtures only; do not run the later real Agent workload or any real external write. Stop only at the roadmap's proven completion/blocker conditions.
```
