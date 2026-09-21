# Scheduling evaluation

This layer measures explicit runtime boundaries and evaluates bounded policies without predicting arbitrary Python behavior. It has three parts:

1. durable Tool observations from real executions;
2. a reproducible Linux-oriented phase matrix;
3. a deterministic discrete-event simulator that consumes fixed phase traces.

None of these components changes durable replay, effect recovery, or Guest semantics.

## Tool observations

A durable `Tool` may receive a Host-local `Observer`:

```go
stats, err := durable.NewToolLatencyStats(0.2)
if err != nil {
    return err
}

tool := durable.Tool{
    Name:       "catalog.read",
    Version:    "v1",
    Recovery:   durable.RetrySafe,
    Scheduling: durable.ExternalIO,
    Observer:   stats,
    Call:       readCatalog,
}
```

Each `ToolObservation` separates:

- wait for `MaxInflightTools` capacity;
- Host callback service time;
- wait to reacquire a running slot;
- call versus recovery lookup;
- success, ordinary failure, cancellation, and deadline;
- canonical tool name, version, operation key, and argument-size bucket.

`ToolLatencyStats.Snapshot` returns deterministic mean and EWMA estimates keyed by tool name, version, operation, scheduling class, and one of four bounded payload buckets. A fixed manifest therefore creates a bounded number of entries. The observer runs synchronously and must remain cheap; production exporters should hand observations to their own bounded non-blocking queue.

Scheduling and observation remain Host-local configuration. Neither is written into the durable Tool declaration snapshot, so changing telemetry does not invalidate existing Runs.

## Real phase workloads

`cmd/pysolate-phase-bench` executes fixed source through the real Wasm Guest. The current cases are:

- `read-finish`: one Host read followed by a small suffix;
- `tool-chain`: two dependent Host reads;
- `park-readmit`: a Host read followed by durable approval and replay;
- `numpy-local`: local computation with no Tool boundary;
- `read-numpy`: a Host read followed by bounded NumPy work.

The JSONL samples now report Tool capacity queue, Host service, and continuation resume time separately. `-external-io` switches only the synthetic read declarations; the source and controlled delays remain fixed.

## Linux phase matrix

Run the full matrix on Linux:

```sh
./tools/run-scheduling-matrix.sh
```

Defaults:

- Tool delays: `5ms`, `50ms`, `200ms`, `1s`;
- private heap fixtures: `0`, `8`, `64` MiB;
- preparation: copy and Linux COW;
- Inline and `ExternalIO` arms;
- three phase samples per case;
- two competing Runs with one running, two resident, and two external-tool slots.

Results are written below `docs/performance-data/scheduling-matrix/local/` unless `PYSOLATE_MATRIX_OUTPUT` selects another directory. The `local` directory is intentionally not a canonical checked-in result: record the machine envelope, source revision, artifact digest, and complete raw rows before promoting a run into a named evidence directory.

A small portable smoke is:

```sh
PYSOLATE_MATRIX_OUTPUT=/tmp/pysolate-matrix \
PYSOLATE_MATRIX_ITERATIONS=1 \
PYSOLATE_MATRIX_DELAYS='5ms' \
PYSOLATE_MATRIX_HEAPS='0' \
PYSOLATE_MATRIX_PREPARATIONS='copy' \
./tools/run-scheduling-matrix.sh
```

On non-Linux systems the queue arm defaults to copy. COW requests fail explicitly rather than falling back. The script accepts `PYSOLATE_MATRIX_CASE`, `PYSOLATE_MATRIX_DELAYS`, `PYSOLATE_MATRIX_HEAPS`, `PYSOLATE_MATRIX_PREPARATIONS`, `PYSOLATE_MATRIX_ITERATIONS`, `PYSOLATE_MATRIX_COW`, and `PYSOLATE_MATRIX_OUTPUT` as bounded experiment controls.

A checked-in WSL quick run at [`performance-data/scheduling-matrix/wsl-quick-0df54dc1/`](performance-data/scheduling-matrix/wsl-quick-0df54dc1/) validates both copy and Linux COW paths at a 50 ms Tool delay. In the two-Run fixture, `ExternalIO` reduced the observed batch from 172.61 to 107.32 ms with a 0 MiB private heap, and from 169.04 to 110.92 ms with an 8 MiB heap. These are one-process queue samples. The sampled RSS deltas changed sign between heap fixtures, so the run supports no memory threshold; the directory README records the exact source, artifact, host, raw rows, and limitations.

## Offline policy simulator

Run the committed common-agent trace:

```sh
./demos/08-scheduling-simulation.sh
```

or supply a strict JSON array of `scheduling.Task` records:

```sh
go run ./cmd/pysolate-schedule-sim \
  -input workload.json \
  -running 2 -resident 8 -tools 4 \
  -policies fifo,ready_first,finish_soon \
  -live both
```

A task contains an arrival time, estimated resident bytes, and explicit `cpu`, `external_io`, and `durable_wait` phases. Durations are observations or controlled inputs. The simulator does not infer them from source, sample random arrivals, preempt running Python, or change effect legality.

Policies are deliberately small:

- `fifo`: oldest runnable phase first;
- `ready_first`: prefer a continuation over a new admission;
- `finish_soon`: prefer the smallest known remaining phase sum, with priority only as a tie-breaker.

The output includes completion count, rejection count, mean and p95 latency, makespan, peak resource counts, running/tool slot-time, and resident MiB-seconds. `MaxQueued=0` means unlimited only in this research model; the production Executor retains its strict queue semantics.

### Built-in pilot

The built-in trace contains twelve deterministic instances of read-finish, dependent Tool calls, read-then-compute, and durable wait. On macOS arm64 with the current simulator source, two running slots, eight resident slots, and four Tool slots produced:

- inline FIFO: **903 ms** makespan, **484.83 ms** mean latency, **20.736 MiB-s** resident area;
- live-I/O FIFO: **504 ms** makespan, **263.42 ms** mean latency, **30.216 MiB-s** resident area;
- live-I/O ready-first: the same result on this trace;
- live-I/O finish-soon: **522 ms** makespan and **269.50 ms** mean latency.

These numbers are model output, not measured runtime performance. They expose the next decision clearly: live-I/O can reduce running-slot contention while increasing concurrent residency. The more complicated ordering policies did not improve this particular trace, so they should not enter the production Executor on this evidence.

## Interpretation boundaries

Use the real matrix to estimate phase distributions and memory costs, then use the simulator to test population assumptions. Do not feed simulator output back as if it were an observed latency distribution.

The first useful production policy remains:

- explicit `ExternalIO` only for known blocking Host operations;
- independent running, resident, and Tool bounds;
- bounded preference for result-ready continuations;
- durable park only at legal replay boundaries;
- ordinary bounded execution for black-box Python.

Consider finish-soon ordering or pressure eviction only if named real workloads show lower tail latency or resident memory-time after reconstruction and replay costs are included.
