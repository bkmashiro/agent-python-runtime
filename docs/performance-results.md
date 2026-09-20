# Main-line performance results

## Baseline and seeded reconstruction

Measured in the same 2-vCPU/2-GiB Linux arm64 VM with Go 1.26.0 and Guest `1050ae5118f531c27075a8885954c6bb2bcde911bffdef76850261486958e8a7`. The baseline core is `ae071608`. The driver uses public Run/Resume APIs; it reports construction separately, excludes one warm-up, and includes attempt cleanup in request time.

Initial phase diagnosis (3 fresh instances): instantiation 6.18 ms, `_initialize` 0.025 ms, CPython initialization 478.19 ms. Construction/compilation was about 2.74 s. Fresh, full-copy and normal COW warm request medians were respectively 497.83, 8.79 and 1.98 ms in five-request smoke runs. These are short diagnostics, not capacity or tail claims.

For deterministic reconstruction, the image stores both clean Python memory and the RNG stream position/logical clocks consumed during initialization. It supports one explicitly chosen seed and no PLM. Fresh/seeded copy/COW results match for Python random, NumPy default RNG, set order, clocks, tools and repeated/concurrent attempts.

The COW park probe exposed an actual use-after-unmap: CloseWithExitCode inside a Host import released linear memory before the Wasm stack unwound. Mapping ownership now outlives logical module close and ends at outer Call return. The original failing probe now passes. Failed pre-fix attempts are excluded from the valid comparison and are not counted as successful measurements.

### Matched history replay

Work: replay 128 persisted read outcomes, check their computed result inside Python, and park again on an unresolved approval. Both variants dispatch zero live reads during measured replay. SQLite WAL/FULL and persistence ordering are unchanged. Three alternating independent processes per arm, five warm requests each (15 requests/arm, 3 independent process repetitions):

- Fresh reconstruction: **516.35 ms** request median; construction median **2975.00 ms**.
- Seeded COW reconstruction: **19.46 ms** request median; construction median **3471.57 ms**.
- Request reduction: **96.2%**, about **26.5x** for this warm replay workload.
- Zero unexpected errors across these valid requests. No p95/p99 claim.
- Post-request process RSS medians: fresh **405.2 MiB**, seeded COW **395.8 MiB**. These include Go/compiler/SQLite memory, not just Guest pages, and are not a capacity claim.

Preparation adds startup and retained-image cost. The default remains fresh, and different seeds are not placed into an unbounded image cache. Integrated correctness and resource checks have since passed, as recorded below.

Reproduce with `go run ./cmd/pysolate-bench -mode durable-replay -work tools -calls 128 -n 5`, then the candidate with `-prepare cow` on Linux. `-prepare copy` is available without Linux mapping support. Raw non-sensitive rows are in `docs/performance-data/seeded-replay/`.

## Bounded history read-ahead

The completed-call path now reads at most 64 records or approximately 1 MiB per window (a single larger record must still fit). Consumed entries are released. Only single-assignment completed outcomes are buffered within one attempt; pending/live operations retain transactional admission and commit order. There is no cross-attempt cache, data-version protocol, schema change or reduced durability.

With the seeded-COW path held fixed, 128-call replay process medians changed from 19.40/19.64/20.01 ms to 14.82/15.74/14.95 ms (three processes per arm, five requests each). A small 32-call live-write check was 14.00 vs 14.15 ms: no live-write improvement is claimed. A trial using a per-call read-only fast lookup only improved replay around 7% and added an extra lookup to every live call; that trial was replaced by bounded read-ahead.

## Explicit native compilation cache

The existing wazero cache can be supplied to constructors with `WithCompilationCache`. CLI `-cache DIRECTORY` is explicit and disabled by default. The caller owns its lifetime and must protect the directory as executable native code; it contains compiled Guest code, not user Run state or tool outcomes.

On the same Linux VM, independently launched constructors took about 2.54 s without cache and 89 ms with a populated cache. First population still cost 2.58 s. Full CLI wall times for one ordinary `result = 42` invocation were 3.03/3.11/3.05 s without cache and 0.58/0.57/0.58 s with a hit (about 5.3x by medians, including process startup/input read/cleanup). Native cached files occupied 50,244 KiB, roughly 49 MiB. This is a cache-hit result, not a faster first-ever cold compile.

