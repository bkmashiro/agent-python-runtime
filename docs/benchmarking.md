# Runtime benchmark evidence

The canonical command records phase-level evidence for one exact Guest artifact and one exact clean Host source revision:

```bash
go run ./cmd/apyrun-benchmark \
  -artifact /path/to/agent-python-runtime.wasm \
  -manifest /path/to/manifest.json \
  -output /path/to/runtime-benchmark.json \
  -class production-safe \
  -samples 3
```

The output must validate against [`benchmark/v1/evidence.schema.json`](../benchmark/v1/evidence.schema.json).

For the opt-in single-use preinitialized strategy, use the same command with a distinct evidence contract:

```bash
go run ./cmd/apyrun-benchmark \
  -artifact /path/to/agent-python-runtime.wasm \
  -manifest /path/to/manifest.json \
  -output /path/to/prepared-runtime-benchmark.json \
  -class production-safe \
  -strategy single-use-preinitialized \
  -samples 3
```

Prepared output validates against [`benchmark/v1/prepared-evidence.schema.json`](../benchmark/v1/prepared-evidence.schema.json). The default strategy remains `fresh`, so existing commands and evidence are unchanged.

For an explicit `numpy-core` artifact, replace the class with `-class profile-candidate`; use the same switch for fresh and prepared strategies. The command rejects `profile-candidate` for `base` and rejects `production-safe|full` for `numpy-core` before collecting samples.

## Provenance

The command fails before measurement unless:

- artifact filename, size, and SHA-256 match the supplied manifest;
- the manifest declares a supported `base|numpy-core` artifact profile, `wasm32-wasip1`, and reactor execution;
- the Host Git worktree resolves to an exact revision;
- the Host Git worktree is clean before measurement begins.

The JSON records Guest artifact identity and Host source identity separately. They may differ during an intentional cross-version experiment; consumers must not silently treat such evidence as same-commit evidence.

## Phases

`compile_once` records Host-import instantiation and Wasm compilation once. Every workload sample then creates a fresh guest and records:

1. `instantiate_guest_ns`;
2. `_initialize_ns`;
3. `runtime_init_ns`;
4. `prepare_ns`;
5. `execute_ns`;
6. `capability_ns` (`0` only for the execute-only workload);
7. total Run wall time;
8. request and result bytes.

The capability duration is observed inside the Host broker. It is nested within `execute_ns`; the two values must not be added together.

### Prepared strategy phases

Prepared evidence keeps startup and request paths separate:

- `compile_once` remains Host-import instantiation plus one Wasm compile;
- `readiness` records total `Factory.New` wall time, the initial candidate's instantiate/`_initialize`/`runtime_init`, ready count, and actual queued guest linear-memory bytes;
- `first_execute` records the first exclusive pool hit;
- `steady_execute` and `steady_capability` contain repeated hits after refill;
- every sample records `pool_hit`, request-specific prepare/execute, refill instantiate/init costs, post-Run refill wait, and retained queued memory;
- `state_copy.applicable` is always `false`: the strategy never copies, restores, or reuses a served module, so copy cost is not reported as zero.

Background refill can overlap request execution. `refill_ready_after_run_ns` measures only the remaining wait after the Run returns; the three refill phase durations report the full observed replacement work. Queued-memory evidence covers guest linear memory, not process RSS, compiled code, Go heap, WASI, or other Host overhead.

## Evidence classes

- `production-safe`: three or more execute samples and one-operation capability samples, with small deterministic integer work; accepted only for the default `base` profile (legacy checked-in evidence predates the explicit profile field).
- `full`: the same schema and lifecycle with larger deterministic integer work and 20 capability operations at Host concurrency 8; accepted only for `base`.
- `profile-candidate`: the same bounded small synthetic workload as `production-safe`, but accepted only when the manifest and evidence both bind `artifact_profile: numpy-core`. It is descriptive candidate evidence and does not approve default selection, release, deployment, or production-safe status.
- `preinitialization-spike`: the bounded fresh-runtime workload, or the canonical Linux lifecycle-density sweep, for an exact `base` artifact transformed by the build-time Python preinitialization experiment. Prepared request-serving evidence remains unsupported. Every lifecycle-density artifact carries an explicit experimental limitation; the class never approves default selection, release, deployment, or production-safe status.

All classes use a local IP-loopback provider with a fixed 2 ms delay per operation. They do not measure production DNS, TCP, TLS, provider rate limits, or provider variance. The class labels separate recurring base measurement, fuller exploratory evidence, and opt-in profile-candidate evidence; none turns synthetic results into a production latency claim. A class/profile mismatch fails before benchmark samples run.

