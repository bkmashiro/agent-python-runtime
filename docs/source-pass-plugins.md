# Stage-aware pass plugins

Status: implemented static multi-stage plugin seam

Pysolate passes are ordinary Go values registered in `runtime/passplugin`. The registry is
static for one Host build: it does not load shared libraries, execute untrusted Host code or
run a generic fixed-point pass manager.

## Pass stages

The catalog distinguishes when a transformation owns enough information to act:

| stage | input | output |
|---|---|---|
| `plan_projection` | sealed capability Plan | generated capability projection |
| `prefix_overlay` | admitted source prefix plus Host contracts | staged physical work without a source patch |
| `hybrid_prepare_patch` | prefix candidate, then sealed final source | preparation followed by a final patch/claim |
| `whole_program_patch` | complete source/AST | derived source and runtime requirements |
| `multi_program_patch` | multiple complete programs | bounded cross-program patch |
| `run_binding` | sealed prepared object and fresh Run identity | one private Guest binding |
| `runtime_lowering` | explicit optimization selection | typed `MechanismSet` requirements for an existing Runtime owner |

`runtime/passpipeline` records these stages with
`pysolate.stage-aware-pass-outcome.v2`. Plan and Run stages do not invent source or AST
identities, and Run-binding outcome keys include the fresh Run identity.

## Stage-specific API

Every plugin exposes an immutable `passregistration.Registration`. Whole-program source
plugins additionally implement:

```go
type SourcePatchPlugin interface {
    Plugin
    Transform(context.Context, sourcepatch.Transformer, string) (sourcepatch.Patch, error)
}
```

Analyzer-free Run bindings and the PLM Host-scheduled path use separate narrow dispatch surfaces:

```go
registry.BindRunValue("prepared_value_binding", slotID)
registry.ExecuteCapabilityHostScheduled(
    ctx,
    sourcepatch.PLMCapabilityCallsName,
    runner,
    request,
    plan.PythonPrelude(),
    passplugin.PLMCapabilityProjections(plan),
)
```

The prepared-value binding is analyzer-free. PLM asks the final exact Guest to transform sealed source before that same module executes it; the Host supplies and validates the Plan-derived projection allowlist. Neither surface exposes grants or handlers to Guest code.

## Unified optimization catalog

`passplugin.NewDefaultUnifiedCatalog` exposes 17 default-off entries. It is a lookup
catalog, not one `passpipeline.Pipeline`; any concrete pipeline instance selects at most
the pipeline's bounded 16 entries:

- the retained stage adapters for semantic analysis, prepared regions and Run bindings;
- `prepared_pure_region`, `pure_scalar_cse`, `pure_scalar_fold`,
  `plm_capability_calls` and `data_local_numpy_sum`;
- nine `runtime_lowering` passes for historical research streaming, child fanout, Agent Function retention,
  single-flight, fresh workflow re-evaluation, prepared Runtime instantiation, private-memory COW,
  cold-I/O residency and semantic whole-Run reuse.

`LowerMechanisms` maps selected passes to the existing typed `MechanismSet`; Runtime owners retain
all mutable state and lifecycle logic. `ResolveRuntime` applies Host availability after lowering
and records `pysolate.optimization-pass-selection.v2`. `wazero.Factory.Passes` rejects direct
optimization flags when a catalog is bound, so selection finishes before Runtime initialization
or Guest effects. Retained-prefix Guest execution and independent semantic pre-dispatch additionally require `LegacyResearchExecution`; the default product Factory rejects them.

Two source-mutating execution patches cannot be enabled together because the repository has no
automatic ordering contract. Conflicting capability scheduling owners also fail before execution.
Plan projection, one source patch and Run binding may compose when their lowered mechanism set is
valid.

This is a uniform pass catalog, not a claim that every pass is `AST -> AST`. Authority-bearing
eligibility comes from sealed Plans and contracts. Cache, workflow, prepared-memory and residency
passes are Runtime lowerings rather than source rewrites. Full inventory, exclusions and evidence
are in
[`stage-aware-pass-catalog-v2.md`](research/stage-aware-pass-catalog-v2.md).

## Source-patch plugins

`passregistration.Define` still permits additional source transformations without editing a
central pass-name switch. The registry dispatches explicitly enabled source patches. Its
`Execute` method runs unchanged source when a pass is disabled, fails before execution or
returns not-applicable.

Runnable authority-free source-patch plugins ask an exact analysis Guest to transform the complete source. Generic patches carry original and derived source identities plus the derived source body. Final execution receives the unchanged original `RunRequest`; after normal validation, a fresh Guest re-derives and selects the patch through `runtime_select_source_pass_execution`. PLM is the deliberate exception: its effect-owning typed entry point keeps one Run-private selected AST in the same final Guest and returns only source/pass/registration/projection metadata for Host validation.

The generic source-patch seam intentionally admits no capability Broker or mounted workspace.
A pass that owns external effects, batching, parallel calls or workspace projection uses its
typed stage adapter rather than pretending to be a pure rewrite. There is no runtime fallback
after derived execution begins.

## Existing narrow source transforms

| name | stage | implementation |
|---|---|---|
| `prepared_pure_region` | `whole_program_patch` | adapter to the existing prepared-region owner |
| `pure_scalar_cse` | `whole_program_patch` | runnable source-patch plugin |
| `pure_scalar_fold` | `whole_program_patch` | runnable source-patch plugin |
| `plm_capability_calls` | `whole_program_patch` | Plan-bound Prepare/Linearize/Materialize lowering in the final Guest |
| `data_local_numpy_sum` | `whole_program_patch` | historical analyzer-driven ValueSlot route; superseded by direct binding |

