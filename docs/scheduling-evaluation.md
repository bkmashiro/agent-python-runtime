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

Results are written below `docs/performance-data/scheduling-matrix/local/` unless `PYSOLATE_MATRIX_OUTPUT` selects another directory. Alongside the real phase and queue rows, the script writes `simulation.jsonl` for the mixed trace and `canonical-sweep.jsonl` for the controlled FIFO grid. The `local` directory is intentionally not a canonical checked-in result: record the machine envelope, source revision, artifact digest, and complete raw rows before promoting a run into a named evidence directory.

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

### Canonical controlled sweep

Use the canonical scenario to remove runtime and workload noise before estimating population behavior:

```sh
go run ./cmd/pysolate-schedule-sim \
  -scenario canonical \
  -tasks 2,8,32 \
  -cpu-before 1ms -cpu-after 1ms \
  -io-ratios 0.1,0.25,0.5,1,2,5,10,50,100 \
  -running 1,2,4 \
  -resident-multipliers 1,2,4,8 \
  -tools 1,2,4,8 \
  -policies fifo \
  > /tmp/pysolate-canonical.jsonl
```

Every task arrives at time zero and has exactly `CPU_before -> ExternalIO -> CPU_after`. The I/O ratio is `ExternalIO / (CPU_before + CPU_after)`. The two arms receive identical tasks: inline holds a running slot during I/O; live-I/O releases only that slot while the Guest continues to consume resident capacity. There is no Wasm construction, replay, page fault, GC, network jitter, random arrival, or measurement noise in this model.

The grid varies five independent controls: population, running slots, resident capacity as a multiple of running slots, Tool slots, and I/O ratio. JSONL emits one metadata row, one `point` per combination, and a `boundary` row per combination excluding I/O ratio. A boundary reports the first sampled ratio reaching 5% and 10% speedup and the sampled maximum. `-format csv` emits the points and boundaries as a rectangular CSV. The research helper rejects more than 10,000 tasks per point or 100,000 grid points; these are process-safety guards, not runtime limits.

The checked command above produces 1,296 points and 144 boundaries from the current deterministic simulator. Its useful sanity checks are:

- when resident capacity equals running capacity, speedup is exactly `1.0` for every sampled point because a waiting live Guest cannot admit another task;
- the largest sampled speedup is `7.864x` at 32 tasks, one running slot, eight resident slots, eight Tool slots, and I/O ratio 100;
- with 32 tasks and four running slots, one Tool slot caps the sampled maximum at `1.194x`; eight Tool slots permit `1.995x` with two resident multiples and `2.526x` with four or eight;
- homogeneous tasks keep resident byte-time close to parity at the maximum point (`1.004x`), while peak concurrent residency still rises. Real reconstruction, dirty-page, and queue costs must be measured separately.

Run [`09-canonical-scheduling-sweep.sh`](../demos/09-canonical-scheduling-sweep.sh) for a smaller presentation grid. Keep this sweep as an idealized sensitivity model. It must not be treated as measured service latency or wired into production policy.

## Measured calibration replay

The canonical sweep controls every variable but cannot show whether its phase assumptions match the real runtime. The calibration path turns real single-Run `pysolate-phase-bench` rows into deterministic `scheduling.Task` traces, then compares simulator makespan with small real Executor batches:

```sh
PYSOLATE_CALIBRATION_OUTPUT=/tmp/pysolate-calibration \
PYSOLATE_CALIBRATION_TASKS='2 4 8' \
PYSOLATE_CALIBRATION_REPEATS=3 \
./tools/run-calibrated-scheduling.sh
```

The script writes:

- `phase-common.jsonl`: measured read-finish, Tool-chain, local NumPy, and read-then-NumPy samples;
- `workload-*.json`: deterministic per-case tasks accepted directly by `pysolate-schedule-sim -input`;
- `observed-read-finish.jsonl`: real Inline and live-I/O Executor batches;
- `comparison.jsonl`: individual prediction errors plus summaries grouped by resource arm.

The adapter deliberately exposes its approximation instead of claiming unavailable precision:

1. Guest creation recorded by `runner.Create` is excluded because it happens before timed admission.
2. Tool capacity queue and continuation-resume waits are excluded from the phase trace because the simulator regenerates those waits from its resource queues.
3. Remaining local attempt time is split equally before, between, and after Tool calls.
4. Aggregate Tool service is split equally across observed Tool dispatches.
5. Samples are replayed in deterministic order when a larger population is requested.
6. Durable park/re-admit samples are rejected; they have a different lifecycle and must not be calibrated as live-I/O.

The equal split is a low-cost neutral heuristic. It is suitable for testing whether first-order running, resident, and Tool limits explain real batches. It does not recover the exact Python instruction boundary. The report records excluded nanoseconds and the split method as provenance.

`pysolate-calibrate` can also be used directly:

```sh
go run ./cmd/pysolate-calibrate \
  -phase phase.jsonl -case read-finish -tasks 8 \
  > workload.json

go run ./cmd/pysolate-schedule-sim \
  -input workload.json -running 2 -resident 8 -tools 4 \
  -policies fifo -live both

go run ./cmd/pysolate-calibrate \
  -phase phase.jsonl -case read-finish \
  -observed observed.jsonl -tolerance 0.15 \
  > comparison.jsonl
```

The default `0.15` tolerance is an engineering target for common controlled I/O cases, not a statistical guarantee or runtime contract. Use repeated observations and inspect each grouped summary. A miss means the model needs another measured cost term; it is not a reason to tune the scheduler against the simulator.

Run [`10-calibrated-scheduling.sh`](../demos/10-calibrated-scheduling.sh) for a short real 2/4-Run demonstration. The full calibration script defaults to three repeats at 2, 4, and 8 tasks. This remains bounded validation, not a high-density load test.

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
