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

## Fresh-runtime verdict: VALIDATED

Exact Linux run `30005614619` at signed commit `9a571176bb58c2d6a41312d01ad789abdd6b82e6` passed both Wizer transforms, byte determinism, the production artifact ABI verifier, the complete real wazero E2E suite, exact baseline/candidate fresh-runtime benchmarks, structural schemas, and the independent semantic comparison. The candidate SHA-256 is `053934218445c897d2b6323800225625d9b6feabe3f07068774a2adf4ce52cab`; both transforms produced exactly 61,056,572 bytes.

Across six Linux execute/capability samples, median `runtime_init` fell from 4,385,950,839 ns to 120,980 ns (36,253.37x), and median fresh-run total fell from 4,417,702,155 ns to 31,805,114 ns (138.90x). The artifact increased by 8,471,658 bytes (16.11%). Raw transform and runtime evidence is retained locally under `.artifacts-private/benchmarks/`; it is not committed.

This validates build-time Python preinitialization for exact fresh-instance execution. It does not validate session restore, post-request reset, cross-node portability, capacity, or production use of a fixed shared Python hash seed.

## Lifecycle-density verdict: VALIDATED FOR PERFORMANCE, NOT PRODUCTION

Exact Linux run `30012386233` at verified signed commit `7e4084fa0a55624737421a599dce3408924c475c` measured the ordinary and transformed artifacts under the same backend, kernel, clean Host revision, `N={1,2,4,8,16}` plan, three fresh-process repeats, 5 GiB RSS guard, and three-minute child timeout. All raw documents passed the structural schema and an independent Go `ValidateLifecycleDensityJSON` semantic gate; the transformed candidate also passed artifact-byte binding and repeated-transform equality.

| Ready slots | Baseline median | Candidate median | Speedup | Ready RSS delta |
|---:|---:|---:|---:|---:|
| 1 | 7.82 s | 2.93 s | 2.66x | +7.8 MiB |
| 2 | 12.71 s | 2.95 s | 4.30x | +5.9 MiB |
| 4 | 22.54 s | 2.96 s | 7.61x | +8.0 MiB |
| 8 | 31.57 s | 4.82 s | 6.55x | +74.0 MiB |
| 16 | 63.66 s | 9.81 s | 6.49x | +140.6 MiB |

At N=16, the transformed candidate's three ready-wall samples were 9.71–9.82 s. Aggregate compile work was 38.58 s while aggregate `runtime_init` work was only 2.55 ms, confirming that four concurrent shard compilations and resource contention had become the dominant bottleneck.

Raw baseline, candidate, and comparison evidence is retained locally under `.artifacts-private/benchmarks/`. This validates that build-time preinitialization removes Python initialization as the original N=16 startup wall. Production promotion remains blocked by the fixed shared Python hash seed, release/cross-node portability qualification, and the absence of an opt-in artifact contract.

## Shared compilation-cache verdict: VALIDATED AT MULTI-SHARD DENSITY, EXPERIMENTAL ONLY

The experimental `single-use-preinitialized-shared-cache` strategy keeps one explicitly owned in-memory wazero compilation cache across otherwise separate hard-capped runtimes. The first shard completes compilation before follower shards start, because wazero does not deduplicate concurrent first-time cache misses. Runner closure does not close the borrowed cache; the owner closes it only after all shard runners, and cache closure waits for active compile borrowers.

The exact spike ran both strategies against the same transformed artifact, Host commit, backend, environment, canonical plan, and safety bounds. `compile_ns` records aggregate per-shard compile observations, and the strict comparator rejects artifact, commit, environment, or plan drift while accepting only the explicit normal-to-shared-cache strategy transition.

| Ready slots | Preinitialized | Shared cache | Ready speedup | Compile-work speedup | Ready RSS delta |
|---:|---:|---:|---:|---:|---:|
| 1 | 2.93 s | 2.93 s | 1.00x | 1.00x | +73.9 MiB |
| 2 | 2.95 s | 2.93 s | 1.01x | 1.01x | +15.9 MiB |
| 4 | 2.96 s | 2.94 s | 1.01x | 1.01x | +13.8 MiB |
| 8 | 4.82 s | 3.07 s | 1.57x | 3.16x | -300.9 MiB |
| 16 | 9.81 s | 3.33 s | 2.95x | 10.65x | -799.7 MiB |

At N=16, shared-cache ready wall was 3.22–3.41 s, aggregate compile work fell from 38.58 s to 3.62 s, and ready RSS fell from 2,176.5 MiB to 1,376.9 MiB. Relative to the ordinary untransformed artifact in the same exact run, the combined build-time preinitialization plus shared-cache path reduced N=16 ready wall from 63.66 s to 3.33 s (19.12x) and ready RSS by 659.1 MiB. N=1–4 show no ready-wall benefit and modest RSS overhead, so the intervention is specifically a multi-shard density optimization rather than a universal startup win.

The same real artifact on Darwin reduced aggregate compile work 4.25x but left wall time statistically unchanged in a narrow one-run smoke, reinforcing that wall benefit is CPU/platform dependent. No production threshold or approval is attached. The transformed artifact remains blocked by the fixed experiment-only Python hash seed; a production path would need a trusted deployment-time transform with a non-public per-deployment seed, one attested artifact distributed to all nodes, and cross-node qualification before the shared cache can be promoted with it.
