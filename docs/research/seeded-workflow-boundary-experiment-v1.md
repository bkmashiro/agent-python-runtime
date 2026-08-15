# Seeded workflow-boundary paired experiment v1

Status: **Implemented and rerun against a real Guest; one balanced 14-task measurement only**
Date: 2026-08-15

## Boundary

`research/workflowbench` is an experiment driver over prepared explicit workflow nodes. It
is not a Runtime scheduler and gives neither the semantic overlay nor Lab execution
authority. Submission order is produced by a fixed local xorshift shuffle from a recorded
seed. Baseline and optimized treatments consume the exact same sealed manifest.

The grammar includes deterministic local model invocation/output fixtures, real Guest WASM
compute, typed local read fixtures, waits, repeated workflow nodes, Harness-declared
independence, freshness partitions and near-match negatives. The eight negative dimensions
are arguments, freshness, resource, privacy, authority, source, artifact and workflow
identity. Portable benchmark evidence contains identities and metrics rather than private
prompt/tool bodies; the separate private Agent trajectory may materialize those bodies.

## Treatment behavior

- `baseline` disables every optimization and performs one fresh physical read per logical
  request in program order;
- `preissue` shifts one necessarily-reached prepared read after qualification but before
  demand;
- `declared_parallel` overlaps only the two requests explicitly declared independent by the
  prepared task;
- `coalesced` links two exact in-flight logical requests to one measured producer;
- `retained_reuse` links a later exact request to a successfully completed producer;
- near matches execute separately and retain a rejected canonical decision.

Every task emits its own bounded `pysolate.workflow-boundary-observation.v0` report. This
keeps Build/Decode limits meaningful while the experiment evidence binds all reports, the
full manifest, seed, task metrics, terminal equivalence and a final seal.

The driver recomputes the ordered tool-result oracle from measured physical executions,
checks it against the manifest, and folds that value together with the actual WASM response
digest and fixed model-output fixture digest. Baseline and optimized outputs must match
exactly. A WASM response drift, missing physical result or manifest-oracle drift fails the
experiment closed. The workload contains only read-only tool fixtures, so the effects oracle
is the validated no-external-write ledger rather than an inferred Python effect.

## Balanced order and CPU accounting

The v0 driver always ran baseline before optimized. V1 removes that confound: the seeded
manifest order deterministically alternates treatment order, producing seven
`baseline_optimized` and seven `optimized_baseline` pairs.

Each treatment records:

- monotonic wall duration;
- process user+system CPU-time delta;
- physical execution count;
- decision and equivalence metrics.

The process CPU delta includes every Harness goroutine active during the treatment and is
not a Guest-only CPU claim. Unsupported platforms explicitly record CPU accounting as
`unavailable`; they do not synthesize zero-cost evidence.

## Measurement labels

Typed Host read and Guest WASM intervals use actual monotonic timings. Model
invocation/output intervals are deterministic local replays and remain labelled `replayed`.
The real Guest command invokes the verified artifact for both treatments of every task.
Call-count reductions and trace topology are measured facts for the frozen fixture. Wall and
CPU deltas remain bounded observations rather than representative population claims.

## Gates

A valid `pysolate.workflow-benchmark-evidence.v1` bundle requires:

- 14 tasks in the exact seeded order and one valid paired report per task;
- exactly seven pairs in each treatment order;
- explicit CPU accounting policy and exact aggregate CPU sums;
- identical terminal output and read-only effect ledger for both treatments;
- no unclassifiable divergence;
- all optimization-positive tasks to carry one admitted Host-recorded decision;
- all eight near-match tasks to retain one rejected decision and no physical reduction;
- optimized aggregate physical executions not to exceed baseline;
- `consumer_admitted=false` in every observation report.

Any Guest failure, manifest mutation, unknown evidence field, seal drift, malformed identity,
cross-report metric mismatch or output divergence fails closed.