Raw data: `performance-data/go-paths/`. Cache constructor rows exclude warm-up; the separate `cli-*.time` records measure the complete CLI process.

## PLM fixture correction

An initial diagnostic ended with `sum(...)`, which deliberately makes this small PLM pass fall back. Those rows are not used as evidence for admitted PLM. The harness now uses supported arithmetic and rejects an expected PLM trial unless it actually reports a transformed program. Zero-delay and controlled-delay studies remain separate; fixtures are not claims about real network services.

## Bounded admission and park/re-admit

The executor uses fixed active and queued limits, FIFO ordering, explicit resubmission, cancellation and drain. It does not predict memory usage or choose arbitrary eviction. The real-Guest fixture submits 16 Runs, each privately touches 8 MiB, blocks in a synthetic 200-ms Host call, parks at an approval, and resumes after a persisted decision. Source, Runner, seed, tools and resource envelope are identical between arms; construction and Run creation are outside batch timings. Three separate processes per arm were interleaved on the same VM. Per-request wall latencies and all completion counts are retained in `performance-data/admission/`.

With MaxActive=2, the Executor's park batch was 1.807–1.819 s, versus 1.805–1.819 s for a simple two-slot semaphore. Resume batches were 66–69 ms versus 66–73 ms. Peak simultaneous Host waits stayed at two. This demonstrates bounded behavior with approximately baseline cost, not a throughput improvement over a semaphore.

Unbounded submission reached sixteen Host waits, finishing the park phase around 246–249 ms but sampled process RSS was 607–615 MiB, versus 426–444 MiB for Executor. Once all sixteen Runs were parked, Executor RSS was 402–420 MiB. Measurements are process RSS sampled at tool/park boundaries, not per-Guest memory or a guaranteed global peak. The unbounded arm is a resource/latency trade-off reference, not a same-admission-policy comparison. No default concurrency recommendation follows from this small workload.

## Guest compilation and source-processing work

The Guest build now precompiles 150 frequently imported stdlib/runtime modules with the host CPython from the same build. Standard checked-hash pyc files use virtual `/usr/lib/python3.14/...` filenames. A Guest audit-hook probe confirmed imports consumed bytecode without compiling those source files. No module is imported earlier and no admission rule changes. The Guest grows from 32,919,442 to 34,193,743 bytes (about 1.22 MiB).

The PLM transformer no longer deep-copies its privately owned, unmodified argument AST nodes. Prefix intake parses only the unconsumed complete tail and stops admission at the first barrier. A differential test checks every two-chunk split of ten source shapes against the original algorithm; another test confirms complete leading statements are parsed once. An unfinished statement can still be retried; this is not a general incremental Python parser.

Same Go harness and fixed Linux envelope, 3 processes per arm and 5 warm runs each; medians of per-process medians:

- fresh/no-op: **505.76 -> 67.98 ms**;
- COW, 64 independent PLM calls, zero synthetic delay: **225.10 -> 71.85 ms**;
- COW, 64 streamed prefix calls, zero synthetic delay: **347.85 -> 80.90 ms**;
- COW, 8 PLM calls with 20-ms synthetic delay: **197.73 -> 47.39 ms**.

Raw rows are in [guest-final.jsonl](performance-data/guest-final.jsonl). The prior unsupported `sum(...)` PLM smoke is excluded. Admission is now an executable harness check.

A final matched **`result = 42`** CLI process test compares the original Guest without disk cache to the final Guest with a warmed native cache; [cli-combined.json](performance-data/cli-combined.json) contains all outputs and process times. The median was **3.08 s -> 0.15 s**. This is a combined configuration comparison, not an attribution to either optimization alone. Timers include process startup, file reading, construction, execution and close; shell resolution is 0.01 s.

The final Guest SHA-256 is `9ae9e368764db31505d4314801256361f4117fe288478efff4db321a167245bb`. Keep the old artifact for existing durable definitions: artifact identity remains enforced; this change does not migrate histories.

## Long-running HTTP service hot path

