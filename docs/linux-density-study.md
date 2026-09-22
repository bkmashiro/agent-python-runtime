# Linux throughput and residency study

This study compares Inline and ExternalIO scheduling under the same CPU and memory limits. The runtime scheduler is unchanged. The workload uses real Python Guests, COW preparation and SQLite durability, with a synthetic Host wait.

## Measured results

Campaign `292688` ran 50 independent processes and completed all 1,600 measured tasks with zero errors. Each process submitted a burst of 32 tasks after two sequential warm-ups. Each Python task allocated an 8 MiB bytearray, waited on one synthetic read Tool, and returned a checked result.

Controls: Linux x86_64, two logical CPUs pinned by affinity, `GOMAXPROCS=2`, verified 2 GiB cgroup memory ceiling, COW preparation, two running slots and eight Tool slots. These are equal resource **limits**, not equal measured memory use. This was a shared Slurm node, not a dedicated machine. The base runtime revision was `f155341a`; this commit adds the measurement harness. Artifact and executable hashes are in the environment record.

For eight resident slots, medians across five process repeats:

| Synthetic wait | Inline / ExternalIO tasks/s | Paired throughput ratio | Inline / ExternalIO p95, ms | Inline / ExternalIO peak PSS, MiB |
| --- | --- | --- | --- | --- |
| 0 ms | 55.17 / 57.87 | 1.008× | 554 / 545 | 465.5 / 481.8 |
| 50 ms | 18.35 / 49.60 | 2.697× | 1,732 / 615 | 464.6 / 511.8 |
| 200 ms | 7.70 / 26.99 | 3.507× | 4,142 / 1,167 | 466.0 / 517.3 |

The ratio is the median of paired ratios, not the ratio of two medians. Zero-wait paired ratios ranged from 0.924× to 1.123×, so this sample does not establish a zero-wait benefit. The 200 ms condition used about 51 MiB more sampled peak PSS (roughly 11%) while completing the same batch about 3.5× as fast. P95 includes burst queueing, not just Python execution or Tool latency. PSS is process-wide and sampled, not per-Guest memory.

At 200 ms wait, increasing the resident limit while keeping two running slots gave:

- 2 residents: ExternalIO 7.68 tasks/s, paired ratio 0.991×, peak PSS 473.0 MiB.
- 4 residents: ExternalIO 14.91 tasks/s, paired ratio 1.928×, peak PSS 486.4 MiB.
- 8 residents: ExternalIO 26.99 tasks/s, paired ratio 3.507×, peak PSS 517.3 MiB.

**Decision:** retain explicit ExternalIO opt-in. Releasing a running slot helps when a Tool waits and resident capacity can admit other work. It has no demonstrated benefit for zero-delay Tools or when the resident limit already prevents additional Guests. This does not establish a maximum tenant count, an automatic residency threshold, a production network throughput claim, or a COW-versus-copy advantage. The experiment did not saturate the memory ceiling. Memory sampling itself has overhead in both arms.

Raw data and regeneration:

- [Environment](performance-data/linux-density/campaign-292688/environment.json)
- [50 raw process rows](performance-data/linux-density/campaign-292688/runs.jsonl)
- [Derived summary, including ranges](performance-data/linux-density/campaign-292688/summary.json)

```sh
# Inside a Linux allocation with at least two CPUs and a <=2 GiB cgroup memory cap:
go build -o /tmp/pysolate-density-bench ./cmd/pysolate-queue-bench
python3 tools/run-linux-density.py --binary /tmp/pysolate-density-bench \
  --guest dist/pysolate.wasm --output /tmp/pysolate-density-smoke --smoke --repeats 1
python3 tools/run-linux-density.py --binary /tmp/pysolate-density-bench \
  --guest dist/pysolate.wasm --output /tmp/pysolate-density-run --repeats 5
python3 tools/summarize-linux-density.py /tmp/pysolate-density-run
```

## Arms and invocation

Use the same artifact, host allocation, and task shape for both arms. The campaign invokes the queue benchmark with:

```text
-warmup 2 -case read-finish -mode executor -tasks 32
-active 2 -resident {2,4,8} -tool-active 8 -heap 8
-hold {0s,50ms,200ms} -cow=true -external-io {true,false}
```

Run five independent process repeats per condition, pairing the arms by alternating arm order. Use artifact SHA-256 `2c63ce407eae626c89fcd1dd918c713b1f8b75a35a6cf6ca6861466168d0faf8` unless the parent campaign records a replacement artifact explicitly. The Slurm allocation is `cpus-per-task=2`, `mem=2G`; the parent campaign verifies the cgroup hard cap. Record the exact artifact digest and host/cgroup provenance with each raw row.

`-warmup` defaults to `0`. With `-warmup 2`, two complete unmeasured Runs finish before measured Run IDs are created. Warm-up time and setup high-water are reported separately; measured memory peaks are reset immediately before measured batch execution. Warm-up does not create measured task IDs and is excluded from `batch_ns` and request latency rows.

The existing default `park-readmit` case remains unchanged when `-case` is not supplied. The existing `read-finish` case performs one tool dispatch per measured Run and finishes without approval parking.

## Timing boundary and accounting

- `setup_ns` covers runner preparation and warm-up. Measured task creation and executor construction follow this timer and are outside both `setup_ns` and `batch_ns`.
- `batch_ns` begins when measured submissions/resumes start and ends after all measured results are accounted for. Task creation is outside this interval.
- Each `request_ns` value is submit-to-completion and therefore includes executor queueing.
- `completed + errors == tasks` and `result_count == tasks` are required for each measured phase. `read-finish` additionally requires `tool_dispatches == tasks` (one dispatch per Run).
- The command fails on accounting or observed-bound violations. Task failures are counted in `errors`; the campaign summarizer also rejects any nonzero task-error count.

## Memory fields

On Linux, `/proc/self/smaps_rollup` is read at startup, before the measured batch, after the measured batch, and periodically at approximately 10 ms. Each snapshot reports `rss_kib`, `pss_kib`, and `private_dirty_kib` when available.

Rows include:

- `startup_memory`: startup snapshot.
- `sampled_setup_peak_memory`: sampled setup/warm-up peak before the measured reset.
- `pre_measured_batch_memory`: snapshot immediately before measured execution.
- `after_batch_memory` (read-finish) or `after_park_memory` (legacy park-readmit): post-phase snapshot.
- `sampled_peak_memory` and the scalar `sampled_peak_{rss,pss,private_dirty}_kib` fields: sampled peak after reset, with `sampled_peak_scope` identifying the measured interval.

A sampled peak is a periodic-observation peak, not an exact process high-water mark. A snapshot is not a peak. Missing `/proc` counters remain null rather than being treated as zero. The process lifetime/setup high-water is intentionally distinguished from the reset measured-batch peak.

## Provenance and limits

Rows retain the existing `peak_host_waits` and legacy RSS field. They also report `peak_executor_running`, `peak_executor_resident`, and `peak_executor_inflight_tools` when the existing Executor exposes those stats; the runtime scheduler is not changed by this benchmark. `cow` and `preparation` identify COW versus copy selection. `gomaxprocs` is the value returned by `runtime.GOMAXPROCS(0)` and `gomaxprocs_source` records that provenance; `num_cpu` is also emitted.

Do not combine setup and measured peaks, infer an exact peak between samples, or compare rows with different artifact, cgroup, CPU, concurrency, or lifecycle settings as if they were one statistic.
