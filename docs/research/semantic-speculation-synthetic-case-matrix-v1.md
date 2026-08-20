# Semantic-speculation synthetic case matrix v1

Status: frozen Phase 3 mechanism evidence input. This matrix is deliberately synthetic and does not estimate natural-workload prevalence or production-wide speedup.

## Identities

- Schema: `pysolate.semantic-speculation-synthetic-case-matrix.v1`
- Matrix identity: `sha256:f69c31c874d56b7563942bf889a798ed16b38a657fef18be90d4251f49fbee3f`
- Canonical file: `docs/evidence/semantic-speculation-synthetic-case-matrix-v1.json`
- File SHA-256: `ac19c0597e4b9dc3e40c847cf37770fb55803f798db94ea4cfdb74a30e70565c`
- Comparator contract: `sha256:16e76c741749bddb61c68ae80902827f73e9dc7efdad6208d858f8edae100ab8`
- Physical delay: 250 ms, inherited from the Phase 0 preregistration

The public matrix contains only source, chunk and input digests plus release offsets and expected outcome classes. Executable fixture bodies remain ordinary checked-in test code, not evidence payloads.

## Case intent

- `external_read_valid_suffix`: positive differentiator. The capability projection deliberately uses the EAGER paper's denied root `time`; syntax/name gating seals it while Host-qualified semantic pre-dispatch may prepare the same read.
- `pure_local`: control where EAGER-style prefix execution may help and sidecar pre-dispatch has no work.
- `unknown_wrapper`: negative control. Neither a static-name comparator nor Pysolate semantic pre-dispatch may infer a new capability authorization through an unknown wrapper.
- `branch_not_taken`, `earlier_exception`, `later_runtime_error`, and `later_syntax_error`: adversarial ordering, reachability and final-source cases.

All treatments receive identical source chunks, inputs, release offsets, capability plan, artifact/profile and physical delay. Treatment adapters cannot inspect case IDs or expected outcomes.

## Trial contract v2

The first timing campaign uses `pysolate.semantic-speculation-trial.v2`. It supersedes the pre-campaign v1 projection by requiring explicit bindings for:

- the frozen synthetic case matrix;
- source schedule;
- canonical inputs;
- artifact manifest;
- import inventory;
- execution profile;
- capability plan.

No v1 timing records were materialized. The historical Phase 1 v1 tests remain evidence for the outcome model; campaign records are v2 so the added identities are not silently retrofitted into the old schema name.

## Claim boundary

Passing this matrix demonstrates bounded mechanism behavior, semantic outcome parity and critical-path economics for these frozen cases. It does not demonstrate case frequency in natural agent programs or justify a general production speedup claim.