The bounded local service keeps separate prepared no-workspace and workspace images, but shares one in-memory wazero compilation cache between their Runners. On the same 2-vCPU/2-GiB Linux arm64 VM with Go 1.26.0 and artifact `35d931b29b8595aefa6fa1c01763a01afb7a7031d1c0e65bc7db3f69a95ae005` (34,199,690 bytes), sharing compilation reduced one-process service preparation from **5.44 s to 2.95 s**. Both prepared images still initialize independently; the cache removes the second native compilation rather than hiding preparation inside request timing.

The real loopback HTTP benchmark excluded one warm-up per worker and then measured 50 requests per worker. It includes request JSON, TCP loopback, admission, Guest creation/execution/cleanup, response encoding and client decoding. The workspace case also writes one file and computes before/after snapshots plus a diff. Observed final p50/p95 E2E latencies were:

- concurrency 1: plain **1.48/2.33 ms**; workspace **1.71/1.82 ms**;
- concurrency 2: plain **2.05/3.09 ms**; workspace **2.33/3.03 ms**;
- concurrency 4 on two vCPUs: plain **4.13/7.79 ms**; workspace **4.41/6.80 ms**.

An extra request sent while all four slots were blocked received HTTP 429 in **0.10 ms**; admitted requests were not placed in an implicit queue. These are descriptive tails from 50 samples per worker, not production SLOs or broad capacity claims. Raw request rows and exact metadata are in [`performance-data/service-hot/`](performance-data/service-hot/). Reproduce with `go run ./cmd/pysolate-service-bench -guest dist/pysolate.wasm -iterations 50 -concurrency 1,2,4 -max-active 4 -mode all`.

## Common-usecase profile cost

The repository/config/data expansion adds one pinned pure-Python dependency, PyYAML 6.0.3, plus precompiled bytecode and package license metadata. The packed artifact changed from **34,199,690 to 34,684,853 bytes**, an increase of **485,163 bytes (1.42%)**. The native raw core stayed byte-identical at `bac2e4a1e700a6f08443a33eb931dee025b718b7b568643224359f69980ae138`; only the Python VFS was repacked. The qualified packed artifact is `d75b6f9cadc9fd0d04b111da6cff4823bd8b592e088941a6ba1d6208d8aa3729`.

Two alternating old/new runs on the same 2-vCPU Linux arm64 VM used 50 measured requests per worker after one excluded warm-up. Median service preparation changed from **2.947 s to 2.983 s** (+36.7 ms, 1.25%). At concurrency 1, median-of-run p50 E2E changed from **1.55 to 1.53 ms** for plain Run and **1.73 to 1.77 ms** for workspace Run. At concurrency 2 it changed from **2.11 to 2.15 ms** plain and **2.30 to 2.44 ms** workspace. These small differences include normal two-run noise; the concrete workspace convenience costs about 0.04 ms at concurrency 1 in this sample. The real Linux acceptance also passed local module import, TOML/YAML edits, AST inspection, CSV/JSONL plus NumPy processing, one namespaced Host tool call backed by a real local HTTP request, and JSON/CSV/Markdown output. Raw rows and acceptance output are in [`performance-data/common-usecases/`](performance-data/common-usecases/).

## Integration checks and remaining costs

`make check` passes against the final Guest on macOS; all root and durable tests pass in Linux. Targeted real PLM/prefix/seeded-preparation race tests and Store/journal/Executor race tests pass. The final seeded-COW + read-ahead stack also recovered after a Linux VM HardStop between the provider's commit and journal outcome: one read dispatch, two write requests, one idempotent effect. This does not establish physical-host power-loss tolerance.

The throughput comparison deliberately does not claim that Executor is faster than an equivalent semaphore, or that cache hits make compilation free. Keep Runner lifetimes long where appropriate. Use an explicit private native cache for repeated processes. Seeded preparation is appropriate for a shared fixed seed; use fresh attempts when seeds vary. Images keep a one-time memory baseline alive, and COW remains Linux-specific.

Deferred: mutable cross-process payload-size caches and schema changes; general scheduling/polling/persistent queues; PLM plus durable replay; optional omission of transformed-source diagnostics. The latter is now a visible remaining Python-side cost but changing the output contract is outside this slice. Live call persistence still pays WAL/FULL and exact quota accounting; those guarantees were not relaxed.

Two independent read-only cross-checks found no reproducible defects in the reviewed memory/replay and queue/prefix/AST changes. The VM is stopped and task-owned Slurm jobs completed. No CI run, main merge or deployment was triggered.
