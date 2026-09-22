# Warm execution performance dissection

## Findings

The first optimization target is Guest instantiation, not JSON or the durable journal. On this Linux x86_64 host and artifact, warm COW execution still applies Wasm data segments before attaching the prepared image. CPU sampling attributes most measured CPU time to that copy path.

This is a diagnosis, not an optimization result. No scheduler, durability policy, Guest artifact, or initialization semantics were changed.

### Measured request latency

Three independent processes per timing variant, 200 sequential requests per process, one excluded warm-up. Values below are medians of process medians, not pooled samples:

| Workload | Original binary | Instrumented, disabled | Phase timing enabled |
| --- | ---: | ---: | ---: |
| Short Python | 17.732 ms | 17.743 ms | 17.704 ms |
| One immediate Tool | 17.096 ms | 17.147 ms | 17.145 ms |
| One immediate durable Tool, finish | not available | 18.871 ms | 18.798 ms |

The original binary is built from `3e98b229`. Its durable benchmark parks, so it is deliberately not used as a baseline for the new finish case. The original/disabled differences for the two comparable cases are about 0.06% and 0.29%; this small experiment detects no material regression, but does not prove zero instrumentation cost. Phase-only differences overlap process variation. CPU and allocation profiles are separate diagnostic processes, not the latency baseline.

The Python case and Tool case execute different scripts; their difference is not a direct measurement of Tool overhead. Durable finish includes definition creation and final persistence; ordinary Tool execution does not. The roughly 1.72 ms difference is the cost of that whole durable path on this fixture, not just a SQLite query.

### Phase attribution

Median across three processes of each phase's mean per request:

| Phase | Short Python | Immediate Tool | Durable Tool |
| --- | ---: | ---: | ---: |
| new_guest | 16.040 ms | 16.139 ms | 16.343 ms |
| execute, inclusive | 1.757 ms | 1.178 ms | 2.035 ms |
| guest_close | 0.105 ms | 0.098 ms | 0.102 ms |
| marshal | 0.012 ms | 0.012 ms | 0.005 ms |

`new_guest` accounts for about 90% of the short Python path. `read_response` is already inside `execute`; journal spans and Tool waits are also nested inside it. **Do not add all reported spans together or call execute's wall time Python CPU time.** Some overhead is outside these spans. Phase means are not required to sum to a request median.

For the durable case, Store operation means per request are: create 0.134 ms, get (four calls combined) 0.231 ms, begin_call 0.361 ms, complete_call 0.274 ms, read_completed 0.094 ms, set_state 0.263 ms. These are secondary to Guest creation in this workload. Their synchronous commit/recovery semantics must not be weakened to improve a benchmark.

### CPU and allocation hotspots

Measured-round CPU profiles (1,000 requests each, startup excluded):

- Short Python: `runtime.memmove` 72.14% flat; `ModuleInstance.applyData` 73.61% cumulative.
- Immediate Tool: memmove 75.17%; applyData 76.44% cumulative.
- Durable Tool: memmove 70.51%; applyData 72.16% cumulative.

The call path is `Runner.newGuest → InstantiateModule → ModuleInstance.applyData → memmove`. Source at `prepared.go:newGuest` instantiates and invokes `_initialize` before `cow.Attach`. This explains why a COW runtime can still pay for writes during instantiation. Whether those writes can safely be eliminated requires a separate experiment: globals, tables, passive segments, initialization side effects, workspace mounts and recorded entropy must remain correct. Do not skip initialization or reuse a previously executed Guest indiscriminately.

Allocation counters over 500 requests show roughly 1.23 MB allocated and 12.6k allocations per ordinary request; durable is about 1.26 MB and 13.3k allocations. These are cumulative Go allocations, not retained memory, mmap memory, or bytes copied. Subtracted sampled allocation profiles identify `FunctionInstanceReference` as approximately 57–59% of allocated bytes, followed by applyData metadata, tables, and call stacks. These are candidates for later work after the dominant copy cost is addressed.

The benchmark reads `/proc` memory information between rounds. This work is outside each request timer but inside the CPU/profile interval: about 7% of short-Python CPU samples are attributable to `main.memory`. Profile percentages therefore describe the measured benchmark interval, not a perfectly isolated runtime. Wasm/JIT samples also appear as `_ExternalCode`; this profile does not resolve Python functions.

### Concurrent slow Tools

