# Optimizer deferred-pass decisions v1

Status: **live architecture-gate record**

This note records passes deferred under the optimizer megagoal's architecture
rule. A deferred pass is not implemented, admitted or counted in the retained
pipeline. Deferral leaves the original source path and existing runtime
semantics unchanged.

## Phase 1: `pure_scalar_cse`

Decision: **Deferred before implementation.**

The semantic analyzer can identify a deliberately narrow total scalar subset,
but the existing executable patch seam is not generic. The current formal patch
path is specifically owned by `PreparedRegionDecision`, its capsule/table
lifecycle and `Engine.RunPreparedRegionDerived`. The fresh Guest accepts that
selection through the prepared-region-specific
`runtime_select_prepared_region_execution` ABI after validating the unchanged
original `RunRequest` source.

A third AST execution patch would therefore require one of two changes:

1. add another pass-specific branch and Guest selector to the central fresh-Run
   path; or
2. replace the prepared-region selector with a generic source-patch union and
   migrate the already-closed prepared-region lifecycle onto it.

The first contradicts the minimum pipeline's purpose and does not establish a
reusable seam. The second changes a security-sensitive, artifact-visible ABI
and a working ownership model before another retained pass proves that the
abstraction is correct. Sending derived source as ordinary `RunRequest.code`
is not an acceptable shortcut because it loses the exact generated-source
binding and bypasses the existing original-source/derived-AST selection gate.

No scalar rewrite, pass registration or speedup claim is retained. Reconsider
only after a separately reviewed source-patch selection design preserves:

- original complete-source seal and target-Guest validation;
- one formal execution;
- closed pass kind and registration identity;
- compile-before-execute behavior;
- unchanged authority, workspace and no-post-effect-replay semantics; and
- byte/trace compatibility for the prepared-region path.

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

The retained pipeline has only the existing `semantic_pre_dispatch` overlay and
`prepared_pure_region` patch. The Phase 7 gate requires at least three retained passes
to expose a real composition seam. `PassPipeline` already freezes current stage
routing, deterministic outcome order, per-pass disable and all-off behavior; no larger
ordering, reanalysis or conflict manager is added for two passes.
