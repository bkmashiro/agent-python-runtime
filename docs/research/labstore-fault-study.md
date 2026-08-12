# LabStore filesystem fault study

Status: experimental, mechanism-only evidence. This study does not establish production readiness, general crash consistency, durability on every filesystem, or multi-host correctness.

## Question

The v1 LabStore uses immutable content-addressed object files plus mutable privacy and retention metadata. Before admitting a SQLite metadata backend, this study asks which guarantees the filesystem design already provides and which failure modes remain.

## Deterministic probes

`research/labstore/fault_test.go` uses real child processes and hard exits at three publication boundaries:

1. after the privacy sidecar is durable but before an object stage exists;
2. after the object stage is written and `fsync`ed but before the hard-link publication;
3. after the object hard link and directory sync but before stage cleanup.

A separate child holds a live, `fsync`ed stage while a read-only process performs aggregate traversal. An eight-process race now probes exclusive writer ownership: exactly one writer is admitted and the other seven fail with `ErrBusy`; after the owner exits, a new writer reopens the store and can monotonically tighten privacy. Existing gates also exercise missing privacy metadata, orphan classifications, observation recorder failure, and strict report-to-Lab projection rejection.

## Observed results

- Privacy-sidecar-only crash: reopen succeeds. The object remains absent and non-exportable; retry publishes one private object; aggregate stats succeed.
- Stage-before-link crash: the object remains absent and non-exportable. Retry publishes one private object. The orphan `.stage-*` remains and aggregate traversal returns `ErrCorrupt` rather than silently excluding its bytes.
- Link-before-cleanup crash: the linked object is readable and private. Retry reuses it without privacy downgrade. The orphan stage remains and aggregate traversal returns `ErrCorrupt`.
- Active writer stage: a concurrent read-only aggregate traversal also returns `ErrCorrupt`. The store cannot distinguish a live stage from a crashed orphan.
- Eight independent writer contenders admit exactly one owner; seven fail without writing. Process exit releases the OS lock, reopen succeeds, and a subsequent private `Put` monotonically tightens any portable winning publication.
- Offline audit and repair require exclusive lifecycle ownership, so they reject every live reader or writer with `ErrBusy`. Audit reports only bounded counts and logical bytes. Repair validates the complete candidate set before mutation, removes valid orphan stages, syncs affected directories, and leaves published immutable objects untouched. Objectless privacy sidecars remain as durable fail-private classifications; repair never deletes them. A malformed candidate rejects the whole repair without partial deletion.
- Required observation-recorder rejection does not advance accepted evidence; best-effort rejection is visibly incomplete and cannot later claim complete evidence.
- Strict report projection rejects missing rows, non-canonical reports, incompatible measurement identities, and schema-forbidden values. Missing object relations are distinct private/unavailable markers, never fabricated available objects.
- Lab v1 compatibility remains closed: already-declared conditional optional fields can appear or be omitted within their scope; a new nominally optional wire field is rejected by v1 and requires v2.

All probes are bounded and local. They do not use public network access or external write side effects. Runtime lock behavior in this study is exercised on Darwin/Unix; Linux and Windows targets are cross-compiled, but this report does not claim Windows runtime qualification.

## Limitation and recovery boundary

The filesystem CAS preserves immutable-object and fail-private safety across the tested boundaries, but does not provide cross-process snapshot isolation or online orphan recovery. Ordinary read-only handles may coexist; writable handles are single-owner. Automatically deleting `.stage-*` during `Open` remains forbidden because an active writer may own the stage. Retention-root replacement is atomic at file level but does not provide a compare-and-swap transaction spanning roots, objects, and sweep decisions.

Therefore aggregate operations intentionally fail closed in the presence of a stage. Recovery is an explicit offline operation under an OS-released exclusive lifecycle lock; it never guesses liveness from PID files or age and never completes an abandoned publication.

## SQLite decision

Do **not** introduce a SQLite metadata backend for evaluation v1.

The measured failures are liveness and recovery-coordination gaps, not evidence of immutable-object corruption, privacy downgrade, or identity divergence. SQLite alone would not remove filesystem object stages or reconcile a database commit with object-file publication. Adding it now would create two durability domains plus dependency, migration, toolchain, and binary-size costs before a transaction boundary has been specified.

The smaller filesystem step is now implemented: exclusive writer ownership, exclusive offline audit/repair, validated stage recognition, and fail-closed aggregate traversal. Reconsider SQLite only if measured metadata contention, transactional multi-record requirements, indexed query cost, or recovery complexity remains unacceptable under this explicit filesystem protocol.