For 64 tasks, 50 ms synthetic Tool delay, two running/eight resident/eight Tool slots:

- Phase-only Inline batch: 3.397 s.
- Phase-only ExternalIO batch: 1.187 s.
- ExternalIO Tool resume wait: mean 12.27 ms per request; Tool-capacity queue wait about 0.002 ms.

This is a diagnostic single batch per arm, not a replacement for the repeated [density study](linux-density-study.md). Go trace sync profiles mostly attribute cumulative blocking to `Attempt.Wait/Result`. These sums span many goroutines and are **not elapsed batch time or CPU consumption**. ExternalIO can improve batch throughput while increasing an individual Guest's inclusive execution time. Creation CPU cost and waiting to regain a running slot are worth examining before changing queue fairness.

## Instrumentation

An internal context-scoped `perfdiag` collector records fixed phase names, counts and elapsed nanoseconds. It stores no source, arguments, results or run identifiers. No public runtime observer interface was added. Disabled spans skip clocks/map updates; original cancellation-independent persistence contexts and independent Guest/state cleanup are retained.

Both benchmark commands accept:

- `-phases path.json`: measured phase aggregates.
- `-measured-cpuprofile path.pprof`: measured-interval CPU profile.
- `-traceprofile path.trace`: measured-interval Go runtime trace.
- `-allocprofile path.json`: measured allocation counter deltas plus `.before.pprof`/`.after.pprof` cumulative sampled allocation snapshots. Use `pprof -base` to subtract setup. Snapshot flushing forces GC outside request timing and perturbs the diagnostic run; do not use it as a latency baseline.
- Existing `-cpuprofile` retains its construction-inclusive scope; do not combine it with measured CPU profiling.

Enabling any measured profiling option also enables the phase collector, although phases are written only when `-phases` is supplied. CPU and allocation observations thus include its bounded bookkeeping. Allocation profiles can also include profiler/output bookkeeping. Counter fields `heap_alloc_bytes` and `heap_inuse_bytes` in the delta structure are end snapshots, not arithmetic deltas or guaranteed live-only retained bytes.

`pysolate-bench -durable-case finish` measures definition creation, execution and completion instead of the default approval park. Terminal durable-replay finish returns the saved terminal result rather than re-executing Python; do not use that combination to measure replayed Guest execution.

## Reproduction and evidence

Campaign 292690: 36 successful processes, 9,684 measured requests, Linux x86_64, two pinned logical CPUs, GOMAXPROCS=2, verified 2 GiB cgroup ceiling, shared Slurm node. This host/artifact is not interchangeable with earlier arm64 or macOS measurements. Initial job 292689 failed a script preflight before measuring and is excluded.

- [Raw rows, environment, phase JSON and pprof text](performance-data/dissection-292690/)
- [Summary](performance-data/dissection-292690/summary.json)
- `tools/run-perf-dissection.py`: bounded Linux job runner; its directory argument must contain `bench`, `baseline`, `queue`, `pysolate.wasm`, and `run-linux-density.py`.
- `tools/summarize-perf-dissection.py`: regenerate the numeric summary from collected rows.

Example profiling commands after building the current binaries:

```sh
./bench -mode cow -work python -n 1000 -measured-cpuprofile python.pprof
go tool pprof -top ./bench python.pprof
./bench -mode cow -work python -n 500 -allocprofile python.alloc.json
go tool pprof -top -alloc_space -base python.alloc.json.before.pprof ./bench python.alloc.json.after.pprof
./queue -case read-finish -tasks 64 -active 2 -resident 8 -tool-active 8 \
  -heap 8 -hold 50ms -warmup 2 -external-io=true -traceprofile queue.trace
go tool trace queue.trace
```

Binary CPU/allocation profiles and traces remain local artifacts rather than inflating Git. Text summaries and raw numerical measurements are versioned. Raw profiler files may contain symbols, paths, goroutine stacks and runtime details even though phase JSON omits request payloads.

## Recommended first experiment

Investigate removing data-segment initialization work that is immediately superseded by attaching the clean COW image. First establish which module state is covered by the image and which still needs normal instantiation. The candidate must bypass the old copy path, retain a fresh isolated Guest, and pass workspace, deterministic replay, NumPy, cancellation and ordinary execution tests. Measure ordinary and durable end-to-end latency plus concurrent throughput before keeping any change. No engine fork or snapshot redesign is justified until a bounded prototype proves a net gain.
