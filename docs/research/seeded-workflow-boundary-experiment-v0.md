# Seeded workflow-boundary paired experiment v0

Status: **Implemented; real Guest evidence generated separately from the committed driver**
Date: 2026-08-15

## Boundary

`research/workflowbench` is an experiment driver over prepared explicit workflow nodes. It
is not a Runtime scheduler and gives neither the semantic overlay nor the Lab execution
authority. Submission order is produced by a fixed local xorshift shuffle from a recorded
seed. Baseline and optimized treatments consume the exact same sealed manifest.

The grammar includes deterministic local model invocation/output fixtures, real Guest WASM
compute, typed local read fixtures, waits, repeated workflow nodes, Harness-declared
independence, freshness partitions and near-match negatives. The eight negative dimensions
are arguments, freshness, resource, privacy, authority, source, artifact and workflow
identity. No prompt, model output, Python source, argument/result body, path or credential is
written to evidence.

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

## Measurement labels

Typed Host read and Guest WASM intervals use actual monotonic timings. Model
invocation/output intervals are deterministic local replays and remain labelled `replayed`.
The real Guest command invokes the verified artifact for both treatments of every task.
Call-count reductions and trace topology are measured facts for this frozen fixture;
wall-clock deltas are retained as observations but are not representative performance
claims. No invented counterfactual duration is used.

## Gates

A valid evidence bundle requires:

- 14 tasks in the exact seeded order and one valid paired report per task;
- identical terminal output and read-only effect ledger for both treatments;
- no unclassifiable divergence;
- all optimization-positive tasks to carry one admitted Host-recorded decision;
- all eight near-match tasks to retain one rejected decision and no physical reduction;
- optimized aggregate physical executions not to exceed baseline;
- `consumer_admitted=false` in every observation report.

Any Guest failure, manifest mutation, unknown evidence field, seal drift, malformed identity,
cross-report metric mismatch or output divergence fails closed.
