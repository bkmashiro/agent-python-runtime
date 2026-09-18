# Wait-time memory residency

Pysolate can advise Linux about a Guest's linear memory while that Guest is
blocked on a Host tool call. The Guest, Python stack and Host resources stay
alive. Execution continues in the same instance when the call returns.

This is an opt-in policy on the existing Linux COW path. The normal default
remains unchanged.

## Policies

- `natural`: wait without memory advice; let the OS choose what to reclaim.
- `fixed`: issue `MADV_COLD` after `ColdAfter`, then optionally
  `MADV_PAGEOUT` after `PageOutAfter`.
- `pressure`: after the minimum wait, check cgroup memory-budget occupancy.
  Issue the same advice only when an applicable ancestor reaches the selected
  fraction of its finite `memory.high` or `memory.max`.

```go
config.Mechanisms = runtime.MechanismSet{
    PreparedRuntime: true,
    MemoryCOW: true,
    ColdIOContinuation: true,
}
config.ColdIO = &runtime.ColdIOPolicy{
    Strategy:          runtime.ColdIOPressure,
    ColdAfter:         100 * time.Millisecond,
    PageOutAfter:      300 * time.Millisecond,
    PressureThreshold: 0.8,
}
```

These numbers are example policy choices. Pressure sampling runs no faster than
`max(ColdAfter, 50ms)`. Decisions occur at sampling ticks, so the age thresholds
are minimum waits. Unknown limits or a probe error suppress advice for that
wait and are recorded. There is no fallback to whole-machine memory pressure.
`Strategy` is explicit; existing fixed-policy callers must set `ColdIOFixed`.

Ordinary tool calls and PLM's blocking linearize/materialize step use the same
wait helper. Requests are copied before asynchronous Host work starts. A late
result cannot write back to a freed Guest or resume a cancelled run. Host
handlers still need to honour their context: an operation already dispatched
may finish and be recorded after cancellation. Cancelling a run does not undo
external effects.

Advice counters report accepted syscalls. They do not measure reclaimed RAM.
Swap configuration, backing storage and kernel pressure affect reclamation.

## Small benchmark

`cmd/residency-bench` runs a real NumPy program:

```text
allocate and compute over an int64 array
→ wait on testio.wait()
→ read/sum the complete array
→ check the exact result
```

Each randomized block has one trial for each policy, run serially in separate
worker processes. A bounded anonymous-mmap pressure companion, when requested,
is identical across policies. It stays active through the measured run and is
stopped, joined and unmapped at cleanup. Its size is below 256 MiB by design.

On Linux with a qualified Guest bundle:

```sh
go build -o /tmp/residency-bench ./cmd/residency-bench
/tmp/residency-bench -guest dist/numpy-core/agent-python-runtime-numpy-core.wasm \
  -output /tmp/residency.jsonl -blocks 5 -seed 20260919 \
  -workloadMiB 64 -waitMs 2000 -cold 100ms -pageout 300ms
python3 scripts/analyze-residency.py /tmp/residency.jsonl \
  --output-dir /tmp/residency-summary
```

The cgroup limit is provided by the execution environment. The command does not
change memory limits, swap or global reclaim settings. `-pressureMiB 224` adds
the same synthetic companion to every policy. It is a workload condition,
not part of the pressure policy implementation.

### Measurement boundaries

- `setup_ns`: artifact admission, engine construction and common warmup.
- `run_ns`: the measured `Engine.Run`, including its internal Guest cleanup.
- `wake_to_result_ns`: Host wait release to Run return, including continuation
  computation. This is not pure page-in latency.
- `cleanup_ns`: companion teardown and engine close.
- `end_to_end_ns`: the complete worker execution through cleanup.

Memory samples are taken at wait entry, every 50ms and just before successful
wait release. These samples cannot mistake Guest destruction for reclamation.
Process PSS/RSS and cgroup memory/swap are recorded separately. Cgroup totals
include the worker, harness parent, companion and charged cache, not just Guest
pages. The parent releases unused artifact heap before scheduling trials.

The analyzer reports time-weighted wait memory, sampled peaks, medians, observed
ranges and paired block differences. Missing measurements remain missing.
Failed, crashed and timed-out workers retain rows and cause a nonzero command
exit. Five trials per policy are a small comparison, not a tail-latency estimate.

This workload measures waiting residency and the cost of returning a result.
It does not establish multi-Run capacity or throughput, durable suspension, or
memory residency after continuation. A benefit needs lower total resource cost
at an acceptable latency; a fall in process PSS alone is insufficient.
