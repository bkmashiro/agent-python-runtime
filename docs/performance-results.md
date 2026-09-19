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