### Local fake-provider durability benchmarks

The fake job and mail packages contain separate Go benchmarks for transaction restart mechanics. They exercise real runtime coordinator, adapter, digest validation, JSON serialization, and plaintext `devsnapshot` SQLite WAL paths while keeping provider behavior deterministic and local:

```bash
go test ./runtime/capability/fakejob \
  -run '^$' -bench '^BenchmarkFakeJobDevelopmentCheckpoint$' \
  -benchmem -count=5

go test ./runtime/capability/fakemail \
  -run '^$' -bench '^BenchmarkFakeMailDevelopmentCheckpoint$' \
  -benchmem -count=5
```

The job lanes separate ambiguous-cancel checkpoint writes from provider/controller reopen. The mail lanes do the same for three-component provider/adapter/send-controller checkpoints at canonical 1 KiB and 64 KiB body sizes. The setup paths prove one accepted irreversible effect before timing; correctness tests separately prove post-restart reconciliation without duplicate dispatch and compensatable draft rollback after restart.

These measurements are suitable for comparing runtime checkpoint overhead, allocation behavior, payload-size scaling, regression, and local concurrency. They are not evidence for real provider network latency, availability, rate limits, delivery semantics, or recall behavior, and must not be pooled with artifact-bound runtime evidence or live-model task benchmarks.

## Interpretation

Raw samples are canonical. Derived medians, deltas, and thresholds must name the artifact SHA-256, Host revision, OS/architecture, Go version, backend, reset mode, evidence class, and sample count. Cross-platform comparisons must use the same schema and fixture; thresholds are added only after a verified Linux baseline exists.

Use the descriptive comparison tool for equal-class/equal-fixture sample sets:

```bash
python3 tools/compare_runtime_benchmarks.py \
  docs/benchmarks/runtime-production-safe-darwin-arm64.json \
  docs/benchmarks/runtime-production-safe-linux-amd64.json \
  --output /tmp/runtime-comparison.json
```

The tool reports medians and candidate/baseline ratios while deliberately emitting no pass/fail or threshold. A threshold requires a separately reviewed product budget and repeated same-environment evidence; cross-platform ratios are descriptive only.

Compare same-run fresh and single-use prepared evidence with the dedicated strict identity/fixture check:

```bash
python3 tools/compare_prepared_benchmarks.py \
  /path/to/runtime-production-safe-linux-amd64.json \
  /path/to/prepared-production-safe-linux-amd64.json \
  --output /tmp/prepared-comparison.json
```

This reports fresh versus prepared first/steady Run ratios, refill `runtime_init`, startup readiness, retained guest memory, and the explicit state-copy N/A record. It also has no threshold or pass/fail field.

## Reactor state census

Before enabling any COW strategy, compile the exact manifest-bound reactor and record which state classes are visible through the current public wazero API:

```bash
go run ./cmd/apyrun-benchmark \
  -kind reactor-census \
  -artifact /path/to/agent-runtime-base.wasm \
  -manifest /path/to/manifest.json \
  -output /tmp/reactor-census.json \
  -class production-safe

go run ./cmd/validate-json-schema \
  benchmark/v1/reactor-census.schema.json \
  /tmp/reactor-census.json
```

The census distinguishes linear-memory COW eligibility from complete restore eligibility. A single owned, exported, fixed-size memory may be COW-memory eligible while the overall reactor remains `single-use-only` because mutable globals and tables are not exhaustively visible through wazero's public compiled-module API. The command requires an exact clean Host revision and never activates a COW strategy.

## Lifecycle-density evidence contract

[`benchmark/v1/lifecycle-density.schema.json`](../benchmark/v1/lifecycle-density.schema.json) and `runtime/evidence.LifecycleDensityEvidence` define the separate Phase 1 capacity/pressure evidence class. The JSON Schema is the structural gate; every producer and consumer must also call `runtime/evidence.ValidateLifecycleDensityJSON` for cross-sample ordering, histogram shape, derived values, and other semantic relations JSON Schema cannot express. This contract does not replace the fresh or prepared latency schemas above. `apyrun-benchmark -kind lifecycle-density` now orchestrates the initial Linux-only, prepared, idle-ready lane; the fresh-instance active baseline is still a separate unfinished lane.

One file binds one exact artifact/profile, clean Host revision, backend/version, environment, requested strategy, workload, and complete sweep. The initial canonical slot sequence is `1,2,4,8,16`; `32` and `64` may be appended only after an external memory guard proves they are safe. Every `(N, repeat)` row must come from a fresh process and remain in canonical order.

