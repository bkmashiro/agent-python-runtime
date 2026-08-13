# Megagoal: build the Full Composable Agent Runtime

Status: **Approved successor; implementation starts only through an explicit Hermes `/goal`.**
Date: 2026-08-13
Owner: Yuzhe
Execution repository: `~/projects/agent-python-runtime`
Predecessor proof: [`megagoal-streaming-authority-staged-execution.md`](megagoal-streaming-authority-staged-execution.md)
Supporting roadmap: [`proof-first-authority-roadmap.md`](proof-first-authority-roadmap.md)

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

- [ ] Reinspect baseline source/history and reconcile stale roadmap claims.
- [ ] Define a narrow internal feature-set/config object without freezing public
  CLI names.
- [ ] Define typed selected/fallback/deferred mechanism evidence with no private
  bodies or Host paths.
- [ ] Reject invalid feature combinations before Guest startup.
- [ ] Add baseline and pairwise off/on tests for currently implemented streaming,
  private attempt, staged-read, and Broker boundaries.
- [ ] Prove optimizations cannot widen a capability Plan.

**Exit gate:** fresh execution with all optional mechanisms disabled remains green;
selected/fallback modes are machine-readable and authority-equivalent.

### Track 1 — Complete staged-observation identity

**Promise:** A staged observation can be consumed only by the exact logical and
physical occurrence that owns it.

- [ ] Freeze a versioned identity over stream/workflow epoch, source and suite
  range, dynamic occurrence, canonical args, capability spec/handler/grant/policy,
  freshness/expiry, privacy partition, and parent lineage.
- [ ] Bind preflight records to final source identity at seal.
- [ ] Add mismatch tests for every identity dimension.
- [ ] Define terminal failure, timeout, cancellation, late response, orphan, and
  fallback-playback ownership.
- [ ] Prove strict occurrence playback with any function cache disabled.

**Do not:** turn occurrence-bound staging into argument-only global memoization.

### Track 2 — Portable immutable workspace roots and branches

**Promise:** Child filesystem state can branch and move independently of live
Guests or Linux COW.

- [ ] Explore the current manifest/Capsule/attempt substrate and choose the
  smallest immutable root and parent-lineage contract.
- [ ] Implement child derived roots/deltas without requiring memory COW.
- [ ] Prove branch-of-branch lineage across fresh Runs.
- [ ] Implement explicit select and expected-base conflict detection.
- [ ] Implement only the bounded compare/merge needed by the north-star; defer
  general three-way merge if it materially expands scope.
- [ ] Keep `/tmp` excluded and direct/private-attempt fallback explicit.
- [ ] Measure changed bytes, materialization, branch depth, and reachable garbage
  on deterministic fixtures.

**Do not:** serialize Host paths, authority references, credentials, or live handles.

### Track 3 — Streamed recursive Subagent fork/join

**Promise:** A parent can start useful child work before parent EOF without giving
unsealed work publication or write authority.

- [ ] Define one versioned structured child descriptor and source occurrence
  identity; do not infer arbitrary children from free-form Python.
- [ ] Stage two children before parent seal, each as a fresh Guest over a private
  immutable child root.
- [ ] Derive child Plan explicitly; do not inherit parent grants, budgets,
  approvals, transactions, or provider handles by default.
- [ ] Add bounded fan-out/depth/cancellation budgets.
- [ ] Implement explicit join/select; use merge only if Track 2 provides the
  required bounded semantics.
- [ ] Cascade parent invalid/cancel/timeout to child disposal and zero publication.
- [ ] Prove child invalidity cannot corrupt siblings or parent base.
- [ ] Record parent/child lineage and critical-path timeline without private bodies.
- [ ] Extend from two-child to recursive depth only if the same contract composes
  without broad scheduler work; otherwise defer recursion depth for joint review.

**Exit gate:** deterministic valid and parent-invalid fan-out fixtures pass with
fan-out off/on and identical selected semantic result.

### Track 4 — Content-addressed Agent Functions

**Promise:** Explicit admitted computations can reuse immutable local results while
ordinary work remains a fresh Run.

- [ ] Freeze binary `cacheable | not_cacheable` admission.
- [ ] Freeze canonical invocation identity over function/source, artifact/profile,
  admitted import closure, canonical input, immutable roots, deterministic
  settings, output schema, privacy partition, and policy epoch.
- [ ] Implement the smallest local project-private whole-function cache behind an
  internal toggle.
- [ ] Fail closed on Host capability calls, undeclared reads, shared writes,
  clock/random access, dynamic import, and unknown behavior.
- [ ] Store immutable output/result identity separately from execution evidence.
- [ ] Support bounded retention and eviction followed by safe fresh recomputation.
- [ ] Prove cache off/on output/root equivalence and no fabricated receipts/effects.
- [ ] Record hit/miss/recompute/materialization evidence without revealing bodies
  or cross-partition existence.

