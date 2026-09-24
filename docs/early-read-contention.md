# Early reads under shared tool contention

This bounded pilot asks whether early reads improve logical task completion when
many real Python Guests share a limited tool backend. It adds no runtime policy,
permissions, dependencies or model calls.

## Contract

`cmd/pysolate-contention-bench` uses `Runner.Run` and `RunWithEarlyReads` on
identical complete sources and inputs. The registered `read(value=...)` tool
returns a stable integer after a synthetic service delay. A shared semaphore
limits tool service capacity. This is a real Guest / runtime API experiment with
synthetic Host I/O, not HTTP, a remote service or a production load claim.

The default cyclic mix is:

- `pair`: two independent reads, then addition;
- `short`: one read;
- `dependent`: the second read's argument depends on the first result.

Each program has an independently specified expected answer and call count.
These are frozen programs, not a new model-generated cohort. Preparation and
one sequential warm-up per workload are excluded from the measured window but
recorded. Copy preparation is portable; COW and the optional data image require
their existing platform/artifact contracts.

Arrivals have fixed scheduled times, independent of previous completions. At the
request cap, new arrivals are rejected rather than building an unbounded queue.
Deadlines start at **scheduled arrival**, so dispatch lag is not removed from the
latency budget. Tool calls can wait for the bounded backend within this deadline.
All scheduled IDs receive a result: correct/on-time, timeout, rejected, cancelled,
execution failure or incorrect result. Interruption preserves partial records.

## Run

```sh
go build -o /tmp/pysolate-contention-bench ./cmd/pysolate-contention-bench
GOMAXPROCS=2 /tmp/pysolate-contention-bench \
  -arm early -requests 60 -interval 40ms -timeout 400ms \
  -inflight 8 -tool-capacity 2 -tool-delay 50ms \
  -out /private/new-early-trace.jsonl
```

Use an existing private parent directory. The output file is created with mode
0600 and exclusive creation, never overwritten. `-arm sequential` changes only
the execution mode. `-workloads pair` isolates the independent-read case; repeated
names in a mix give explicit deterministic weights.

The small paired campaign creates a **new** private directory, runs fresh
processes, alternates arm order, and validates complete traces before accepting
a summary:

```sh
python3 tools/run-contention-study.py \
  --binary /tmp/pysolate-contention-bench --guest dist/pysolate.wasm \
  --out /private/new-contention-study \
  --requests 60 --repeats 3 --interval-ms 100,40,20 --timeout-ms 400
```

Use `--arms sequential,early,spare-capacity` to include a minimal host-side
policy. Just before Guest creation it samples occupied backend slots: a full
backend selects `Run`, otherwise it selects `RunWithEarlyReads`. This does not
reserve capacity or predict occupancy when the Guest eventually issues a call.
It is an experimental benchmark arm, not a new runtime default or public API.
All arms record their mode decision and the observed occupancy.

These intervals offer nominally 10, 25 and 50 requests/s. At 50 ms per call and
two backend slots, the ideal tool service ceiling is 40 calls/s. With the evenly
weighted default mix averaging 5/3 calls per successful request, 24 requests/s
is an **idealized workload calculation**, not a measured service capacity.
Runtime, tracing and scheduling overhead lower the realizable capacity.

## Read the results

- `counts`: every scheduled request, including rejected and timed-out requests.
- `ok_p50_ns` / `ok_p95_ns`: nearest-rank latency **conditional on correct,
  on-time completion**. A smaller value with more failures is not automatically
  better. No observations means null, not zero.
- `by_work`: outcomes and conditional latency for each workload. A global median
  can hide harm to short requests.
- `on_time_per_second`: correct/on-time tasks divided by the full finite measured
  window, including drain. It is not a steady-state throughput estimate.
- `arrival_lag_p95_ns`: how late the driver actually handled scheduled arrivals.
- `calls` / backend `attempts`: Host callback invocations, including calls cancelled
  in the tool queue. Backend `acquired` counts calls that got a service slot.
- Backend queue timings include wrapper/trace work before acquiring the slot;
  service timings include instrumentation and scheduling around the synthetic
  delay. Peak and final active counts check the service capacity and cleanup.

