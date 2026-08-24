# Stage-aware optimization pass catalog v2

## Decision

All retained Runtime optimization selectors now enter through one static, default-off pass
catalog. This does not turn cache, scheduling or memory policy into `AST -> AST` compiler
passes. It gives each optimization an explicit stage, immutable registration and typed lowering
to the existing Runtime owner.

The code target is Host commit
`3a502fac306b04e342c55bd8585fef008310286c`, tree
`30f8a911ff8820080eca8d6cbe08fa9a49e4c71c`.

V2 extends the four-pass catalog from v1. It registers the runnable source transforms, the
prepared-region adapter and all optional execution-cost mechanisms represented in
`runtime.MechanismSet`. Safety, identity and authority mechanisms remain substrates rather than
being relabeled as optimizations.

## Catalog

### Source, Plan and Run passes

| pass | stage | lowering owner | status |
|---|---|---|---|
| `capability_future_projection` | `plan_projection` | sealed Plan and bounded Future table | Experimental/retained |
| `semantic_pre_dispatch` | `prefix_overlay` | staged-observation and capability owners | Experimental/narrow positive |
| `prepared_numpy_load` | `hybrid_prepare_patch` | prepared-data producer and private materializer | Experimental/mixed |
| `prepared_pure_region` | `whole_program_patch` | prepared-region table | Experimental |
| `pure_scalar_cse` | `whole_program_patch` | exact Guest source patch | Experimental/no-go on cost |
| `pure_scalar_fold` | `whole_program_patch` | exact Guest source patch | Experimental/no-go on cost |
| `split_phase_sources_read` | `whole_program_patch` | capability Future table | Historical/no-go on cost |
| `data_local_numpy_sum` | `whole_program_patch` | immutable ValueSlot table | Historical/no-go on cost |
| `prepared_value_binding` | `run_binding` | immutable template plus per-Run `Fresh()` table | Experimental/retained |

### Runtime-lowering passes

| pass | stage | emitted mechanisms | Runtime owner |
|---|---|---|---|
| `source_streaming_execution` | `runtime_lowering` | streaming, private workspace | Wazero stream runner |
| `streamed_child_fanout` | `runtime_lowering` | streaming, private workspace, immutable branches, child fanout | subagent/workspace owners |
| `agent_function_retention` | `runtime_lowering` | immutable branches, function cache | bounded Agent Function store |
| `agent_function_singleflight` | `runtime_lowering` | single-flight | Agent Function flight group |
| `fresh_workflow_reevaluation` | `runtime_lowering` | immutable branches, function cache, fresh re-evaluation | workflow evaluator |
| `prepared_runtime_instantiation` | `runtime_lowering` | prepared Runtime | Wazero prepared slot |
| `private_memory_cow` | `runtime_lowering` | prepared Runtime, memory COW | Linux private mapping owner |
| `cold_io_residency` | `runtime_lowering` | prepared Runtime, memory COW, cold-I/O continuation | Wazero residency owner |
| `semantic_whole_run_reuse` | `runtime_lowering` | semantic analysis, single-flight, semantic reuse | semantic reuse plus Agent Function owner |

There are 18 entries. Every one starts disabled.

## Selection and lowering

`passplugin.NewDefaultUnifiedCatalog` constructs the static catalog. Selection is immutable:
`Enable` returns a copy and preserves previously selected entries. `LowerMechanisms` produces a
stable pass-name order and the existing typed `MechanismSet`. The Runtime owners still execute
the behavior; the catalog does not duplicate cache state, Future lifecycle, workflow state,
prepared modules, mappings or ValueSlot claims.

```go
passes, _ := passplugin.NewDefaultEnabledCatalog(
    passregistration.AgentFunctionRetention,
    passregistration.AgentFunctionSingleFlight,
)
selection, _ := passes.LowerMechanisms(runtime.MechanismSet{})
```

