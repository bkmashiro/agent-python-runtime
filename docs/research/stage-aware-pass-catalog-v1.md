# Stage-aware optimization pass catalog v1

## Decision

Retain one static, default-off pass catalog for the four runtime mechanisms that have
independent evidence. Do not force them through one `AST -> AST` interface and do not add a
generic IR, dynamic plugin loader, dependency solver or fixed-point manager.

The implementation target is Host commit
`bedc894115adb6354660f9e9beab335160d8f97d`. It adds two analyzer-free stages to the
existing source-bound stages and routes the two direct mechanisms through the same
`passplugin.Registry` used by source plugins.

## Catalog

| pass | stage | analysis | emitted requirement |
|---|---|---:|---|
| `capability_future_projection` | `plan_projection` | 0 analyzers | Future-backed capability projection |
| `semantic_pre_dispatch` | `prefix_overlay` | exact prefix analyzer | staged physical read/observation |
| `prepared_numpy_load` | `hybrid_prepare_patch` | exact prefix and final proof | prepared immutable ndarray plus final claim |
| `prepared_value_binding` | `run_binding` | 0 analyzers | private scalar/byte binding from a ValueSlot |

The catalog distinguishes pass identity from runtime mechanism ownership. Future submission,
drain and logical receipts remain in the bounded capability table. Prepared-object lifetime,
private copy/COW and per-Run claims remain in the prepared-data and ValueSlot owners. A pass
selects a declared requirement; it does not receive a Host pointer, file descriptor, Wazero
mapping or mutable canonical object.

## Lifecycle

```text
sealed capability Plan
  -> plan_projection
  -> capability_future_projection

source prefix stream
  -> prefix_overlay
  -> semantic_pre_dispatch
  -> hybrid_prepare_patch.prepare
  -> prepared_numpy_load candidate

sealed final source
  -> hybrid_prepare_patch.finalize
  -> final occurrence claim or discard

fresh formal Run
  -> run_binding
  -> prepared_value_binding
  -> one private Guest value
```

A streaming prefix can identify and start bounded physical work, but it cannot authorize a
final source rewrite. The final source seal must still confirm the occurrence and suffix. This
is why prepared NumPy loading is a hybrid pass rather than an ordinary whole-program AST pass.

## Registration and outcomes

`plan_projection` and `run_binding` registrations deliberately contain no analyzer identity.
Source-bound stages continue to require one exact analyzer identity. Stage-aware outcome v2
allows Plan and Run records without fabricating source/AST hashes. A Run-binding record binds
a fresh Run identity so two consumers of the same immutable slot do not collide.

Every catalog entry starts disabled. Dispatch through the wrong stage fails with
`ErrUnsupportedStage`; dispatch while disabled fails with `ErrPluginDisabled`.

## Direct mechanism preservation

The Future pass returns the same precomputed `Plan.FuturePythonPrelude()` string as the direct
mechanism. The prepared-value pass returns the same `valueslot.PythonPrelude(slotID)` string.
Exact-Guest gates now obtain both preludes through `passplugin.Registry`:

- two independent live calls still overlap with zero analyzers;
- ignored writes drain exactly once;
- errors do not prevent later Future claims;
- scalar, array and `null` Future results materialize;
- the full sealed call budget is honored;
- one immutable ValueSlot template still serves fresh private consumers;
- the fixed NumPy sum still materializes `12` bytes with zero analyzers.

The positive direct evidence therefore remains mechanism evidence, now reached through pass
dispatch rather than a second code path. A fresh pass-dispatch verification on the target
commit produced:

| pass | baseline median | treatment median | improvement |
|---|---:|---:|---:|
| `capability_future_projection` | `2.565 s` | `2.442 s` | `4.79%` |
| `prepared_value_binding` | `4.798 s` | `2.250 s` | `53.11%` |

Every treatment trial was faster. The Future projection and prepared-value binding were built
outside the `Runner.Run` timer, matching their compile/preparation stage. These observations
are consistent with, but do not replace, the original direct mechanism evidence:

- capability Future median speedup: `5.17%` on two independent `150 ms` calls;
- prepared-value median speedup: `53.06%` on the fixed `8,388,736`-byte NumPy fixture.

These numbers are not relabeled as automatic AST-pass speedups. The two direct passes require
an explicit mechanism selection, and the streaming adapters retain their existing lifecycle
and evidence boundaries.

## Streaming evidence boundary

The semantic pre-dispatch adapter retains the synthetic source-overlap controls reported in
[`source-bound-pass-workload-evidence-v2.md`](source-bound-pass-workload-evidence-v2.md):
roughly `1.91x` speedup when a deterministic `1.5 s` read overlaps about `1.4 s` of source-tail
generation. Its coding-shaped prevalence sample remains negative.

The prepared NumPy adapter retains the mixed prepared-data campaign rather than inventing one
headline. Private COW and data-local treatments beat serial execution at fanout `N=2/4` in the
frozen Linux matrix, while `N=1` needed the `1000 ms` lead coordinate. Exact scope remains in
[`2026-08-21-authority-preserving-prepared-data-autonomous-megagoal.md`](../plans/2026-08-21-authority-preserving-prepared-data-autonomous-megagoal.md).

## Composition boundary

The catalog is not an automatic optimizer scheduler. Initial composition remains explicit:

1. a staged observation and a Future projection must never issue two physical requests for the
   same dynamic capability occurrence;
2. `prepared_numpy_load` and a data-local scalar reduction must not both claim the same source
   region;
3. an early prepared array that loses final admission is discarded without a logical claim;
4. writes, approval-gated calls and captured playback do not enter speculative stages;
5. no fallback may replay a live effect after selected execution begins.

A later occurrence-level selector may make these choices. V1 does not add a cost model or
pass-order solver merely to enable all four passes at once.

## Evidence

Machine-readable implementation and gate evidence is in
[`stage-aware-pass-catalog-v1.json`](../evidence/stage-aware-pass-catalog-v1.json).
