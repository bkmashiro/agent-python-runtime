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

Preparation adds startup and retained-image cost. The default remains fresh, and different seeds are not placed into an unbounded image cache. Final integrated resource-limit tests and other cost lanes remain in progress.

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
