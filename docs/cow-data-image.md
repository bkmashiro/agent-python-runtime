# Opt-in COW data images

This Linux-only optimization removes repeated active-data copying during Guest instantiation. It leaves the default COW path unchanged and never reuses a Guest that has executed user code. There is no wazero fork and no change to the distributed Guest artifact.

## Measured result

On the same shared Linux x86_64 node, pinned to two logical CPUs with `GOMAXPROCS=2` and a verified 2 GiB cgroup ceiling, three alternating process pairs per workload completed 3,600 measured requests. Both arms used the same unchanged Guest artifact, one warm-up and 200 measured requests per process. Phase collection was enabled in both arms. These are runtime API measurements, not HTTP or production network latency.

| Workload | Baseline median | Data image median | Median paired speedup |
| --- | --- | --- | --- |
| Short Python | 17.82 ms | 2.90 ms | 6.14× |
| One immediate Host call | 17.23 ms | 2.31 ms | 7.45× |
| Durable completion, one Host call | 19.03 ms | 4.07 ms | 4.67× |

Each latency is the median of three process medians; speedup is the median of paired ratios. Durable timing includes creating and completing each Run. Startup remains separate: median Runner construction was about 6.95/6.98 s for ordinary Python and 7.85/7.96 s for durable. This is not a cold-start optimization.

The one-time instruction walk measured **45.65 ms** with zero Go allocations; full transformation including that walk measured **60.70 ms** and about 46 MB transient Go allocation. These are medians of three five-iteration benchmark runs, not per-request costs. Only the walk would be amortized in roughly four short requests at the observed saving; total construction and memory costs must be evaluated separately.

Retained sealed-image allocation increased from **128 MiB to 151.41 MiB** (one extra sparse seed, **23.41 MiB**). Process memory is a different measure: the median of per-process sampled batch PSS maxima increased from **450.57 to 589.48 MiB** for short Python. Do not infer that total memory only increased by the seed size, add the two measurements together, or claim a memory-density improvement from this latency experiment. The unexplained process-memory delta warrants profiling; this option remains off by default.

Raw [per-process summaries](performance-data/cow-data-image-292705/summary.jsonl), [environment](performance-data/cow-data-image-292705/environment.json), request rows and phase files live under `performance-data/cow-data-image-292705/`. [Verification output](performance-data/cow-data-image-292705/verification.txt) includes the final guard, isolation, timeout and deterministic NumPy/replay tests plus the one-time scan benchmark. Earlier failed measurement-script batches are excluded. `tools/run-cow-data-image.py` reproduces the paired workload matrix within an existing allocation.

## Usage

```go
runner, err := pysolate.NewPreparedCOW(ctx, wasm, tools,
    pysolate.COWOptions{DataImage: true})
```

The same option is accepted by `NewPreparedRecordedCOW`. Workspace callers use `NewPreparedWorkspaceCOWWithOptions`. Durable callers set `durable.Preparation{Seed: seed, COW: true, COWDataImage: true}`. Requesting the data-image option without COW is rejected.

For a direct comparison:

```sh
go run ./cmd/pysolate-bench -mode cow -work python -n 200 -cow-data-image=false
go run ./cmd/pysolate-bench -mode cow -work python -n 200 -cow-data-image=true
```

`pysolate-queue-bench` also accepts `-cow-data-image` with `-cow`. The standalone service CLI does not expose this option yet. Unsupported modules fail explicitly; the opt-in path does not silently revert to ordinary instantiation.

## What changes

At construction, the runtime copies all non-Data sections byte-for-byte. Each active data segment keeps its index, classification, memory index and offset, but its payload becomes empty. Passive data segments stay unchanged. The original active payloads are written, in original order, into a sealed sparse memfd. Overlapping segments therefore preserve last-write behavior.

Only the transformed module is compiled. Each new instance receives the original-data mapping **before** `_initialize`. The first instance then initializes Python and captures the final clean image. Later instances run `_initialize` against original data and attach the final Python image afterward, exactly preserving the old ordering of initialization versus restore. Globals, tables and engine state are still constructed per instance.

The runtime retains two immutable images per Runner:

- the original data seed, needed to preserve `_initialize` behavior;
- the prepared Python image, used for execution.

Reading sparse seed holes while capturing the first full snapshot materializes tmpfs pages. Construction therefore closes that initial Guest and rebuilds the seed from its original segments once, before returning the Runner. This reduces retained seed allocation without altering bytes or per-request execution. The temporary segment descriptors and transformed bytes are not intentionally cached separately by the Runner.

## Admission checks

The optimization requires one bounded local 32-bit memory, no imported memory and no automatic Wasm start function. Active offsets must be supported `i32.const` expressions within initial memory bounds. Unsupported memory flags, nonconstant offsets, malformed section/LEB encodings, duplicate relevant sections and out-of-bounds segments are rejected.

A one-time instruction walk rejects `memory.init` or `data.drop` references to active segments. This matters because the pinned wazero version retains segment bytes in its data instances. Passive-segment references remain allowed. The walker handles the supported MVP and bulk-memory/table instruction encodings; unknown instructions, including SIMD and atomics in this narrow path, fail closed. It parses instruction boundaries rather than looking for byte patterns inside constants.

These checks run during Runner construction, not per request. wazero still validates and compiles the transformed module. This is an artifact-specific optimization with explicit admission, not a general Wasm snapshot implementation.

## Verification

- Transformation tests preserve every non-Data byte, passive payloads, segment indexes and overlapping placement.
- Rejection tests cover active-segment references, instruction-like bytes inside constants, truncated instructions, unsupported instructions and over-width LEB values.
- Linux tests check sealed mappings, growth, isolation, error/timeout recovery, cleanup and sparse seed storage.
- Real Guest comparisons cover fresh/copy/COW/data-image deterministic random bytes, clocks, hash behavior, NumPy and Host Tools, including concurrent recorded execution and journal-stop recovery.
- Workspace tests run the same per-run attachment checks against the new constructor.

The checked Guest SHA-256 is `2c63ce407eae626c89fcd1dd918c713b1f8b75a35a6cf6ca6861466168d0faf8`.
