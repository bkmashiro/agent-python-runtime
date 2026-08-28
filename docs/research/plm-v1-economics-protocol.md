# PLM V1 economics protocol

Status: frozen before the Gate 6 measurement. The checked-in result is attributed to target
`5bc36725f13034fa5418d6396d1c503820b513d4` and artifact
`sha256:e9e2416f0cd34b397222267ec18637b6973597a2ec9ec4bb9e9bb526eca40585`; it is not evidence for a later code target.

## Question

Measure whether inline sealed-source lowering removes the predecessor's second-Guest cold cost, and report any remaining end-to-end cost without generalising beyond this fixture.

## Controlled inputs

Both arms use the same:

- exact CPython/WASI artifact bytes;
- source bytes and source digest;
- sealed capability Plan and Broker limits;
- provider adapter, 75 ms provider delay and response;
- Run configuration and Host process;
- five samples per arm and profile;
- 750 deterministic scalar assignments followed by one immutable read;
- success result, one logical call and one provider request.

The only execution difference is that the PLM arm enables `plm_capability_calls`. Sample order alternates by iteration to reduce monotonic drift.

## Profiles

### `cold_end_to_end`

The timer starts before Engine creation and stops after Engine close. It includes artifact compile/load, module work, source validation, inline lowering, execution and cleanup.

### `engine_precompiled`

Both Engines are created before the timer. The timer covers Run and close. This removes the same Engine creation work from both arms.

This profile is not called a prepared authority-bearing Guest. Pysolate intentionally rejects public pre-provisioning of a Guest carrying Broker or workspace authority. The protocol does not weaken that policy to manufacture a warmer number.

## Evidence

Each sample records:

- total and Engine setup nanoseconds;
- process `TotalAlloc` delta;
- provider nanoseconds;
- final-Guest instantiate, initialize, runtime-init, source-validation, lowering, selection, trusted-prelude, execution and total nanoseconds;
- candidate provider, validation, linearization and materialization nanoseconds;
- candidate counts, reuse, rejection, discard and canonical restart counts.

The JSON artifact records raw samples and medians. A median delta is descriptive evidence for this source/provider fixture, not a system-wide latency claim.

## Decision rule

Correctness and economics remain independent. If inline PLM is slower on either profile, the evidence says so and PLM remains default-off. The second implementation alternative is attempted only if the first still carries a structural second-Guest cost or cannot meet the mechanism gate; ordinary residual overhead alone does not justify a third runtime framework.