The independent repetition unit is a process. Requests inside one process are
not independent repeated experiments. Do not pool low-load, overload, copy and
COW measurements into one headline speedup. The small pilot does not estimate a
production success probability or memory density.

## Complete local traces

The header retains config, exact sources/oracles, inputs, Guest hash and build
information. Request records retain admission, deadlines, results, transformed
source, errors and raw Guest stdout/stderr. Tool records retain request/call IDs,
arguments, queue/start/completion events, values and errors. Warm-ups remain in
the trace with negative request IDs. A missing footer is a partial recording.

Trace writes are synchronous JSONL and their overhead is included in both arms.
The file is synced on close, not at every event. A machine crash can lose an
unsynced tail. Write failure cancels the campaign instead of silently continuing.
No credentials or environment dump are recorded. Raw traces remain local;
reviewed aggregate observations may be published separately.

`run-contention-study.py` verifies request coverage, result oracles, tool event
pairing, argument consistency, call totals, capacity and final cleanup. This is
**trace validation**, not deterministic timing replay. The ordinary early-read
API is separate from `RunRecorded`; offline playback cannot reproduce observed
wall-clock contention. Re-running this benchmark is a new measurement.

## Development checks

```sh
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" go test ./cmd/pysolate-contention-bench
python3 -m unittest tools/test_contention_study.py
```

Tests cover accounting, expired arrivals, admission rejection, cancelled planned
arrivals, backend limits, queue cancellation, trace failure and actual Guest
execution in both arms. Race checks exercise the same bounded helper path.

## Local pilot result and decision

The [reviewed process summaries](performance-data/early-read-contention/policy-pilot.json)
contain 27 fresh processes: three arms × three arrival intervals × three
independent repetitions, with 60 scheduled requests per process. This was
macOS/arm64, Go 1.26.0, copy preparation and `GOMAXPROCS=2` on a 10-logical-CPU,
16 GiB host. The process was not CPU-pinned or given an OS memory limit. No other
benchmark/test process was deliberately run concurrently. This is not the Linux
COW experiment, and it provides no memory-density measurement.

All arms used eight request slots, two tool-service slots, 50 ms synthetic service
and a 400 ms request deadline. Each repetition's correct/on-time completion count:

| Offered interval | Sequential | Always early | Spare-capacity |
| --- | --- | --- | --- |
| 100 ms | 60, 60, 60 | 60, 60, 60 | 60, 60, 60 |
| 40 ms | 59, 59, 59 | 57, 58, 56 | 59, 60, 60 |
| 20 ms | 30, 30, 28 | 27, 27, 27 | 29, 30, 29 |

At low load, the median of the three per-process `pair` p95 values was 140.33 ms
for sequential, 103.26 ms for always-early and 103.22 ms for spare-capacity.
All requests completed on time in this load cell. At 40 ms arrivals, always-early
still shortened the successful pair tasks, but short-task conditional p95 rose
from 195.67 ms to 236.88 ms and total on-time completions fell. Spare-capacity's
short-task conditional p95 was 199.62 ms. These are process-level summaries, not
percentiles pooled across independent runs.

Spare-capacity selected early execution for 57–59 of 60 low-load requests per
process. Near capacity it selected early execution only once per process;
under overload, only two or three times. It mostly recovers ordinary execution
under pressure. It does **not** demonstrate higher saturated capacity than the
sequential baseline: their overloaded completion counts overlap, and the two
near-capacity extra completions across three processes are too small to claim a
new throughput improvement.

Across all 1,620 scheduled requests, 1,324 completed correctly and on time, 52
timed out and 244 were rejected. No execution failure or incorrect result/call
count was observed. Every tool event was paired, every backend ended with zero
active calls, and the largest per-process arrival-lag p95 was 2.11 ms. All raw
traces and the earlier two-arm exploratory cohort remain private; the cohorts
are not pooled. The published JSON retains source/artifact identity and all
per-process aggregate outcomes, without raw private traces.

**Decision:** retain the tiny host-side policy as an experimental comparison arm.
It preserves the observed low-load latency benefit and avoids most of the loss
from always enabling early reads in this fixed mix. Do not promote it to a
runtime default or add a public scheduling API. Different task mixes, actual
backend signals and Linux execution need separate evidence before production
use. This result also does not justify a new eviction/replay scheduler.