`ResolveRuntime` then applies Host availability to the lowered mechanisms and records
`pysolate.optimization-pass-selection.v2`, including each selected pass version, stage and
registration identity. A requested Linux COW pass can therefore fall back on another Host without
rewriting the selected pass identity or pretending COW ran.

`wazero.Factory.Passes` is the canonical formal-Guest entry point. When a catalog is bound, the
factory rejects direct optimization booleans with `ErrDirectOptimizationSelection`; this keeps
selection before module initialization or Guest effects. Low-level tests may still construct a
bare `MechanismSet` to test a substrate in isolation, but that is not the compositional Runtime
path.

The semantic pre-dispatch and EAGER streaming treatments now create or accept explicit catalogs.
Prepared analyzer provisioning, source-pass execution, direct Future projection, prepared-value
binding, semantic reuse, the composable north-star path, prepared modules, memory COW and
cold-I/O gates all lower from selected passes before reaching their established owners.

## Composition rules

V2 remains deliberately smaller than a pass manager.

1. Enabling more than one source-mutating `ExecutionPatch` fails with `ErrPassConflict`. There is
   no hidden CSE/fold/NumPy ordering.
2. Semantic pre-dispatch and split-phase/Future scheduling cannot both own the same capability
   lifecycle; their lowered `MechanismSet` is invalid and selection fails before execution.
3. Prefix observation may compose with one final source mutation because the stages differ.
4. Plan projection, one source mutation and Run binding may compose when their Runtime
   requirements validate.
5. Host availability fallback occurs after pass lowering and before Runtime initialization.
6. An applied source patch still has no post-effect retry path. Prefix preparation that loses
   final admission is discarded without a logical claim.
7. Approval-gated calls, playback and external-write reconciliation remain synchronous authority
   paths, not optimization passes.

No dependency solver, fixed-point loop, dynamic plugin loader, generic IR or automatic cost model
was added.

## What is not an optimization pass

The following remain explicit substrates or authority policy:

- semantic analysis and staged observation;
- private workspaces and immutable branch identity;
- capability grants, approval, playback and external-write lifecycle;
- artifact/profile verification and placement admission;
- runtime observations, receipts and Lab projection;
- the developer build cache.

Optimization passes may require these mechanisms, but naming them as passes would not remove
work or select a faster execution strategy.

## Verification and economics

The migration preserves the existing economics decisions. It does not relabel mechanism evidence
or historical no-go results as new speedups.

A fresh exact-Guest verification on the code target observed:

| pass or lane | baseline median | treatment median | change | decision |
|---|---:|---:|---:|---|
| Future projection, one paired verification | `2.606 s` | `2.456 s` | `-5.75%` | retained Experimental |
| prepared-value, five pairs | `4.856 s` | `2.274 s` | `-53.18%` | retained Experimental |
| scalar CSE, three pairs | `2.489 s` | `2.890 s` | `+16.10%` | no-go |
| scalar fold, three pairs | `2.290 s` | `2.386 s` | `+4.20%` | no-go |
| analyzer-driven split-phase read, one paired verification | `2.574 s` | `7.601 s` | `+195.31%` | no-go |
| analyzer-driven data-local NumPy, five pairs | `4.889 s` | `5.207 s` | `+6.51%` | no-go |

The one-pair rows are correctness/performance smoke checks, not replacement campaigns. The
original multi-sample Future, prepared-value, semantic pre-dispatch and prepared-data evidence
remains authoritative for their bounded claims.

The prepared-region exact-Guest gate also covers configuration reuse across baseline,
derived and source-drift controls. Pass lowering occurs once before those copies are made;
the copied configurations are not lowered a second time.

The same target passed:

- the complete Go suite;
- race tests for the Runtime, catalog, Wazero and migrated owners;
- `go vet ./...`;
- 189 Guest bootstrap tests;
- exact-Guest source, Future, semantic pre-dispatch, prepared-value, prepared Runtime, semantic
  reuse and full-composition gates.

Machine-readable evidence is in
[`stage-aware-pass-catalog-v2.json`](../evidence/stage-aware-pass-catalog-v2.json).