`phases.compile_ns` is an optional backward-compatible v1 field so archived evidence remains valid. New prepared-density producers record the aggregate per-shard `CompileModule` observations. The experimental `single-use-preinitialized-shared-cache` strategy requires positive measured `compile_ns` in every sample plus explicit first-shard/cache-ownership and no-production limitations; `production-safe` CLI evidence continues to reject that strategy.

Run the initial base-profile lane from a clean Linux checkout with an explicit RSS kill threshold and per-child timeout:

```bash
go run ./cmd/apyrun-benchmark \
  -kind lifecycle-density \
  -artifact /path/to/agent-runtime-base.wasm \
  -manifest /path/to/manifest.json \
  -output /tmp/lifecycle-density.json \
  -class production-safe \
  -strategy single-use-preinitialized \
  -samples 1 \
  -max-rss-bytes 5368709120 \
  -child-timeout 3m
```

`-samples` is repeats per canonical N, not a request to truncate the sweep. The parent launches a distinct bounded child for every row, rejects backend/environment/artifact drift, generates a unique process-instance digest from the observed launch identity, and writes only after object validation, artifact-byte binding, and canonical semantic JSON validation succeed. The RSS threshold is a sampled kill guard rather than a kernel reservation; the evidence records that limitation explicitly.

For `cow-ready-single-use`, lifecycle-density additionally requires mapping-attributed `/proc/self/smaps` evidence for every `memfd:apyrun-cow-image` VMA. The aggregate records virtual bytes, RSS, PSS, shared/private clean/dirty bytes, referenced bytes, and anonymous bytes. A sealed memfd/shmem page can appear as `Private_Dirty` when it has one mapping and as `Shared_Dirty` after another mapping is added; that map-count classification is not proof of a private COW copy. `Anonymous` plus phase-attributed mutation evidence is the safer signal for private MAP_PRIVATE pages. Logical retained guest bytes and mapping RSS are not physical-capacity measurements.

## Process fixed-cost attribution

`apyrun-memory-probe` isolates each `0,1,4,64` slot control in a fresh child and pauses it at `process-start`, `artifact-loaded`, Host instantiation, compile, canonical guest initialization, sealed-image creation, and final ready capacity. The parent—not the measured child—collects `/proc/<pid>/status`, `smaps_rollup`, `maps`, `fd`, faults, and named COW mappings before releasing each checkpoint. Every raw checkpoint is followed by a `runtime.GC` plus `debug.FreeOSMemory` settled checkpoint, separating transient compiler/initializer heap from retained Runtime, CompiledModule, canonical image, and slot state:

```bash
apyrun-memory-probe \
  -artifact /absolute/path/agent-python-runtime.wasm \
  -output /absolute/private/path/cow-memory-attribution.json \
  -profile-dir /absolute/private/path/heap-profiles \
  -cache-dir /absolute/private/path/wazero-cache \
  -slots 0,1,4,64
```

The output is written with mode `0600`. Schema v2 records only `compilation_cache_mode: disabled|disk`, never the cache path. `-cache-dir` is optional and user-owned; the probe creates/chmods it to `0700`, while `wazero.NewCompilationCacheWithDir` owns cache-format and artifact/config compatibility checks. Reusing the directory across isolated controls measures warm disk-cache startup without transferring Runtime, CompiledModule, or guest state between children. Full `smaps` parsing occurs only while the child is paused, never in timed execution or refill paths. Slot zero still constructs the Host runtime and compiled module but does not create a canonical COW image; later controls expose canonical-image and marginal ready-slot effects. Differences remain mechanism attribution for that exact binary/artifact/kernel, not a production capacity claim.

## Bounded COW pressure evidence

