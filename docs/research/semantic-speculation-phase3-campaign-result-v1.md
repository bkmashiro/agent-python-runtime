# Semantic-speculation Phase 3 matched campaign result v1

**Status:** complete frozen synthetic campaign; semantic equivalence passed; Gate P3 economics condition not met

## Scope

This result covers the preregistered seven-case synthetic matrix, five trials per case and three achieved treatments:

- `serial_whole_file`;
- research-private `eager_style_gate`;
- production-semantics-preserving `semantic_pre_dispatch`.

All 35 coordinates used the seeded order derived from `shuffle_seed=20260820`. Each coordinate used the same source, canonical inputs, source-release schedule and operation latency in all three treatments. The result supports mechanism and semantic-equivalence claims only. It does not estimate natural-workload prevalence or production-general speedup.

## Sealed evidence

| Binding | Value |
|---|---|
| Campaign source commit | `79f54d5fd7b01f368184fc6b7c78a971b7797b3d` |
| Artifact SHA-256 | `12dbb89ec0d9ae1510c990539ab9316c0f4ab979f8d15d4320973ff4f3fcfb54` |
| Campaign manifest identity | `sha256:a0397a28664675e5f450745a7315ea8f088f6a64cff864b69c2d66a8eeba1d33` |
| Campaign manifest SHA-256 | `2da100224c976bae886ce07d1624ef7b92c51dd9bc9aa3fd646f3f54645a3bac` |
| Independent review identity | `sha256:7472a33d8c6ba268f198d9f3505a9fa583229fc1146f6c342392d2db2289a878` |
| Independent review SHA-256 | `5ceedc065181100efc69669ae1eb281f26f8f076438141d44b8999ec5627abdc` |

The independent review used a separate Python implementation. It verified canonical JSON, private file modes, all file hashes and identities, the complete seeded coordinate and treatment orders, shared bindings, matched terminal semantics and all 35 recomputed aggregates.

## Observed medians

Times are seconds. `serial − semantic` is a signed achieved delta; a negative value means semantic pre-dispatch was slower.

| Frozen case | Serial | EAGER-style | Semantic pre-dispatch | Analysis-only oracle | Serial − semantic | Ready before finalization |
|---|---:|---:|---:|---:|---:|---:|
| `branch_not_taken` | 2.383 | 2.224 | 6.781 | 2.383 | -4.391 | 0/5 |
| `earlier_exception` | 2.544 | 2.232 | 9.016 | 2.544 | -6.462 | 0/5 |
| `external_read_valid_suffix` | 2.818 | 2.486 | 9.046 | 2.568 | -6.234 | 0/5 |
| `later_runtime_error` | 2.863 | 2.484 | 9.068 | 2.613 | -6.222 | 0/5 |
| `later_syntax_error` | 2.383 | 2.250 | 8.184 | 2.383 | -5.772 | 0/5 |
| `pure_local` | 2.421 | 2.227 | 6.820 | 2.421 | -4.396 | 0/5 |
| `unknown_wrapper` | 2.777 | 2.465 | 9.087 | 2.527 | -6.319 | 0/5 |

Campaign totals:

- 35 complete matched coordinates and 105 achieved trials;
- 35/35 matched final result or exception, logical calls and authority disposition;
- 0/35 positive achieved `semantic_pre_dispatch` versus serial coordinates;
- 0/35 semantic prepared results ready before source finalization;
- zero orphaned physical attempts;
- 380 semantic physical result bytes and 20 semantic provider cost units;
- perfect-effect estimates remained analysis-only and were never counted as achieved speedup.

## Decision boundary and approved continuation

The campaign validates the comparator, seeded matched runner, exact-Guest terminal semantics, authority/workspace accounting, private evidence codec and complete-grid sealing. It satisfies the roadmap's renamed P3-S semantics/evidence gate. It does not satisfy P3-E's required positive net overlap.

The observed failure is consequential: under this frozen 300 ms source schedule and 250 ms external-operation latency, target-Guest semantic analysis did not produce a prepared result before finalization, and its achieved lane paid substantially more startup/analysis cost than serial execution. Changing these frozen cases after observing the result would invalidate their preregistration, so the matrix and this negative result remain immutable.

Subsequent source inspection localized the cost. `GenerateVerifiedSourceWithPreDispatch` scheduled exact analysis for every cumulative prefix. `AnalyzeSemantic` directly instantiated a fresh module, called `_initialize` and `runtime_init`, analyzed one prefix and destroyed the module; it bypassed the ordinary Run path's `PreparedRuntime`, and the macOS campaign did not use Linux memory COW. Two-chunk rows therefore paid two analyzer Guests plus one formal Guest; three-chunk rows paid three plus one. This is the measured cold-analyzer implementation boundary, not evidence for a hot prepared/COW treatment.

On 2026-08-20 the owner approved the remediation path. Phase 4 may proceed under these rules:

- retain this campaign as the permanent cold baseline;
- do not mutate or relabel the original matrix;
- use conservative Host screening only to skip unnecessary analysis, never to mint semantic authority;
- reduce exact target-Guest analyzer invocations to candidate transitions;
- acquire one fresh or prepared/COW analyzer instance per source-generation Run and use it as a bounded REPL-like RPC session for multiple prefix analyses; never reuse that mutable session across Runs;
- use only single-use prepared modules or private Linux COW clones across Runs, never reset a served interpreter into a pool;
- keep formal execution fresh and original source/AST unchanged;
- freeze a separate versioned extension protocol before measuring remediation economics;
- require a new P4-E gate to account for provisioning, analyzer, Broker, memory, cancellation/orphan and discarded-capacity costs.

This decision permits investigation and implementation. It does not retroactively turn P3-E green or authorize a production-general speedup claim.
