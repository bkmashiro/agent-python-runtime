# Benchmarking

Benchmarks bind one Host revision to one verified Guest artifact. Raw evidence is local-only; the repository publishes only reviewed summaries and figures.

## Output policy

Write raw runs under a private directory:

```bash
mkdir -p .artifacts-private/benchmarks
chmod 700 .artifacts-private/benchmarks
```

Do not commit provider payloads, credentials, raw model exchanges, node-specific diagnostics, or benchmark JSON. A public result should contain only the method, artifact/source identities, derived statistics, limitations, and a reproducible plotting script.

## Runtime latency

Fresh execution:

```bash
go run ./cmd/apyrun-benchmark \
  -artifact /path/to/agent-python-runtime.wasm \
  -manifest /path/to/manifest.json \
  -output .artifacts-private/benchmarks/fresh.json \
  -class production-safe \
  -samples 3
```

Single-use prepared execution:

```bash
go run ./cmd/apyrun-benchmark \
  -artifact /path/to/agent-python-runtime.wasm \
  -manifest /path/to/manifest.json \
  -output .artifacts-private/benchmarks/prepared.json \
  -class production-safe \
  -strategy single-use-preinitialized \
  -samples 3
```

Linux COW-ready execution requires a fixed-memory artifact:

```bash
go run ./cmd/apyrun-benchmark \
  -artifact /path/to/fixed-memory-agent-python-runtime.wasm \
  -manifest /path/to/manifest.json \
  -output .artifacts-private/benchmarks/cow-ready.json \
  -class production-safe \
  -strategy cow-ready-single-use \
  -samples 3
```

For `numpy-core`, use `-class profile-candidate`. A NumPy-ready run must also bind the `numpy-ready-v1` warmup profile and the matching fixture; it must not be presented as the base production-safe profile.

Fresh evidence validates against [`benchmark/v1/evidence.schema.json`](../benchmark/v1/evidence.schema.json). Prepared and COW-ready evidence validates against [`benchmark/v1/prepared-evidence.schema.json`](../benchmark/v1/prepared-evidence.schema.json).

## Lifecycle notation

Use these terms consistently:

```text
A = CPython/WASI runtime readiness
B = configured pre-COW warmup, such as import numpy
C = request checkout, execute, result validation, and release

fresh NumPy                 = A + B + C
CPython-ready/request import = B + C
NumPy-ready COW hit          = C
factory-to-ready              = A + B
```

Do not compare `A+B+C` with `C` without naming the different preparation boundary.

## Recorded phases

Fresh samples record:

1. guest instantiation;
2. `_initialize`;
3. `runtime_init`;
4. Host-call attachment;
5. request preparation;
6. execution;
7. total run time and byte counts.

Prepared/COW evidence additionally records factory readiness, pool acquisition, warmup and seal time, prepared-image identity, refill/restore phases, and retained Guest memory.

Capability duration is nested inside execution and must not be added to execution time.

## Memory and pressure

The lifecycle-density and pressure harnesses are Linux-only. They can record:

- ready, leased, queued, refilling, and retiring slots;
- process RSS/PSS and page faults;
- named COW mappings and private/anonymous pages;
- cgroup memory events and pressure;
- scheduler admission, reclaim, retry, and refill behavior.

Logical Guest memory, mapping RSS, process RSS, and cgroup usage are different quantities. Reports must identify which one is used.

Use a private output path and an explicit external memory guard. A Slurm `COMPLETED` state or process exit code alone is not acceptance evidence; also verify wrapper exit, checksums, schemas, semantic invariants, source/artifact identity, and full stdout/stderr.

## Phase 6 NumPy-ready pressure

The versioned Phase 6 contract is [NumPy-ready COW density and admission qualification](phase6-numpy-density.md). `cow-pressure` schema v11 adds:

- exact `profile-candidate` + `numpy-core` + `numpy-ready-v1` binding;
- `numpy-v1` and `numpy-mixed-v1` request classes with Host-validated numerical results;
- distinct `closed-loop` and deterministic `open-loop-fixed-v1` modes;
- `offered = accepted + rejected`, `accepted = started`, and `started = completed + failed` accounting;
- latency total/mean and effective `production-policy-v1` telemetry derived from maximum memory, maximum CPU, and greed.

Generate bounded plans without executing them:

```bash
python3 tools/phase6_matrix.py plan --tier canary
python3 tools/phase6_matrix.py plan --tier small
```

The canary has two cells and the small matrix has eleven. `formal` has no default matrix: it requires an explicit JSON selection of successful small-matrix cell IDs and expands each selected cell to exactly three repetitions. The runner refuses a modified Host tree or non-empty output directory and validates source/artifact identity, request conservation, inventory recovery, NumPy request classes, and evidence checksums before writing its run manifest.

## Agent-framework comparison

For an external agentic benchmark, keep task inputs, tool schemas, scoring, and Host capability policy fixed across:

1. the framework's native baseline;
2. WASI fresh;
3. single-use prepared;
4. Linux COW-ready;
5. profile-specific COW warmup when applicable.

Report task success and isolation separately. Required isolation cases are listed in [Framework integration test drive](framework-integration.md).

## Interpretation rules

- Compare latency or throughput only within the same workload, artifact profile, fixture, and machine class.
- Separate startup, preparation, request, refill, and queue time.
- State sample count and percentile definition.
- Keep synthetic/fake I/O separate from real-provider behavior.
- Do not rank unlike machines or workloads.
- Do not treat a prepared lifecycle speedup as faster Python kernel execution.
- Preserve raw evidence privately; publish derived numbers only after schema and semantic validation.

The current reviewed results are in [Host-governed COW scheduler experiment report](reports/scheduler-experiment-results.md).