[`benchmark/v1/cow-pressure.schema.json`](../benchmark/v1/cow-pressure.schema.json) defines the machine-readable extreme-test envelope. Schema v7 owns one bounded wazero Runtime, one compiled module, and one sealed baseline for every admitted COW slot. It records bounded post-load replenishment as `complete` or `timeout`; a timeout retains the final pool and memory snapshot instead of discarding an otherwise valid capacity/load boundary run. The default pool remains unchanged; an explicit `cow-pressure` run may raise the COW-only hard envelope to 65,536 slots so a paper experiment can locate a real PSS/headroom knee instead of stopping at the former 4,096-slot harness cap. The Linux baseline writer preserves all-zero native pages as sealed memfd holes and coalesces contiguous non-zero pages into write extents; reads from holes remain canonical zero bytes. Optional warmup profiles are artifact-owned guest handlers selected by `wazero.Factory.COWWarmupProfile`; Host code accepts only a bounded lowercase profile ID, never arbitrary Python source or an external raw-memory image. Artifact builders register a deterministic handler with `agent_runtime.register_warmup_profile("name.v1", handler)` during guest bootstrap, and the guest rejects unknown profiles. The Host executes the selected profile only on the canonical module after `_initialize` and `runtime_init`, blocks capability calls, and binds the verified artifact bytes plus exact profile ID into the prepared-image generation digest. Empty profile remains the production default. Slots still instantiate a fresh module shell before attaching the sealed linear-memory image. Each phase snapshot binds the immutable prepared-image census: virtual and allocated bytes, Linux page size, zero/non-zero page counts, and the theoretical sparse-storage upper bound. It begins with four slots, doubles bounded growth batches, and caps each later admission step at 64 slots up to the configured hard maximum. These are accounting batches, not runtime shards. Served slots are destroyed and replenished through a deficit-deduplicated queue with ordered watermarks and a bounded retry/breaker policy. The automatic refill-worker default remains capped at four; explicit pressure runs may select 1, 2, 4, 8, 12, or 16 workers to measure whether replenishment is CPU-scalable or limited by instantiate/mapping contention. Phase-boundary snapshots include pool lifecycle gauges, Go heap/GC/scheduler metrics, process faults and `VmPTE`, scoped raw cgroup memory/events/PSI counters, and mapping-attributed `smaps`. Shared cgroup values remain job-boundary observations, not process attribution.

The first same-artifact `request-shell-v1` A/B passed the production attach and refill gates but failed the promotion gate: it added 4 KiB to the sealed baseline and added 52 KiB of active named-mapping `Private_Dirty` at every 0/1/4/8/16/32 MiB sample. Post-refill dirty still returned to zero. The profile therefore remains default-off and must not be described as a density optimization; the result suggests that priming and then freeing generic request-shell objects only adds allocator state, while request-specific JSON, namespace, compile, and result objects must still be written privately.

A 40 GiB allocation that reserves 8 GiB outside the runtime is expressed as two separate values, not as a claimed capacity result:

```bash
go run ./cmd/apyrun-benchmark \
  -kind cow-pressure \
  -artifact /path/to/fixed-memory-agent-runtime.wasm \
  -manifest /path/to/manifest.json \
  -output /private/path/cow-pressure.json \
  -class production-safe \
  -strategy cow-ready-single-use \
  -memory-budget-bytes 34359738368 \
  -memory-reserve-bytes 8589934592 \
  -max-pressure-slots 4096 \
  -consumers 16 \
  -pressure-workload cpu \
  -pressure-refill-workers 4 \
  -pressure-duration 30s
```

Admission uses measured process PSS plus conservative dynamic headroom and stops before the 32 GiB runtime budget. The surrounding scheduler/cgroup must independently enforce the 40 GiB allocation; the PSS policy is not a kernel reservation. The hard slot bound prevents an accounting bug from creating an unbounded loop.

After admission, closed-loop fake consumers continuously submit a small deterministic Python request. Evidence records one runtime instance, admitted slots, every growth PSS snapshot, final COW mapping metrics, stop reason, ready counts, started/completed/failed/timed-out requests, throughput, p50/p95/p99/max service latency, process user/system CPU, average utilized cores, `GOMAXPROCS`, phase totals, and separate replenish drain. Checkout requests replenishment through a bounded queue. Automatic/default pools remain capped at four refill workers; explicit pressure experiments may select `1,2,4,8,12,16` without changing that production default.

Full `/proc/self/smaps` collection is excluded from ordinary timed loads because page-table traversal over large mapping sets can dominate the workload being observed. Schema v7's `dirty-hold` lane is the explicit exception: it takes three fixed-offset active snapshots while Guest bytearrays remain live, includes that diagnostic perturbation in measured time, and then records a separate post-refill final snapshot. CPU and timer-only lanes still make no in-load physical-memory claim.

Production COW slot admission does not fault and SHA-256 all 128 MiB. It relies on the sealed image FD, fixed shape, allocator ownership, and remap result. `wazero.Factory.VerifyCOWPreparedImage` is an explicit bounded diagnostic switch that restores full-image verification and emits `cow_verify`; pressure production evidence leaves it disabled.

`pressure-duration` controls the request-issuance window. Workers finish requests already in flight after that window closes; `load.duration_ns` and `throughput_per_second` cover issuance plus this bounded request drain. `replenish_drain_ns` then measures restoration of the ready pool separately and is excluded from request throughput.

