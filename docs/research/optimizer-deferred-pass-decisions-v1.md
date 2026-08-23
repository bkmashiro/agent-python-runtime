# Optimizer deferred-pass decisions v1

Status: **live architecture-gate record**

This note records passes deferred under the optimizer megagoal's architecture
rule. A deferred pass is not implemented, admitted or counted in the retained
pipeline. Deferral leaves the original source path and existing runtime
semantics unchanged.

## Phase 1: `pure_scalar_cse`

Decision: **Implemented after the plugin-seam follow-up.**

The original deferral was correct for the then-current prepared-region-only patch
ABI. A later, explicitly approved refactor added a small compile-time plugin registry
and a generic authority-free whole-program source-patch selector. The first
`pure_scalar_cse` implementation is deliberately narrow: adjacent single-name
assignments with identical `+`, `-` or `*` bool/int expressions over known scalar
names and literals.

The exact Guest emits the patch, and a fresh final Guest receives the unchanged
original `RunRequest`, validates it, re-derives the patch and selects the derived
program before execution. Calls, attributes, subscripts, control flow and
non-adjacent reuse remain unchanged. See [`source-pass-plugins.md`](../source-pass-plugins.md).

This supersedes only the Phase 1 deferral. It does not supply the Broker, batching,
parallel-call, workspace-projection or multi-program contracts required by later
passes.

## Phase 2: `frozen_observation_cse`

Decision: **Deferred before implementation.**

The Broker preserves one operation, budget charge, handler outcome and receipt per
dynamic call. Its sealed pre-dispatch contract explicitly fixes coalescing to
`forbidden`; ordinary `workspace.read_text` is `live_only` and carries no immutable
workspace-root or freshness identity. Whole-Run `semanticreuse` retains complete
Agent Function results and cannot stand in for per-occurrence capability reuse.

Implementing this pass would require a new Plan/spec contract, a Broker-owned
coalescer, detached result materialization, per-occurrence logical receipts and a
workspace/freshness identity. Those are changes to the capability lifecycle rather
than a narrow source pass. Existing single-flight code also collapses only concurrent
whole invocations and deliberately retains no completed value, so it is not the
required substrate.

Reconsider only after a separately reviewed Broker contract can prove one physical
read while preserving each original logical call, failure position, budget, receipt
and result detachment.

## Phase 3: `repository_projection_pushdown`

Decision: **Deferred before implementation.**

The current workspace surface exposes `workspace.read_text` but no line/range
projection with a versioned encoding, newline, slice and error law. Adding a narrow
`read_lines` capability would be possible, but rewriting the original call still
requires the deferred generic source-patch selector from Phase 1. Introducing the
capability without an admitted rewrite would only enlarge the product surface.

Reconsider when both an adapter-authored projection law and the reviewed source-patch
selection seam exist. No query DSL or partial capability is added now.

## Phase 4: `ordered_read_batch_fusion`

Decision: **Deferred before implementation.**

The Broker currently maps one Guest call to one operation index and one receipt. A
single batch capability would therefore appear as one logical call, not the ordered
set of original calls required by the pass contract. Expanding one physical batch
into per-item logical receipts, first-visible failure and cancellation state requires
a new Broker lifecycle and batch adapter contract. The source rewrite also depends on
the deferred patch selector.

Reconsider only after the Broker has a reviewed logical-item/physical-dispatch model;
do not emulate fusion with an ordinary batch tool and claim parity.

## Phase 5: `independent_read_parallelization`

Decision: **Deferred before implementation.**

The Guest exposes synchronous capability calls, and the Broker assigns operation order
as calls arrive. There is no bounded Host helper that can start several calls while
later presenting exceptions in source order, cancelling siblings and recording
late/orphaned physical work. Adding that helper plus an AST lowering and budget model
would create a new concurrent execution subsystem. It also depends on the deferred
source-patch selector.

Reconsider only after source-order failure selection and physical orphan accounting
have a narrow Host owner. The existing general workflow/subagent concurrency is not
reused inside one Agent Python program.

## Phase 6 and 6S: prepared literal/array hoisting

Decision: **Deferred before implementation.**

Prepared NumPy ingress can install one bounded private array as a Guest global, but
ordinary source that constructs the same array would overwrite that global. Turning
construction into one-shot materialization therefore still needs a final-source-bound
execution patch. The available patch selector is prepared-scalar-region-specific and
cannot express array assignment removal or materialization. Streaming promotion would
inherit the same missing final patch and add speculative preparation lifecycle.

The existing Prepared Family/data plane remains unchanged. Reconsider complete-source
hoisting after the source-patch selector is reviewed; reconsider streaming promotion
only after that complete-source pass exists and passes lifecycle controls.

## Cohort common prefix and Phase 7 composition

Decision: **Deferred before implementation.**

Factoring existing sibling programs needs the unavailable multi-program source patch.
Using authored residual programs with prepared globals would demonstrate the existing
Prepared Family API, not an optimizer pass. Sharing mutable Python state, Plan/Broker
objects or workspace publication remains prohibited.

The retained registry now has the existing `semantic_pre_dispatch` overlay,
`prepared_pure_region` patch and the narrow `pure_scalar_cse` source-patch plugin.
This proves compile-time registration and one-pass dispatch, but not automatic
composition: no current fixture needs overlapping transforms, reanalysis or conflict
resolution. `PassPipeline` keeps deterministic outcome order, per-pass disable and
all-off behavior; a larger ordering or fixed-point manager remains unnecessary.
