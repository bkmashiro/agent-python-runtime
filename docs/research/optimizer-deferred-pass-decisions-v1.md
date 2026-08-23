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