The pressure lane does not claim open-loop sustainable throughput, provider latency, complete served-slot restore, or a general machine-capacity model. Evidence files are written with mode `0600`; private run directories must remain `0700`.

`-pressure-workload cpu` keeps the tiny request compute-only. `-pressure-workload wasi-timer-wait -pressure-wait 100ms` adds a bounded Guest WASI timer wait so a consumer sweep can distinguish CPU-active work from wait-hiding concurrency. `-pressure-workload dirty-hold -pressure-dirty-bytes 16777216 -pressure-wait 2s` allocates a real Guest bytearray, writes one byte per 4 KiB page, and holds it across the three active snapshots. Neither wait workload is external network, filesystem, database, or provider-I/O evidence.

The checked-in three-repeat base-profile artifact is [`docs/benchmarks/lifecycle-density-production-safe-linux-amd64.json`](benchmarks/lifecycle-density-production-safe-linux-amd64.json). It binds Host revision `5921411c3716f6ce37caee26a10cff5b036e99a9` and remains raw prepared idle-ready evidence, not a fresh/prepared comparison or capacity model.

The exact build-time-preinitialization experiment archives its raw baseline and candidate as `docs/benchmarks/preinitialization-spike-lifecycle-density-{baseline,candidate}-linux-amd64.json`. Reproduce its strict same-plan comparison with:

```bash
python3 experiments/preinitialized-guest/compare_density.py \
  --baseline docs/benchmarks/preinitialization-spike-lifecycle-density-baseline-linux-amd64.json \
  --candidate docs/benchmarks/preinitialization-spike-lifecycle-density-candidate-linux-amd64.json \
  --output /tmp/preinitialization-density-comparison.json
```

The shared-cache intervention adds `docs/benchmarks/preinitialization-spike-lifecycle-density-shared-cache-{candidate,comparison}-linux-amd64.json`. Reproduce the same-artifact strategy transition with:

```bash
python3 experiments/preinitialized-guest/compare_density.py \
  --baseline docs/benchmarks/preinitialization-spike-lifecycle-density-candidate-linux-amd64.json \
  --candidate docs/benchmarks/preinitialization-spike-lifecycle-density-shared-cache-candidate-linux-amd64.json \
  --intervention shared-compilation-cache \
  --output /tmp/shared-cache-density-comparison.json
```

The comparator rejects Host/backend/environment/plan drift. Default mode also requires identical strategies and distinct artifacts; shared-cache mode instead requires the same artifact, the exact normal-to-shared strategy transition, measured compile work, and explicit ownership/no-production limitations. Reports are descriptive and intentionally have no production approval threshold.

Each raw row records:

- process-instance digest, runtime-shard count, configured RSS/timeout guards, and pool target plus initializing/ready/leased/unhealthy/retiring accounting, so reused processes and duplicated compiled/runtime owners are not misattributed to slot cost;
- active concurrency and queue/optional compile/instantiate/initialize/runtime-init/prepare/execute/capability/total phases;
- Go heap live/goal, GC cycles/pause, goroutines, and scheduler-latency histogram;
- process RSS/virtual/PSS/private/swap, minor/major faults, FD count, and VMA count;
- cgroup v2 scope and anonymized membership identity. V1 does not accept a process-dedicated claim or any measured cgroup counter: known shared scope skips as `nonisolated_scope`, and every other v2 scope skips as `isolation_unproven`.

Metric shapes distinguish `measured`, `timestamp_observed`, `model_estimated`, `unsupported`, and `skipped`. Unavailable fields carry a bounded reason code rather than a fake zero. Raw measurements cannot be labeled model estimates; optional fixed/per-slot estimates are separate summary fields. Canonical Go validation recomputes sample count, sample order, pool accounting, histogram shape, and measured peaks from raw rows.

Evidence fails closed for a dirty Host worktree, strategy fallback, noncanonical N/sample distribution, reused process identity, missing guards, artifact byte mismatch, cgroup/environment drift, pool counter overflow, mixed metric availability, and fabricated measured summaries. A future cgroup measurement contract must provide an auditable isolation and baseline witness; adding a boolean or observing one PID in the leaf is insufficient. Optional Linux sources use bounded unavailable reason codes rather than zero; malformed required `/proc` state fails collection. Build-time preinitialization and shared-cache evidence remain experimental; production promotion is blocked on a non-public per-deployment hash seed, one attested transformed artifact distributed across nodes, and cross-node qualification.
