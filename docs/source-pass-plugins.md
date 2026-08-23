# Source pass plugins

Status: implemented compile-time plugin seam

Pysolate source passes are ordinary Go values registered in `runtime/passplugin`. The registry is static for one Host build: it does not load shared libraries or execute untrusted Host code.

## Minimal API

A plugin exposes an immutable `passregistration.Registration`. A source-patch plugin additionally implements:

```go
type SourcePatchPlugin interface {
    Plugin
    Transform(context.Context, sourcepatch.Transformer, string) (sourcepatch.Patch, error)
}
```

`passregistration.Define` creates a new name, version, stage, consumer and binding set without editing a central pass-name switch. `passplugin.Registry` stores plugins by name, starts all of them disabled and dispatches explicitly enabled source-patch transforms. Its `Execute` method runs the unchanged original request when a pass is disabled, fails before execution or returns not-applicable. Body-free terminal outcomes remain in `runtime/passpipeline`.

This is deliberately compile-time extensibility. It has no dynamic loader, generic IR, dependency solver, cost model, automatic reordering or fixed-point loop.

## Current plugins

| name | stage | implementation |
|---|---|---|
| `semantic_pre_dispatch` | `prefix_overlay` | adapter to the existing semantic pre-dispatch owner |
| `prepared_pure_region` | `whole_program_patch` | adapter to the existing prepared-region owner |
| `pure_scalar_cse` | `whole_program_patch` | runnable source-patch plugin |
| `pure_scalar_fold` | `whole_program_patch` | runnable source-patch plugin; narrow constant-fold kernel absorbed from stratum |

The two adapters preserve their current lifecycle and execution paths. The registry does not duplicate or replace those implementations.

## Generic source-patch seam

Runnable source-patch plugins ask an authority-free exact Guest to transform the complete source. The Guest returns a patch containing the original and derived source/AST identities and the derived source body. Final execution receives the unchanged original `RunRequest`; after normal source validation, a fresh Guest re-derives and selects the patch through `runtime_select_source_pass_execution`.

The first generic seam intentionally admits no capability Broker or mounted workspace. A paper pass that owns external effects, batching, parallel calls or workspace projection must provide a stage adapter with the corresponding Host contract rather than pretending to be a pure source rewrite.

There is no runtime fallback after a derived execution begins. A transform that is not applicable returns unchanged/not-applicable before final execution.

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
