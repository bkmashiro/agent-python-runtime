# Prepared family v1 contract

**Status:** Phase 0 implementation contract. This document governs the post-paper prepared-family lane and does not change the frozen prepared-data paper contracts.

## Scope

A `numpy-core` Host may seal one bounded ndarray and create fresh single-use runners from it. Consumers may use different source, Run identity, capability plan and private workspace. Runtime executes one runner at a time and owns no scheduling, retry, selection or publication policy.

The v1 implementation owner is `runtime/engine/wazero`. It reuses `runtime/numpycodec`, `runtime/workspace`, `runtime/subagent` and the ordinary response contract. It does not add a Cohort field to `RunRequest` or `MechanismSet`.

## Invariants

- The Host copies and validates input before admission. Guest/model data cannot mint a family.
- Shared physical pages contain Guest-pure state only. No Broker, grant map, workspace lease, InvocationRef, FD, Host pointer or mutable Host view is captured.
- Each consumer gets a fresh module, Broker namespace, cancellation/deadline, RunConfig snapshot and workspace binding.
- A family never shares mutable Python state. Linux COW mappings are private; the portable lane copies into each private Guest.
- All limits are explicit: body at most `numpycodec.MaxBodyBytes`, rank at most `numpycodec.MaxRank`, and family consumer/active counts are non-zero bounded integers.
- The body never enters `RunRequest`, Broker JSON, evidence, logs, trusted Python source, Guest-visible handles or a filesystem path.
- Ordinary mechanism-off execution remains unchanged.

## Binary preparation ABI

Add one optional Guest export without changing existing exports:

```c
int32_t runtime_prepare_numpy_ndarray(
    const char *descriptor_json,
    int32_t descriptor_len,
    const uint8_t *body,
    int32_t body_len
);
```

Host sequence:

```text
instantiate exact artifact
→ _initialize
→ runtime_init({})
→ alloc descriptor and body separately
→ write canonical descriptor and raw body into Guest memory
→ call runtime_prepare_numpy_ndarray
→ dealloc both staging buffers on every outcome
→ seal image or execute one private-copy consumer
```

The descriptor is bounded canonical JSON with exactly:

```text
schema_version = pysolate.prepared-numpy-input.v1
name           = validated Python identifier
codec          = numpy_ndarray_c_v1
dtype          = qualified v1 dtype
shape          = bounded non-empty dimensions
order          = C
endianness      = little | not_applicable
nbytes          = exact body length
body_sha256     = exact digest
input_sha256    = numpycodec descriptor identity
```

The C entrypoint enforces pointer/length bounds, creates Python-owned descriptor and byte values, invokes `_prepare_numpy_ndarray`, and drops temporary references. Python rejects unknown/missing fields, invalid names, unsupported dtype/layout, length/digest mismatch and repeated preparation. It constructs a private mutable `bytearray`, then a C-contiguous ndarray under `name` in `_prepared_globals`. Host staging pointers are not retained.

`runtime_prepare` remains the research/general trusted-source ABI. Prepared-family runners reject non-empty `trustedPrepare`; promoted family data never uses it.

Adding the export requires updating the C header/source, linker export list, artifact verifier, source-contract tests, manifest expectations and real `numpy-core` artifact qualification. An old artifact fails closed because the export is absent.

## Host input and family API

The intended minimal Go surface in `runtime/engine/wazero` is:

```go
type PreparedNumpyInput // immutable; constructor copies body
func NewPreparedNumpyInput(name string, descriptor numpycodec.Descriptor, body []byte) (PreparedNumpyInput, error)

type PreparedFamilyConfig struct {
    ImageConfig runtime.RunConfig
    MaxConsumers uint32
    MaxActive uint32
    Mode PreparedFamilyMode // auto, private_copy, private_cow
}

type PreparedRunnerConfig struct {
    RunConfig runtime.RunConfig
    BrokerFactory BrokerFactory
    WorkspaceManager *workspace.Manager
    WorkspaceRef workspace.Ref
    WorkspaceOwner string
    InvocationRef runtime.InvocationRef
}

func PrepareNumpyFamily(context.Context, []byte, PreparedFamilyConfig, PreparedNumpyInput) (*PreparedFamily, error)
func (*PreparedFamily) NewRunner(context.Context, PreparedRunnerConfig) (engine.Runner, error)
func (*PreparedFamily) State() PreparedFamilyState
func (*PreparedFamily) Close(context.Context) error
```

The exact exported names may change during implementation, but not these ownership rules:

- `PreparedNumpyInput` stores a copied body and copied shape; callers cannot mutate admitted bytes.
- The canonical preparation engine has no Broker, grants or workspace.
- `NewRunner` validates compatibility before creating the runner and deep-copies mutable RunConfig members, especially `CapabilityGrants`.
- Each call receives a fresh Broker factory result and private workspace binding through the existing Engine path.
- The wrapper applies its Host-owned `InvocationRef` to the Run context, requires request `run_id == InvocationRef.ExecutionID`, and rejects caller-supplied trusted preparation.
- The returned wrapper is single-use and does not expose the raw prepared Engine.
- Family identity and member records are Host values; neither is Guest input.

