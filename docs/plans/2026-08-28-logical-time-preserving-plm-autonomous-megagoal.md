# Logical-Time-Preserving PLM Autonomous Mega-Goal

> **Status (2026-08-28): active successor architecture; implementation intentionally not started.** The owner requested design review and a ready-to-run megagoal now, followed by execution later. This file is the only active execution pointer for the migration.

**Goal:** Replace the predecessor `issue/collect` interpretation with a Run-owned, logical-time-preserving Prepare/Linearize/Materialize model that preserves original Host-call semantics for temporally changing external worlds, fixes the independently reported lifecycle/authority defects, and earns any latency claim through matched end-to-end evidence.

**Architecture:** Source streaming performs authority-free analysis and may create only positively admitted Host-private candidates. After source seal, one exact target-Guest lowering emits internal PLM calls into one synchronous CPython execution. Physical preparation may move earlier; linearization stays at the original logical Host-call point; V1 materialization stays at that same point. A candidate is adopted only when an operation-specific temporal validator proves that it denotes an outcome allowed at linearization; otherwise the Host starts the canonical operation there. Python receives no Future, and the Host does not execute Python expressions or maintain a Python dependency graph.

**Primary design:** [`docs/research/logical-time-preserving-split-phase-execution.md`](../research/logical-time-preserving-split-phase-execution.md)

**Predecessor implementation/evidence:**

- [`2026-08-27-unified-split-phase-execution-roadmap.md`](2026-08-27-unified-split-phase-execution-roadmap.md)
- [`unified-split-phase-v1-contract.md`](../research/unified-split-phase-v1-contract.md)
- [`unified-split-phase-evidence-v1.md`](../research/unified-split-phase-evidence-v1.md)

**Project:** `/Users/yuzhe/projects/agent-python-runtime`

---

## Migration decision

Migrate the semantic architecture, but reuse the verified substrate.

The current implementation is the predecessor PLM slice:

```text
issue_or_reuse                 -> prepare
Broker-routed Materialize      -> linearize + materialize
collect at original call       -> L=M at the original logical point
```

Retain:

- incremental source analysis without Agent-source execution;
- exact sealed-source AST lowering;
- one ordinary synchronous CPython execution for Agent code;
- Host-owned physical attempt lifecycle;
- Broker-owned logical permission, budget, order and receipts;
- static source site plus dynamic occurrence;
- branch/loop reachability expressed by CPython;
- unchanged synchronous fallback;
- exact Guest, race and differential test infrastructure;
- negative economics evidence.

Replace or version deliberately:

- `issue_or_reuse` as the complete semantic model;
- Plan-only `SplitPhaseTable` ownership;
- arbitrary-Broker materialisation;
- `FreshnessPlanEpoch` as sufficient freshness evidence;
- direct claim of a physically prepared result without temporal validation;
- unbounded duplicate-reuse event recording;
- product/research gates enforced only through one constructor surface;
- stale active-plan prose that describes retired paths as live.

Do not relabel the predecessor immutable-read correctness result or the `+151.40%` cold fixture as PLM evidence. New behavior receives new contract, pass, artifact and evidence versions.

## Why the migration is required

### Temporal semantics

`read_only + idempotent + plan_epoch` does not imply that a result observed at physical time `tP` remains valid when CPython reaches the source call at `tL`. A current stock-price read, unversioned database lookup or mutable filesystem read can be read-only yet temporally unstable.

PLM adds an explicit semantic event:

```text
candidate prepared at tP
-> validate/adopt or restart at tL
-> concrete value/exception materialized at tM
```

### Host ownership

Independent bounded review found four implementation defects that must be corrected before any closeout:

1. exported lower-level Wazero constructors bypass `LegacyResearchExecution` validation;
2. a split-phase table is Plan-bound but not immutably Run/Broker-bound and accepts an arbitrary Broker at materialisation;
3. already-started source-time work can outlive setup failures because finalization ownership is installed too late;
4. exact duplicate reuse appends unbounded telemetry without consuming another call entry.

PLM's Run-owned context is the architectural repair, not a compatibility adapter around those defects.

### Economics

The predecessor matched cold exact-Guest fixture measured:

```text
baseline median   3.755490702 s
unified median    9.441244409 s
median delta      5.685753707 s
change            +151.40%
```

The mechanism preserved physical overlap, but the extra cold analyzer/lowering Guest dominated two independent 150 ms reads. PLM must reduce or avoid that cost before any latency claim.

## Value function

Prioritise, in order:

