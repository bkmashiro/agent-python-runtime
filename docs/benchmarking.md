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

## Provenance

The command fails before measurement unless:

- artifact filename, size, and SHA-256 match the supplied manifest;
- the manifest declares `wasm32-wasip1` and reactor execution;
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

- `production-safe`: three or more execute samples and one-operation capability samples, with small deterministic integer work.
- `full`: the same schema and lifecycle with larger deterministic integer work and 20 capability operations at Host concurrency 8.

Both classes use a local IP-loopback provider with a fixed 2 ms delay per operation. They do not measure production DNS, TCP, TLS, provider rate limits, or provider variance. The class labels keep production-safe recurring measurement distinct from fuller exploratory evidence; they do not turn synthetic results into production latency claims.

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