## Compatibility identity

`pysolate.prepared-family-image.v1` hashes canonical fields:

- artifact and manifest SHA-256;
- execution profile ID and allowed, available and qualified import roots;
- deterministic profile identity or `none`;
- memory limit pages;
- Guest preparation ABI version;
- ndarray name, codec, dtype, shape, order, endianness, bytes and body/input digest.

Consumer timeout, request/response byte limits, capability grants, Broker factory, workspace and InvocationRef are deliberately not image state. They remain per consumer. Attachment rejects artifact/profile/import/deterministic/memory drift before Guest execution.

Do not broaden the existing `ExecutionProfileBindingSHA256` contract only to serve this feature; use a prepared-image identity local to the family unless an independently useful profile fix is required.

## Lifecycle

```text
family:   open → closing → closed
consumer: new → running → terminal → closed
```

- `NewRunner` is accepted only while family is open, total created is below `MaxConsumers`, and an active slot can be reserved at Run time.
- `Run` is accepted exactly once. Success, Guest error, timeout, cancellation and trap all consume that runner and one total-consumer reservation.
- A second or concurrent `Run` returns a stable consumed/busy error and does not enter Guest execution.
- Runner `Close` before Run retires it; Close during Run waits for that run to terminate through context cancellation rules and is idempotent afterward.
- Family Close transitions to closing. It rejects while a run is active, invalidates unstarted runners, then releases image/body/runtime resources exactly once.
- Failed constructor or attachment paths release every acquired module, temporary workspace, mapping and counter.

## Physical dispositions

- `private_cow`: Linux exact-artifact path. One sealed image is used by N fresh `MAP_PRIVATE` mappings. Family owns the immutable image; child Engine close must not close it.
- `private_copy`: portable reference. Family retains an immutable Host-owned body; each fresh Guest receives it through the same binary ABI. Staging is released immediately after preparation.
- `ordinary_fresh`: honest mechanism-off control with no family attachment.
- `unsupported`: fail closed with typed blockers; never label it as COW.

`auto` chooses `private_cow` only after exact eligibility checks; otherwise it selects `private_copy`. Explicit `private_cow` never silently downgrades.

## Initialization equivalence

Canonical COW preparation and private-copy preparation both execute exactly:

```text
_initialize → runtime_init({}) → runtime_prepare_numpy_ndarray
```

A COW consumer instantiates its fresh module/Host context, then restores the sealed Guest memory. It must not invoke the binary preparation again. Tests compare namespace presence, module-global reset, dtype/shape/value, mutation isolation and ordinary response semantics across private-copy and COW lanes.

## Dtype qualification

The Host arithmetic allowlist remains in `numpycodec`. Prepared family v1 admits a dtype only after the exact `numpy-core` Guest proves:

```text
np.dtype(dtype).str == declared dtype
np.frombuffer(body, dtype).reshape(shape, order='C') succeeds
result is C contiguous and has exact nbytes
```

Qualification runs on the real artifact for every promoted dtype and binds the artifact/profile identity. A non-qualified dtype fails closed even if Host arithmetic recognizes it.

## Terminal record

`runtime/engine/wazero` owns a bounded `pysolate.prepared-family-member.v1` record containing only:

- family/image identity;
- member identity and Host execution identity;
- physical disposition;
- terminal state (`ok`, `guest_error`, `cancelled`, `timeout`, `closed_unrun`);
- optional initial/final workspace root identities;
- counters and bounded timings.

It contains no body, source text, response body, credential, Host path or raw native handle. Existing response, workspace receipts and subagent join records remain authoritative in their domains; this record joins identities rather than replacing them.

## Linux Host gate

`scripts/test-host-workstation.sh` stages only clean `HEAD` through `shell2|shell3` to gpu31 and accepts a fixed suite enum. The worker validates canonical paths under `/vol/bitbucket/ys25/pysolate`, uses the shared Go 1.25 toolchain/module/build caches, copies into a private `/tmp` directory, runs allowlisted package commands, writes bounded logs plus `RESULT.READY` and exact `SHA256SUMS`, and cleans only its own directories.

No caller-provided command, environment fragment or remote path is evaluated. A local verifier checks exact source commit/tree, builder, target, suite, pass status and complete checksum coverage. A skipped real-Guest test is not a passing artifact-backed gate.

## Gate P0

Runtime/COW product edits may begin only after:

1. this contract and its source references are current;
2. binary ABI, adapter, state machine and compatibility negative tests have been written and observed RED;
3. mutable RunConfig aliasing has a RED test;
4. the gpu31 Host-test wrapper passes one exact clean-HEAD run;
5. the roadmap records the chosen seams and RED evidence.
