# Source-bound pass pipeline v0

Status: **implemented outcome shell; companion compile-time plugin registry**

`runtime/passpipeline` is the minimum Host-owned shell shared by source-bound
optimization lanes. It does not execute Agent Python, invoke transforms, schedule
work, choose a fallback after external effects, or replace the existing semantic
and prepared-region contracts.

## Current routes

| pass registration | existing consumer | v0 stage |
|---|---|---|
| `semantic_pre_dispatch` | `overlay_only` | `prefix_overlay` |
| `prepared_pure_region` | `execution_patch` | `whole_program_patch` |
| `pure_scalar_cse` | `execution_patch` | `whole_program_patch` |
| `pure_scalar_fold` | `execution_patch` | `whole_program_patch` |

The registration definition owns its stage. `CurrentEntry` projects that stage into
the outcome shell, and `New` rejects a caller-supplied stage that differs from the
registration. `passregistration.Define` and `runtime/passplugin` allow another
compile-time pass to register without editing a central name switch. Existing pass
names cannot be reinterpreted at another stage.

## Stage-specific entry points

The pipeline has four distinct outcome entry points:

- `RecordPrefixOverlay`
- `RecordHybridPreparePatch`
- `RecordWholeProgramPatch`
- `RecordMultiProgramPatch`

There is no transform callback in this outcome shell. `runtime/passplugin` dispatches
stage-specific implementations; `runtime/sourcepatch` supplies the first generic
whole-program transform seam. The shell validates stage,
registration, required binding keys, outcome class, body-free identities and
resource use, then appends an immutable outcome record. Transformation,
preparation, final-source sealing, target-Guest compilation and execution stay
with their existing owners.

## Controls and bounds

Each registered pass has an explicit `Enabled` control. The zero-enabled
pipeline is `AllOff`; attempts to record work return `ErrAllOff` and append no
record. With a mixed configuration, an attempted disabled pass becomes a typed
`rejected/pass_disabled` record. Existing runtime product defaults remain off.

The maximum v0 limits are:

| limit | maximum |
|---|---:|
| registered passes | 16 |
| positive derived-source growth | 1 MiB |
| positive derived-AST growth | 8192 nodes |
| physical preparation | 8 MiB |
| reanalysis | 16 |

A caller may lower a limit but cannot widen it. Overflow is rejected before a
record can represent an applied pass. These bounds do not grant authority and do
not bypass the stricter limits of source validation, Guest analysis, prepared
memory or capability admission.

## Outcome evidence

`pysolate.source-bound-pass-outcome.v1` contains only bounded metadata:

- pass registration identity and stage;
- `applied`, `discarded`, `prepared_awaiting_final` or `rejected`;
- rejection reason and deterministic outcome order;
- original/derived source and AST digests;
- the exact registration-required binding map;
- logical/physical event counts, result digest or exception class/order;
- workspace disposition;
- source/AST growth, preparation bytes and reanalysis counts.

The pipeline defensively copies binding maps and records. It stores no source,
result, workspace or prepared-data body. Overlay-only records cannot name derived
source or AST identities. Rejected records cannot claim derived identities, results,
logical/physical work or preparation, and discarded or awaiting-final work cannot
claim a logical event. A current pass can record at most one terminal outcome per
bound occurrence or region; duplicate projection is rejected before append.

## Preserved boundaries

- A prefix outcome is not formal Agent Python execution.
- A patch outcome does not waive final-source seal, exact target-Guest derivation
  or compile-before-execute.
- A typed rejection does not authorize fallback after a possibly started
  authority-bearing effect.
- `discarded` applies only to private/speculative work whose owning contract
  permits discard; it is not external rollback.
- The pipeline does not change Broker, Plan, receipt, workspace or Prepared
  Family ownership.
- Ordering, conflict resolution, fixed-point iteration, plugins, a new IR and
  cost-based selection remain deferred until retained passes expose a real
  composition seam.
