# Source-bound pass pipeline v0

Status: **implemented infrastructure; no transform semantics**

`runtime/passpipeline` is the minimum Host-owned shell shared by source-bound
optimization lanes. It does not execute Agent Python, invoke transforms, schedule
work, choose a fallback after external effects, or replace the existing semantic
and prepared-region contracts.

## Closed current routes

| pass registration | existing consumer | v0 stage |
|---|---|---|
| `semantic_pre_dispatch` | `overlay_only` | `prefix_overlay` |
| `prepared_pure_region` | `execution_patch` | `whole_program_patch` |

The registration object and its identity are retained unchanged. `CurrentEntry`
looks up this closed route. `New` rejects moving either registration to another
stage, even when that stage has a compatible consumer kind. A future hybrid or
multi-program pass therefore needs its own closed registration and route rather
than reinterpreting an existing pass.

## Stage-specific entry points

The pipeline has four distinct outcome entry points:

- `RecordPrefixOverlay`
- `RecordHybridPreparePatch`
- `RecordWholeProgramPatch`
- `RecordMultiProgramPatch`

There is no exported universal transform callback. The shell validates stage,
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
- source/AST growth, preparation bytes and reanalysis counts.

The pipeline defensively copies binding maps and records. It stores no source,
result, workspace or prepared-data body.

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
