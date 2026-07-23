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

## Lifecycle-density evidence contract

[`benchmark/v1/lifecycle-density.schema.json`](../benchmark/v1/lifecycle-density.schema.json) and `runtime/evidence.LifecycleDensityEvidence` define the separate Phase 1 capacity/pressure evidence class. The JSON Schema is the structural gate; every producer and consumer must also call `runtime/evidence.ValidateLifecycleDensityJSON` for cross-sample ordering, histogram shape, derived values, and other semantic relations JSON Schema cannot express. This contract does not replace the fresh or prepared latency schemas above. `apyrun-benchmark -kind lifecycle-density` now orchestrates the initial Linux-only, prepared, idle-ready lane; the fresh-instance active baseline is still a separate unfinished lane.

One file binds one exact artifact/profile, clean Host revision, backend/version, environment, requested strategy, workload, and complete sweep. The initial canonical slot sequence is `1,2,4,8,16`; `32` and `64` may be appended only after an external memory guard proves they are safe. Every `(N, repeat)` row must come from a fresh process and remain in canonical order.

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

The checked-in three-repeat base-profile artifact is [`docs/benchmarks/lifecycle-density-production-safe-linux-amd64.json`](benchmarks/lifecycle-density-production-safe-linux-amd64.json). It binds Host revision `5921411c3716f6ce37caee26a10cff5b036e99a9` and remains raw prepared idle-ready evidence, not a fresh/prepared comparison or capacity model.

The exact build-time-preinitialization experiment archives its raw baseline and candidate as `docs/benchmarks/preinitialization-spike-lifecycle-density-{baseline,candidate}-linux-amd64.json`. Reproduce its strict same-plan comparison with:

```bash
python3 experiments/preinitialized-guest/compare_density.py \
  --baseline docs/benchmarks/preinitialization-spike-lifecycle-density-baseline-linux-amd64.json \
  --candidate docs/benchmarks/preinitialization-spike-lifecycle-density-candidate-linux-amd64.json \
  --output /tmp/preinitialization-density-comparison.json
```

The comparator rejects Host/backend/environment/strategy/plan drift and reports per-N ready wall, aggregate runtime-init work, instantiation, and ready RSS. Its output is descriptive and intentionally has no production approval threshold.

Each raw row records:

- process-instance digest, runtime-shard count, configured RSS/timeout guards, and pool target plus initializing/ready/leased/unhealthy/retiring accounting, so reused processes and duplicated compiled/runtime owners are not misattributed to slot cost;
- active concurrency and queue/instantiate/initialize/runtime-init/prepare/execute/capability/total phases;
- Go heap live/goal, GC cycles/pause, goroutines, and scheduler-latency histogram;
- process RSS/virtual/PSS/private/swap, minor/major faults, FD count, and VMA count;
- cgroup v2 scope and anonymized membership identity. V1 does not accept a process-dedicated claim or any measured cgroup counter: known shared scope skips as `nonisolated_scope`, and every other v2 scope skips as `isolation_unproven`.

Metric shapes distinguish `measured`, `timestamp_observed`, `model_estimated`, `unsupported`, and `skipped`. Unavailable fields carry a bounded reason code rather than a fake zero. Raw measurements cannot be labeled model estimates; optional fixed/per-slot estimates are separate summary fields. Canonical Go validation recomputes sample count, sample order, pool accounting, histogram shape, and measured peaks from raw rows.

Evidence fails closed for a dirty Host worktree, strategy fallback, noncanonical N/sample distribution, reused process identity, missing guards, artifact byte mismatch, cgroup/environment drift, pool counter overflow, mixed metric availability, and fabricated measured summaries. A future cgroup measurement contract must provide an auditable isolation and baseline witness; adding a boolean or observing one PID in the leaf is insufficient. Optional Linux sources use bounded unavailable reason codes rather than zero; malformed required `/proc` state fails collection. The next Phase 1B work is to preserve the first exact-CI prepared raw artifact, add repeated samples, implement a lifecycle-fair fresh-instance baseline, and fit costs only after both lanes are valid.