## `plm_capability_calls` v1

The Host-scheduled PLM pass accepts only direct typed capability assignments whose sealed Plan entry has a positive `pysolate.plm-contract.v1`. Its Host-authored descriptor binds capability, Python module/method, ordered argument names and result field. The final exact Guest inserts an internal prepare at the earliest admitted statement-level point and a synchronous linearize/materialize call at the original logical call. Static source span plus runtime occurrence distinguishes loops and repeated sites. Projection receivers and methods must remain unmodified, and code/frame/tracing observers make the pass not applicable. Unsupported wrappers, unsafe argument evaluation, writes and ambiguous source return `not_applicable` and execute unchanged synchronous source.

Source-time preparation requires the final sealed source identity and the same Run-owned candidate table used at the original call. The Host rechecks call/source/arguments, authority, provider/session and temporal evidence before adoption. `CURRENT` prepares transport only; `WALLCLOCK_OBSERVING` is not staged. Ordinary Python never receives a Future or candidate handle, and the Host stores no Python dependency graph. The predecessor `split_phase_capability_calls` issue/collect pass is Removed; its exact correctness and `+151.40%` cold result remain unchanged in [`unified-split-phase-evidence-v1.md`](research/unified-split-phase-evidence-v1.md). Current PLM economics and temporal/fault results are [`plm-v1-production-economics.json`](evidence/plm-v1-production-economics.json) and [`plm-v1-fault-matrix.json`](evidence/plm-v1-fault-matrix.json).

## `pure_scalar_cse` v1

The initial plugin handles one deliberately narrow case:

```python
seed = 7
left = seed * seed + 3
right = seed * seed + 3
```

It replaces the second adjacent scalar RHS with the first result name while preserving source byte length and line layout. V1 accepts only a closed top-level scalar program: every statement is a single-name assignment over known bool/int64 values, followed by `result` as a scalar or bounded literal/list/tuple/dict/compare assembly. The repeated RHS trees must be identical one-line ASCII spans, both static values must match before and after the first assignment, and the expression may contain only bool/int literals, known names and `+`, `-` or `*`. Any import, call, attribute, control flow, unsupported assignment or observable compiled-code path makes the whole pass `not_applicable`.

This pass proves that a new transform can define itself, register without changing the central registry, run in the exact Guest and execute through the common patch selector. It is not intended as a broad Python optimizer.

### Exact-Guest result

The checked synthetic control produced one replacement, the same `[52, 52]` result in
pass-off and pass-on execution, the same original model-source identity and a different
effective AST identity. Inapplicable repeated-call, self-referential reassignment,
unknown-call mutation, compiled-code introspection and integer-identity controls all
executed unchanged source; their results remained `7`, `[2, 3]`, `[4, 16]`, `false`
and `false`.

The matched three-pair runtime fixture was negative: baseline median 2,428,868,958 ns
and treatment median 2,783,839,291 ns, a 14.61% slowdown. The initial CSE is therefore
an extensibility/correctness demonstrator, not a speedup result. The exact artifact,
raw samples and claim boundary are in
[`source-pass-plugin-v1.json`](evidence/source-pass-plugin-v1.json).

## `pure_scalar_fold` v1

The first paper-derived follow-up absorbs only stratum's exact constant-folding kernel.
It accepts the same closed top-level scalar program lane and folds known `bool`/signed-int64 `+`, `-` and `*` expressions into equal literals while preserving source byte length and line layout. A program containing imports, calls, attributes, control flow, unsupported assignments, division, heap values, int64 overflow or compiled-code introspection is wholly `not_applicable`.

For example:

```python
seed = 7
folded = seed * seed + 3
```

becomes a padded source replacement equivalent to `folded = 52`. The pass is independent
of `pure_scalar_cse`; there is still no automatic pass ordering or composition.

This does not import stratum's DataOp graph, cross-pipeline optimizer, predicate
pushdown, vectorization, cache policy or approximate operators. Those mechanisms need
different Host contracts or intentionally change behavior.

### Exact-Guest result

The positive control produced one replacement and returned `52` both pass-off and
pass-on. Mutating-call, compiled-code introspection and integer-identity controls all
returned `not_applicable` and executed unchanged source with results `3`, `false` and
`false`. The existing CSE and adapter regressions also passed on the same artifact.

Two repeated three-pair synthetic runs were negative. The retained run measured baseline
median `2,385,342,083 ns` and treatment median `2,492,427,458 ns`, a `1.0449×`
treatment/baseline ratio or `4.49%` slowdown. This is bounded mechanism evidence, not a
workload speedup. Raw samples and artifact binding are in
[`pure-scalar-fold-paper-pass-v1.json`](evidence/pure-scalar-fold-paper-pass-v1.json).

## Adding a paper pass

1. Define a versioned `passregistration.Definition` and bind it to the current analyzer/config.
2. Implement the stage-specific plugin interface.
3. Register the value in the Host's `passplugin.Registry`.
4. Add synthetic positive and negative fixtures that state the intended applicability conditions.
5. Run the pass against the exact target Guest and compare pass-off/pass-on results and relevant timing.
6. If the pass needs an effect, workspace or multi-program contract not owned by an existing stage, add a typed stage adapter; do not weaken the generic pure source-patch seam.

A pass that fits an existing stage therefore becomes a new implementation plus tests, not a new Runtime execution branch.
