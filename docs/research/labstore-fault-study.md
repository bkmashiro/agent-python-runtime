# LabStore filesystem fault study

Status: experimental, mechanism-only evidence. This study does not establish production readiness, general crash consistency, durability on every filesystem, or multi-host correctness.

## Question

The v1 LabStore uses immutable content-addressed object files plus mutable privacy and retention metadata. Before admitting a SQLite metadata backend, this study asks which guarantees the filesystem design already provides and which failure modes remain.

## Deterministic probes

`research/labstore/fault_test.go` uses real child processes and hard exits at three publication boundaries:

1. after the privacy sidecar is durable but before an object stage exists;
2. after the object stage is written and `fsync`ed but before the hard-link publication;
3. after the object hard link and directory sync but before stage cleanup.

A separate child holds a live, `fsync`ed stage while a read-only process performs aggregate traversal. An eight-process race publishes identical content with conflicting private/portable requests. Existing gates also exercise missing privacy metadata, orphan classifications, same-process and cross-handle publication, observation recorder failure, and strict report-to-Lab projection rejection.

## Observed results

- Privacy-sidecar-only crash: reopen succeeds. The object remains absent and non-exportable; retry publishes one private object; aggregate stats succeed.
- Stage-before-link crash: the object remains absent and non-exportable. Retry publishes one private object. The orphan `.stage-*` remains and aggregate traversal returns `ErrCorrupt` rather than silently excluding its bytes.
- Link-before-cleanup crash: the linked object is readable and private. Retry reuses it without privacy downgrade. The orphan stage remains and aggregate traversal returns `ErrCorrupt`.
- Active writer stage: a concurrent read-only aggregate traversal also returns `ErrCorrupt`. The store cannot distinguish a live stage from a crashed orphan.
- Eight independent processes converge on one immutable object identity and a final private classification; portable export fails after convergence. The probe does not claim a linearizable classification transaction during the race.
- Required observation-recorder rejection does not advance accepted evidence; best-effort rejection is visibly incomplete and cannot later claim complete evidence.
- Strict report projection rejects missing rows, non-canonical reports, incompatible measurement identities, and schema-forbidden values. Missing object relations are distinct private/unavailable markers, never fabricated available objects.
- Lab v1 compatibility remains closed: already-declared conditional optional fields can appear or be omitted within their scope; a new nominally optional wire field is rejected by v1 and requires v2.

All probes are bounded and local. They do not use public network access or external write side effects.

## Limitation and recovery boundary

The filesystem CAS preserves immutable-object and fail-private safety across the tested boundaries, but does not provide cross-process snapshot isolation or online orphan recovery. Automatically deleting `.stage-*` during `Open` would be unsafe because the stage may belong to a live writer. Retention-root replacement is atomic at file level but does not provide a compare-and-swap transaction spanning roots, objects, and sweep decisions.

Therefore aggregate operations intentionally fail closed in the presence of a stage. Recovery must remain an explicit offline operation under exclusive ownership until writer coordination and stage ownership are designed and tested.

## SQLite decision

Do **not** introduce a SQLite metadata backend for evaluation v1.

The measured failures are liveness and recovery-coordination gaps, not evidence of immutable-object corruption, privacy downgrade, or identity divergence. SQLite alone would not remove filesystem object stages or reconcile a database commit with object-file publication. Adding it now would create two durability domains plus dependency, migration, toolchain, and binary-size costs before a transaction boundary has been specified.

The evidence supports a smaller next step: filesystem recovery hardening with an explicit cross-process ownership/lock protocol, identifiable stage records, offline orphan inspection/repair, and retention/sweep exclusion. Re-run this matrix after that work. Reconsider SQLite only if measured metadata contention, transactional multi-record requirements, indexed query cost, or recovery complexity remains unacceptable after the filesystem protocol is explicit.