**Do not:** claim arbitrary Python purity or cache LLM/provider calls.

### Track 5 — Concurrent single-flight independent of retention

**Promise:** Concurrent identical admitted computations may share one in-flight
physical evaluation without requiring durable cache retention.

- [ ] Define leader/follower identity, cancellation, timeout, panic/error, and late
  completion ownership.
- [ ] Implement single-flight behind a separate toggle from durable cache.
- [ ] Prove retention off + single-flight on works.
- [ ] Prove single-flight off permits independent fresh evaluations.
- [ ] Prove one follower cancellation does not cancel a still-owned leader and a
  leader terminal failure does not strand followers.
- [ ] Prove privacy/policy/root/source mismatches never coalesce.
- [ ] Add race tests and bounded memory/entry cleanup tests.

### Track 6 — Explicit workflow and fresh re-evaluation

**Promise:** A waiting workflow releases its Guest and later continues from small,
explicit, immutable state—not a Python continuation.

- [ ] Define a minimal synchronous workflow skeleton with explicit compute,
  observation, wait/refresh, join, and terminal nodes.
- [ ] Persist only node identities, edges, immutable outputs/observations/roots,
  structured continuation input, freshness/policy, and disposition.
- [ ] Destroy the Guest at a deterministic wait boundary and prove destruction.
- [ ] Start a fresh Guest and re-evaluate unchanged compute nodes through local
  lookup.
- [ ] Execute the next live fixture read under current freshness/policy.
- [ ] Prove changed observation invalidates only transitive descendants.
- [ ] Prove cache eviction causes safe recomputation.
- [ ] Prove resume off falls back to an ordinary Harness-directed fresh Run.
- [ ] Measure retained explicit bytes, lookup/recompute counts, re-evaluation
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

- [ ] complete-source baseline;
- [ ] streaming without fan-out/cache/resume;
- [ ] fan-out off/on;
- [ ] function cache off/on;
- [ ] retention off with single-flight on;
- [ ] fresh re-evaluation off/on;
- [ ] invalid parent and invalid child;
- [ ] changed freshness/policy/privacy/source/root identity;
- [ ] branch conflict;
- [ ] cache eviction;
- [ ] cancellation at each active boundary;
- [ ] every implemented mechanism disabled together.

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

- [ ] Re-audit historical prepared/COW source and current Wazero/artifact lifecycle.
- [ ] Census Guest and Host-native state under the copy-or-broker rule.
- [ ] Spike the smallest never-served, single-use prepared baseline behind
  capability detection and an off-switch.
- [ ] If supported without weakening freshness, implement and test it; otherwise
  record exact blocker and defer.
- [ ] On Linux only, probe whether exact prepared memories and fixed-memory artifact
  contracts support private COW safely.
- [ ] If bounded and truthful, implement optional COW with fresh fallback;
  otherwise defer it with source/runtime evidence.
- [ ] Verify exact artifact/profile, authority reset, private workspace, `/tmp`,
  Broker namespace, cancellation, replacement, and no stale Host handles.
- [ ] Run deterministic fresh/prepared/COW treatment parity before any performance
  interpretation.
- [ ] Measure startup phase, request phase, teardown/refill, shared/private memory,
  and completion rate separately on an authorized Linux environment.

**Stop/reframe:** Do not recreate a VM, fork Wazero, or claim linear-memory-only
continuation merely to obtain COW. A well-supported deferral is an acceptable
outcome for this track.

### Track 9 — Evidence, hardening, and real-workload acceptance handoff

**Promise:** Final claims are independently checkable and the next real-workload
experiment is ready but not silently run.

- [ ] Version records for every implemented mechanism and fallback.
- [ ] Add negative corruption/identity-substitution fixtures.
- [ ] Prove strict playback independently of function cache.
- [ ] Extend independent verification only to claims backed by Runtime records.
- [ ] Audit docs for Current/Proposed/Deferred truth and remove stale claims.
- [ ] Produce a final mechanism matrix: implemented, rejected, deferred for joint
  review, exact reason, evidence, and smallest next decision.
- [ ] Produce a deterministic measurement summary without extrapolating to real
  Agent workloads.
- [ ] Draft the separate real repository-shaped Agent acceptance contract for
  Yuzhe/Hermes joint review; do not execute it in this Megagoal.
- [ ] Run final local and real-Guest gates, verify signed commits/pushes and a clean
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

`Track 0 — rediscover live baseline, freeze the internal feature-set/evidence seam,
and add the first off-state/invalid-combination RED tests.`

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

## Completion log

Append only gate-backed checkpoints. Each entry should include date, track/slice,
RED/GREEN evidence, focused/global gate result, signed commit, and next pointer.
Do not mark umbrella tracks complete from implementation presence alone.

- 2026-08-13 — Created and approved successor Megagoal from clean baseline
  `e3715fc7`. No successor implementation started.

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