1. logical-time and authority correctness;
2. one Run owner for Broker, candidates, jobs and cleanup;
3. operation-specific sound temporal validation;
4. unchanged synchronous fallback;
5. smallest compiler and ABI surface;
6. bounded physical/economic/provider evidence;
7. end-to-end economics after correctness;
8. formal claims no stronger than the implemented subset and proof obligations.

A correct default-off mechanism with a negative economics result is acceptable research. Do not weaken temporal semantics to rescue performance.

## Core model

### Time domains

```text
t ∈ T   physical wall-clock time
ℓ ∈ L   source-program logical point
```

For each Host operation:

```text
tP <= tL <= tM
P_h -> L_h -> M_h
```

### Environment

```text
Theta_t = (external world, authority, execution context, quota, nonce,
           provider session, snapshot/version/lease state, ...)
```

### Contract

```text
Materialize(
  Linearize(
    Prepare(request, tP),
    actual request,
    Theta_tL),
  tM)

must produce an outcome allowed by the original Host operation at tL.
```

For nondeterministic tools, require membership in the allowed outcome relation, not equality to one invented canonical value.

### Evidence domains

Keep three projections separate:

```text
logical visible trace
provider/economic physical trace
Host-private lifecycle trace
```

A prepare event may be hidden from the Python trace only when the Host contract admits its provider/economic consequences and proves non-interference with later logical outcomes.

## Run-owned lifecycle

Introduce one narrow owner, provisionally named `RunPLMContext` in the plan. Do not commit to the final public type name before the RED contract tests.

It owns:

```text
immutable Run identity
immutable Plan identity
exactly one Broker
bounded candidate/job table
physical/cost/result/event budgets
cancellation and terminal cleanup
```

Required invariants:

- no table exists without Run and Plan identity;
- one Broker and one table are atomically joined before physical preparation;
- a table cannot be attached to or consumed by another Broker;
- materialisation does not accept an arbitrary Broker parameter;
- all public constructors apply the same mechanism policy;
- cleanup ownership exists before a source-time candidate can start;
- every candidate/job reaches one bounded terminal state;
- exact duplicate calls cannot grow evidence without bound;
- no uncertain physical outcome is implicitly replayed.

## Minimal V1 ABI

Conceptual internal calls:

```text
prepare(site, occurrence, request, contract) -> candidate_ref
linearize(site, occurrence, actual_request, candidate_ref?) -> job_ref
materialize(job_ref) -> ordinary result or exception
discard(candidate_ref)
finalize(success)
```

These are trusted Guest/Host bridge operations, not user-callable authority tokens.

V1 may fold calls when semantics allow:

```text
no candidate                  prepare + linearize
original-site materialize     linearize + materialize
ordinary unsupported call     prepare + linearize + materialize
```

Python source never receives `candidate_ref` or `job_ref`.

## Initial temporal vocabulary

Version a minimal Host-authored contract rather than infer from `read_only`:

```text
IMMUTABLE
SNAPSHOT
VERSIONED
LEASED
CURRENT
WALLCLOCK_OBSERVING
```

Initial behavior:

- `IMMUTABLE`: prepare result early; exact identity check at linearization;
- `SNAPSHOT`: prepare result bound to an immutable snapshot;
- `VERSIONED`: prepare result plus sound version evidence; validate at linearization;
- `LEASED`: prepare result plus provider-guaranteed lease; validate time/epoch safely;
- `CURRENT`: prepare transport/session only; execute final read at linearization;
- `WALLCLOCK_OBSERVING`: no PLM movement in V1.

Do not implement heuristic TTL as strict validity. Approximate/stale-tolerant semantics require a separate explicit tool contract and are outside this megagoal.

## Compiler model

The compiler may temporarily represent:

```text
P_h -> L_h -> M_h
```

and the smallest required data/control/effect/exception/temporal edges. It then lowers those facts into ordinary synchronous code and discards the graph.

V1 compiler scope:

- direct typed Host-call assignments;
- exact source spans;
- literal or already-concrete arguments;
- statement-local/basic-block preparation placement;
- branch-local linearization only after CPython enters the branch;
- dynamic loop occurrence;
- original-site `L=M`;
- all-or-nothing transformation with unchanged-source fallback.

No general SSA, PDG, interprocedural optimizer, Python scheduler or runtime AST rewrite.

## Explicit non-goals

Do not implement or claim during this megagoal:

- Python-visible Futures, promises or `await`;
- Host-side Python expression evaluation;
- Host dependency DAG or continuation scheduler;
- arbitrary materialize sinking;
- movement across `try/finally`, opaque calls, reflection or observable timing;
- cross-branch result speculation without a separately admitted budgeted contract;
- prepare/commit external writes;
- generic compensation or rollback;
- heuristic freshness promoted to strict equivalence;
- online global schedule optimisation;
- multi-level DNS/TLS/auth/request phase decomposition;
- universal commutativity/effect inference;
- proof of full CPython;
- thesis, report or defence-deck claim changes before evidence closure;
- deployment, package publication, paid cloud or manually triggered GitHub Actions.

## Repository and execution rules

- Main controller owns shared contracts and integration. Delegate only bounded read-only reviews with independent value.
- Use TDD for every behavior change: record the missing behavior, observe RED, make the smallest GREEN change, then run the broader gate.
- Prefer existing `SplitPhaseTable`, Broker, source-pass and exact-Guest seams only where their ownership/semantics remain valid. Do not build a parallel framework and leave both active.
- Once the PLM vertical slice is verified, delete or version-replace the predecessor path rather than add a facade chain.
- Make coherent signed commits and push after each closed gate. Continue automatically during the later execution run unless a stop condition fires.
- Do not manually trigger GitHub Actions. Use local full/race gates and bounded `gpu31` Linux/Guest validation.
- Keep body-bearing source, request and provider results private. Commit only body-safe aggregate evidence.
- Never rewrite predecessor evidence or preregistration after observing results.

## Global validation commands

Use focused commands while developing and the repository wrapper for broad gates:

```bash
cd /Users/yuzhe/projects/agent-python-runtime

scripts/unified-split-phase-gate.sh focused
scripts/unified-split-phase-gate.sh race
scripts/unified-split-phase-gate.sh full

git diff --check
go vet ./...
```

For exact Guest work:

```bash
AGENT_RUNTIME_GUEST=/absolute/path/to/agent-python-runtime.wasm \
  scripts/unified-split-phase-gate.sh guest
```

Build the final exact Guest on bounded Linux infrastructure when macOS cannot establish artifact parity. Bind final evidence to source commit, artifact SHA-256, profile, Plan, contract and test matrix.

## Stop conditions

Stop and record the smallest counterexample when:

1. a candidate validator cannot provide sound operation-specific temporal evidence;
2. a `CURRENT` operation requires final-value prefetch rather than transport-only preparation;
3. safe materialize movement requires a general Python effect/exception framework;
4. Run/Broker ownership cannot be made one-shot without breaking a documented required consumer;
5. candidate preparation can change later logical outcomes through quota, nonce, billing, rate-limit or provider interaction and no explicit non-interference contract exists;
6. correctness requires replay after any physical effect whose `not_started` state is unproven;
7. exact Guest cost remains negative after one bounded lower-cost lowering alternative and one prepared-capacity alternative;
8. the formal theorem would require hiding provider-visible interfering events or pretending wall-clock equality;
9. external mutation, paid resources, deployment or a product semantic decision needs owner approval.

When a stop fires, do not weaken the gate or continue into materialize sinking. Record the finding, preserve the predecessor fallback and continue only with independent unblocked documentation/evidence work.

---

# Autonomous execution queue

## Gate 0: Activate PLM and close predecessor bookkeeping

**Promise:** There is one active architecture pointer and no stale claim that the predecessor is the final model.

- [x] Confirm clean signed baseline and no sibling writer.
- [x] Mark the 2026-08-27 unified roadmap as a historical predecessor implementation/evidence record.
- [x] Reconcile its stale `Current baseline` section with the post-refactor implementation.
- [x] Keep the predecessor contract/evidence immutable except status links and factual errata.
- [x] Resolve the active-doc pass-count ambiguity: catalog entries and one pipeline instance's `MaxPasses` are different scopes.
- [x] Qualify “already formally proven” in the issue/collect report as a conditional proof candidate with open obligations.
- [x] Link this megagoal from README, product direction and development workflow without describing PLM as implemented.
- [x] Run docs/link/diff checks, sign, push and continue.

**Gate G0:** Exactly one `Current execution pointer` exists in the active plan; current implementation, proposed architecture and historical evidence are distinguished.

## Gate 1: Freeze the PLM contract and oracle

**Promise:** Candidate, job, logical invocation and provider-visible preparation have separate semantics before runtime code changes.

Primary files:

