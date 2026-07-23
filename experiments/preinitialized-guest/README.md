# Build-time Python preinitialization spike

## Question

Given the exact locked base Guest, when Wasmtime 44.0.1 Wizer executes the reactor initializer and `runtime_init("{}")` at build time, does the rewritten standard Wasm artifact execute unchanged under the current wazero Host while eliminating per-instance CPython initialization?

## Candidate boundary

The experimental input exports a build-only `runtime_preinitialize` function which calls the reactor `_initialize` and then `runtime_init("{}")`. Wizer captures the resulting linear memory and mutable globals, removes that build-only entry point, and renames a build-only no-op export to the normal runtime `_initialize` export so the existing Host does not rerun reactor constructors.

The custom `agent_runtime_v1.host_call` import is satisfied during transformation by `host-call-stub.wat`, which always returns rejection. The build-time initializer must not acquire Host capabilities.

The production artifact, ABI header, manifest, and default runtime path are unchanged. The candidate is a separate artifact and is never selected automatically.

## Falsifiable gates

1. The locked Wasmtime Wizer transform succeeds twice against one exact input.
2. Both transformed outputs are byte-identical.
3. The candidate passes the existing artifact ABI verifier and real wazero E2E suite.
4. Exact baseline/candidate runtime evidence uses the same clean Host revision, environment, fixture, backend, and sample count.
5. `runtime_init` median improves by at least 10x and fresh-run total median by at least 2x.
6. The candidate remains at or below 512 MiB.

## Iteration evidence

Exact run `30002337205` at signed commit `643e9e9e244c94d430544c4b7d1b410fe595835d` kept the production boundary intact: the locked producer and normal downloaded-artifact wazero consumer passed, while only the exploratory transform job failed. Wasmtime instantiated the exact experimental input, entered `runtime_preinitialize`, spent about 4.8 seconds in initialization, and then observed the wrapper's fail-closed `unreachable` because `runtime_init` returned nonzero. No candidate was emitted or benchmarked. This invalidates the first wrapper shape only; it does not establish whether the failure was caused by explicitly repeating reactor initialization or by CPython/VFS initialization under Wizer.

The second exact run `30003974043` at signed commit `0b4f5856dd76906277ff30b0131f7074040f2132` resolved the boundary. Explicitly calling reactor `_initialize` trapped before returning. Calling only `runtime_init("{}")` succeeded twice with `python_initialized=1`, proving that Wizer owns reactor instantiation for this flow. Wizer emitted two candidates, but the ABI verifier correctly rejected the selected artifact because the unselected diagnostic `runtime_preinitialize` export remained. The two outputs were also not byte-identical: `7ac64dc9...` / 61,063,430 bytes versus `71cbc957...` / 61,056,802 bytes.

The first candidate nevertheless passed the complete real wazero E2E suite from an exact detached `0b4f585` worktree on Darwin, including fresh-state isolation, trusted prepare freshness, timeout recovery, Host-owned capabilities, ambient authority denial, and prepared-pool failure paths. Fresh-runtime candidate evidence measured median `runtime_init_ns=67,979` and `run_total_ns=11,153,542` across all six execute/capability samples. This is supporting cross-platform feasibility evidence, not the required Linux baseline/candidate verdict.

The third iteration removes the diagnostic export and sets CPython's hash seed to the fixed experiment-only value `0xa9e17f5d` before build-time initialization. This is intended to test whether Python hash randomization caused the output mismatch. A fixed shared hash seed is a security and multi-tenant collision-risk trade-off and cannot be promoted merely because it makes the artifact reproducible.

## Verdict: PENDING

No production recommendation is made until a Linux artifact job produces and independently validates the raw transform receipt, both runtime evidence files, and the comparison report.
