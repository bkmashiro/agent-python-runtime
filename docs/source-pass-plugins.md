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

The two adapters preserve their current lifecycle and execution paths. The registry does not duplicate or replace those implementations.

## Generic source-patch seam

`pure_scalar_cse` asks an authority-free exact Guest to transform the complete source. The Guest returns a patch containing the original and derived source/AST identities and the derived source body. Final execution receives the unchanged original `RunRequest`; after normal source validation, a fresh Guest re-derives and selects the patch through `runtime_select_source_pass_execution`.

The first generic seam intentionally admits no capability Broker or mounted workspace. A paper pass that owns external effects, batching, parallel calls or workspace projection must provide a stage adapter with the corresponding Host contract rather than pretending to be a pure source rewrite.

There is no runtime fallback after a derived execution begins. A transform that is not applicable returns unchanged/not-applicable before final execution.

## `pure_scalar_cse` v1

The initial plugin handles one deliberately narrow case:

```python
seed = 7
left = seed * seed + 3
right = seed * seed + 3
```

It replaces the second adjacent scalar RHS with the first result name while preserving source byte length and line layout. Both assignments must be single-name assignments, the RHS trees must be identical one-line ASCII spans, both static values must match before and after the first assignment, and the expression may contain only known bool/int64 names, bool/int literals and `+`, `-` or `*`. Calls, attributes, subscripts, control flow and non-adjacent reuse are not transformed.

This pass proves that a new transform can define itself, register without changing the central registry, run in the exact Guest and execute through the common patch selector. It is not intended as a broad Python optimizer.

### Exact-Guest result

The checked synthetic control produced one replacement, the same `[52, 52]` result in
pass-off and pass-on execution, the same original model-source identity and a different
effective AST identity. Inapplicable repeated-call and self-referential
reassignment controls executed the unchanged source and returned `7` and `[2, 3]`.

The matched three-pair runtime fixture was negative: baseline median 2,444,994,167 ns
and treatment median 2,819,939,459 ns, a 15.34% slowdown. The initial CSE is therefore
an extensibility/correctness demonstrator, not a speedup result. The exact artifact,
raw samples and claim boundary are in
[`source-pass-plugin-v1.json`](evidence/source-pass-plugin-v1.json).

## Adding a paper pass

1. Define a versioned `passregistration.Definition` and bind it to the current analyzer/config.
2. Implement the stage-specific plugin interface.
3. Register the value in the Host's `passplugin.Registry`.
4. Add synthetic positive and negative fixtures that state the intended applicability conditions.
5. Run the pass against the exact target Guest and compare pass-off/pass-on results and relevant timing.
6. If the pass needs an effect, workspace or multi-program contract not owned by an existing stage, add a typed stage adapter; do not weaken the generic pure source-patch seam.

A pass that fits an existing stage therefore becomes a new implementation plus tests, not a new Runtime execution branch.