- `docs/research/logical-time-preserving-split-phase-execution.md`
- `docs/research/logical-time-plm-v1-contract.md`
- `runtime/capability/` contract tests
- bounded fake-world/oracle fixtures under `runtime/` or `research/`

Tasks:

- [x] Freeze versioned candidate, job and terminal-state schemas.
- [x] Freeze logical-visible, provider/economic and Host-private evidence projections.
- [x] Define the baseline operation relation over a deterministic fake world with version, snapshot, lease, authority epoch, quota and provider session state.
- [x] Define strict temporal modes and reject unknown/missing metadata.
- [x] Define operation-specific validator soundness and explicit fallback.
- [x] Define prepared-failure policy: canonical restart by default; adopt only a sound
  operation-specific stable-failure certificate.
- [x] Require authority recheck at every linearization even when preparation bound an
  earlier authority epoch.
- [x] Define one linearization per reached dynamic occurrence.
- [x] Define source-order constraints for non-commuting temporal reads.
- [x] Define exception and cancellation placement with `L=M` at the original call.
- [x] Freeze RED matrix identities before behavior changes.
- [x] Include immutable, snapshot, valid/invalid version, valid/expired lease, current, wallclock-observing, authority revocation, request mismatch and provider-visible quota cases.

**Gate G1:** Every admitted row has a complete temporal/authority/non-interference explanation. Unknown evidence falls back or rejects; no heuristic TTL passes as strict validity.

## Gate 2: Establish Run ownership and repair lifecycle defects

**Promise:** No physical candidate exists outside one immutable Run/Broker owner.

Primary files:

- `runtime/capability/split_phase.go`
- `runtime/capability/broker.go`
- `runtime/engine/wazero/engine.go`
- focused capability/engine tests

RED cases:

- lower-level public constructor attempts to enable legacy streaming/pre-dispatch without the research gate;
- table created or reused without exact Run identity;
- table consumed by a foreign Broker with the same Plan;
- arbitrary Broker passed to materialisation;
- Broker/table double attachment;
- setup failure after source-time preparation but before Guest execution;
- duplicate exact prepare repeated beyond event budget;
- cancellation/late completion racing finalization.

Tasks:

- [x] Make one Run owner bind Run identity, Plan identity, Broker and candidate/job table atomically.
- [x] Remove arbitrary-Broker materialisation from the table API.
- [x] Make attachment one-shot and impossible after any logical call or physical start.
- [x] Apply mechanism gates through every exported constructor surface.
- [x] Install cleanup ownership before a factory can return or start source-time physical work.
- [x] Bound event evidence separately from candidate count; preserve aggregate counters without overflow.
- [x] Preserve one-shot finalization and uncertain-outcome no-replay semantics.
- [x] Run focused/race/full gates, sign, push and continue.

**Gate G2:** The four independent Host-review findings reproduce before the fix and are closed by direct tests. No table or attempt can cross a Run/Broker identity.

## Gate 3: Implement the minimal P + (L=M) Host vertical slice

**Promise:** An immutable candidate is not a logical operation until the original source call linearizes it.

Tasks:

- [x] Version the predecessor `issue_or_reuse` contract into explicit candidate preparation.
- [x] Add candidate states: prepared/running/ready/failed/cancelled/discarded.
- [x] Add job states created only by successful original-point linearization.
- [x] Make Broker logical admission occur exactly once at linearization.
- [x] Adopt an exact immutable candidate without a second physical start.
- [x] On candidate mismatch or invalidity, discard/detach it and start the canonical operation at linearization.
- [x] Keep result/exception materialization at the same original point.
- [x] Preserve permission denial, logical order and receipt identity.
- [x] Preserve physical failure hiding until linearization and no logical receipt for unlinearized candidates.
- [x] Delete or version-replace predecessor APIs after the new path is integrated; do not retain two product tables.

The predecessor names and compiler/Guest/Host bridge were removed after Gate 5 exact-Guest parity; PLM uses one candidate/job table and one inline lowering path.

**Gate G3:** One real immutable operation proves prepare-before-source-call, original-point linearization, one physical start on valid adoption, canonical restart on invalid candidate, ordinary Python value/exception and exact Broker evidence.

## Gate 4: Add temporal contracts and validators

**Promise:** Result preparation is admitted by resource semantics, not by `read_only` appearance.

Tasks:

- [x] Replace `FreshnessPlanEpoch` as the complete freshness model with a versioned temporal contract while retaining Plan/authority binding separately.
- [x] Add `IMMUTABLE` and `SNAPSHOT` validators.
- [x] Add one deterministic fake `VERSIONED` adapter with a sound version check.
- [x] Add one provider-guaranteed `LEASED` fixture with monotonic/epoch-safe validation.
- [x] Add `CURRENT` transport/session-only preparation and prove no final value is read before linearization.
- [x] Reject `WALLCLOCK_OBSERVING` movement.
- [x] Add default `RETRY_AT_LINEARIZE` failure handling and one explicit
  `STABLE_FAILURE` fixture; never surface a prepare-time exception early.
- [x] Bind candidate certificates to request, resource, handler, site/occurrence, Run, Plan, authority epoch and mode evidence.
- [x] Reject changed source seal, arguments, authority, provider session, snapshot/version/lease and resource identity.
- [x] Record validation request/cost/provider effects separately from the logical operation.
- [x] Prove a provider-visible prepare that consumes outcome-affecting quota is rejected unless its contract explicitly preserves later semantics.

**Gate G4:** Immutable/snapshot/versioned/leased candidates adopt only under sound evidence; current reads execute at linearization; no absent/unknown mode fails open.

## Gate 5: Lower sealed source to PLM without a Python-visible Future

**Promise:** Ordinary synchronous CPython retains all control flow and data computation while the compiler emits only the smallest PLM scaffolding.

Primary files:

- `guest/bootstrap/agent_runtime/source_pass.py`
- `guest/bootstrap/agent_runtime/__init__.py`
- `runtime/sourcepatch/`
- `runtime/passplugin/`
- `runtime/engine/wazero/`
- Guest and exact E2E tests

Tasks:

- [x] Version `split_phase_capability_calls` into the PLM pass; do not silently change v1 evidence identity.
- [x] Emit prepare candidate handles and original-point linearize/materialize calls for direct typed assignments.
- [x] Keep source-time candidate identity compatible with final sealed source site and dynamic occurrence.
- [x] Keep runtime-derived prepare after actual arguments become concrete.
- [x] Keep branch-local linearization after CPython enters the branch.
- [x] Keep loop occurrences exact and bounded.
- [x] Preserve argument evaluation, exception and source-location order.
- [x] Reject the whole transform on helper shadowing, reflection, unsupported wrappers, ambiguous source or unsafe statement movement.
- [x] Prove unsupported source executes byte-identical original code synchronously.
- [x] Delete obsolete predecessor bridge names after exact Guest parity.

**Gate G5:** Exact Guest tests pass source-time candidate adoption, runtime-derived `A -> code -> B`, branches, loops, mismatches, invalidation, earlier exception, failure/discard and pass-off fallback without Python Futures or Host DAG state.

## Gate 6: Remove cold lowering from the critical path or reject the optimisation economics

**Promise:** PLM does not repeat the predecessor's known extra cold-Guest cost without an explicit measurement decision.

Evaluate only two bounded alternatives before choosing:

1. **same final Guest pre-execution lowering:** validate and lower sealed source inside the never-served final Guest before its one Agent-source execution;
2. **one Run-private prepared analyzer/lowering capacity:** reuse the existing bounded authority-free analyzer lifecycle without executing Agent source and without cross-Run mutable state.

Tasks:

- [ ] Freeze matched baseline/predecessor/PLM timing protocol before implementation measurements.
- [ ] Instrument instantiate, initialize, runtime init, analysis, lowering, compile/load, prepare, validation, provider, linearization, materialization and final execution spans.
- [x] Implement the smaller viable alternative first.
- [x] Preserve exact target-Guest AST authority and all-or-nothing pre-execution selection.
- [x] Prove no second Agent-source execution, retained Agent interpreter or cross-Run mutable analyzer.
- [ ] Measure cold end-to-end and equivalently pre-provisioned profiles separately.
- [ ] If the first alternative cannot pass the mechanism or cost gate, try the second once; do not create a third framework.
- [ ] Record memory, discarded capacity and fallback costs.

**Gate G6-M:** Exact source/AST/result/exception/authority parity and one formal Agent-source execution hold.

**Gate G6-E:** At least one frozen non-trivial latency regime has positive median end-to-end saving after all analysis/lowering/validation/provider costs, with positive saving in at least 4/5 trials. If both bounded alternatives fail, retain correct PLM default-off and record the negative result.

## Gate 7: Differential, temporal and fault campaign

**Promise:** The implementation survives changing worlds and adversarial lifecycle order, not only immutable happy paths.

Required cases:

- immutable candidate ready/not ready;
- snapshot exact/mismatch;
- version unchanged/changed during prepare/changed after validation;
- lease valid/expired/revoked/clock-epoch mismatch;
- current read with transport-only prepare;
- two ordered current reads;
- authority revoked before linearization;
- quota/rate-limit interference;
- branch not taken and candidate discard;
- zero/multiple loop occurrences;
- earlier Python exception;
- physical failure before logical point;
- logical denial after physical success;
- setup failure before Guest execution;
- cancellation before/while/after validation;
- late completion and uncertain provider outcome;
- foreign Run/Broker/table/candidate/job identity;
- pass-disabled unchanged-source baseline.

Tasks:

- [ ] Generate bounded small programs over the admitted subset.
- [ ] Randomize provider completion order and version invalidation points deterministically by seed.
- [ ] Compare visible return/exception, logical calls, receipt order and final external state.
- [ ] Separately compare provider/economic and Host-private lifecycle projections.
- [ ] Run race and repeated teardown tests.
- [ ] Independently recalculate body-safe aggregates.

**Gate G7:** Transformed visible traces refine the baseline model for every admitted row; all other rows fall back or reject before unsafe execution. Provider/economic differences remain explicit.

## Gate 8: Formal core and claim calibration

**Promise:** Formal language matches the implementation rather than proving a stronger imagined optimizer.

Tasks:

- [ ] Define a small baseline calculus with assignment, sequence, condition, Host call and exception.
- [ ] Define PLM candidate/job states and small-step labelled transitions.
- [ ] Define logical-visible, provider/economic and internal labels.
- [ ] State exactly when internal labels may be hidden.
- [ ] Prove or carefully sketch prepare stuttering under non-interference.
- [ ] Prove sound candidate adoption from validator soundness.
- [ ] Prove invalid-candidate canonical restart at linearization.
- [ ] Prove `L=M` original-site exception/order preservation for V1.
- [ ] Prove untaken admitted speculation is invisible only under silent-discard and provider non-interference assumptions.
- [ ] Prove a forward simulation for the implemented direct-assignment subset.
- [ ] Label all unmechanised obligations and do not claim full CPython proof.
- [ ] Replace the predecessor report's unconditional “already formally proven” sentence with conditional wording.

**Gate G8:** Every theorem assumption maps to a versioned runtime/tool/compiler check or is explicitly identified as an external adapter obligation.

## Gate 9: Optional research reserves

These are not required for PLM V1 closeout. Admit each only through a separate owner decision after G6/G7:

- basic-block materialize sinking across proven pure/total/noexcept statements;
- cross-branch candidate speculation;
- just-in-time prepare windows from provider-guaranteed validity;
- demand promotion and contention-aware candidate priority;
- multi-level transport/auth/request preparation;
- prepare/commit external writes.

Do not leave unchecked tasks for these in the active closeout queue. If approved later, create a separate successor plan with its own evidence identity.

## Gate 10: Documentation and closeout

**Promise:** Code, evidence and claims describe one implemented architecture with no stale predecessor pointer.

Tasks:

- [ ] Update README, architecture, product direction, pass catalog and development commands.
- [ ] Mark predecessor paths Historical/Removed/Research-only accurately.
- [ ] Link exact contract, Guest artifact and body-safe evidence.
- [ ] Report strict temporal modes separately from current/approximate operations.
- [ ] Report logical and provider/economic traces separately.
- [ ] Keep predecessor `+151.40%` result unchanged and report the new matched result independently.
- [ ] Do not update thesis/report/deck current-system claims until implementation, exact Guest and evidence gates pass.
- [ ] Run focused, full, race, vet, Python and exact Guest gates.
- [ ] Run bounded independent Host ownership, compiler lowering, temporal contract and evidence reviews.
- [ ] Fix every High/Medium causal defect or record a named stop decision.
- [ ] Sign final commits, push, verify upstream and leave a clean worktree.

**Gate G10:** One active source of truth, no open High/Medium correctness findings, all required gates green, exact artifact/evidence linked, signed upstream clean.

---

## Current execution pointer

**Current:** Gates 0 through 5 are complete. The PLM pass and Guest ABI are versioned independently, the predecessor bridge is removed, exact Guest control-flow/failure/fallback cases pass and sealed-source lowering now runs inside the one final Guest before its one Agent-source execution.

**Next:** Complete Gate 6: freeze the matched timing protocol, add stage spans, measure cold and equivalently pre-provisioned profiles and record the economics decision without changing the two-alternative limit.

**Blocked:** No blocker.
