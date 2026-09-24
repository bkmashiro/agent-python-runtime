# Early-read validation: Linux COW and HTTP cancellation

This follow-up adds tests and evidence only. Runtime code, public interfaces,
permissions, defaults and the existing experimental policy are unchanged.

## Linux campaign

The existing `pysolate-contention-bench` and study script were built from
`52aacd68be73de6560fd1e9336dcbe3c6f048060`. Both COW modes ran on the same
Linux/x86-64 Slurm allocation. The recorded process affinity was logical CPUs
`0,36`, with `GOMAXPROCS=2`. Slurm requested two CPUs and 2,048 MiB. Physical-core
topology, effective memory enforcement and peak RSS/PSS were not independently
verified. In particular, the allocation must not be described as two dedicated
physical cores. The accounting database was unavailable during collection.

Each mode ran three arms × three offered intervals × three fresh-process
repetitions, 60 scheduled requests per process. Default COW ran first, then COW
with data-image enabled. Arm order rotated within each mode; the modes themselves
were not interleaved. Compare arms within each mode, and do not pool these rows
with the earlier macOS pilot or attribute cross-mode differences solely to the
memory implementation.

Controls remained eight admitted requests, two synthetic backend service slots,
50 ms service time and a 400 ms deadline from scheduled arrival. Workloads and
oracles were unchanged. These are runtime-API results with an in-process synthetic
tool, **not HTTP throughput measurements**.

### Correct, on-time completions per 60 scheduled requests

Default COW ([all process summaries](performance-data/early-read-contention/linux-cow.json)):

| Offered interval | Sequential | Always early | Spare-capacity |
| --- | --- | --- | --- |
| 100 ms | 60, 60, 60 | 60, 60, 60 | 60, 60, 60 |
| 40 ms | 60, 60, 60 | 57, 58, 57 | 60, 60, 60 |
| 20 ms | 28, 28, 28 | 24, 26, 25 | 27, 28, 30 |

COW data-image ([all process summaries](performance-data/early-read-contention/linux-data-image.json)):

| Offered interval | Sequential | Always early | Spare-capacity |
| --- | --- | --- | --- |
| 100 ms | 60, 60, 60 | 60, 60, 60 | 60, 60, 60 |
| 40 ms | 60, 60, 60 | 58, 58, 60 | 60, 60, 60 |
| 20 ms | 27, 28, 26 | 29, 27, 28 | 31, 31, 30 |

Across 54 processes and 3,240 scheduled requests: 2,649 completed correctly and
on time, 95 timed out, and 496 were rejected. No execution failure, incorrect
answer/call count, orphan tool event or residual active backend call was observed.
Every trace passed the existing independent accounting validator. The largest
per-process arrival-lag p95 was 14.93 ms for default COW and 1.50 ms for data-image;
lag remains part of request latency/deadline accounting.

### A limitation visible in short requests

In the data-image low-load cell, the median of per-process p95 values was:

- Independent two-read tasks: 110.94 ms sequential, 93.39 ms always-early,
  92.87 ms spare-capacity.
- Single-read short tasks: 58.67 ms sequential, 90.69 ms always-early,
  90.51 ms spare-capacity.

The simple occupancy rule therefore does not preserve every workload's latency.
For the short tasks, [recorded boundary measurements](performance-data/early-read-contention/linux-short-boundaries.json)
locate the extra time **before the first Host callback**. The median of three
per-process median scheduled-arrival-to-callback intervals was 6.50 ms sequential
and about 37.0 ms for the two early-read modes. Median backend queue time stayed
below 0.04 ms, and synthetic service remained about 50 ms.

Boundary derivation: for each measured `short` request, subtract its
`scheduled_ns` from the matching `tool_queued.at_ns`, then take the within-process
median. Tool queue/service values come from the corresponding `tool_end` record.
This localizes the overhead but is not a CPU profile or proof of one particular
parser/import hotspot. A single-read task has no second read to overlap. No
source-analysis shortcut, caching change or policy adjustment was added to hide
this result.

## Independent HTTP process cancellation test

`cmd/pysolate-contention-bench/http_backend_test.go` launches a separate server
process on loopback and executes a real Guest whose Host tool performs an HTTP
request. A server-side start barrier proves the request reached the backend
before the test cancels the execution or lets its deadline expire. Server state
is read independently over a control connection.

Four cases passed, including a race-enabled run:

- Cooperative server + manual cancellation: Guest returns an error; backend
  eventually records one cancellation and zero completed operations.
- Cooperative server + deadline: same eventual backend outcome.
- Server ignoring cancellation + manual cancellation: after Guest return, the
  server still reports one active operation. A test-only release barrier lets it
  finish; the server then records one completion.
- Server ignoring cancellation + deadline: the same continued-work observation.

[Reviewed outcomes](performance-data/early-read-contention/http-cancellation.json)
include missing observations as `null`: the cooperative cases wait for eventual
settlement and do not assert that the server was already idle at Guest return.
Client/server process identities were checked and all four server processes
exited cleanly. Full request arguments, client errors, Guest output/raw I/O and
server events remain in private local traces.

The release barrier makes the cancellation distinction deterministic. It is not
a model of representative remote service time, a production API integration, or
an HTTP load benchmark. A server-side completion/write attempt does not imply
that the cancelled client received its result.

This test demonstrates why returning from a context-aware Go HTTP callback is
insufficient evidence that remote work stopped. A Host occupancy counter based
only on live client callbacks can underestimate actual backend work after
cancellation. The synthetic benchmark's slot count measures its own backend
accurately; replacing that signal with arbitrary HTTP-client activity would
change the policy's assumptions.

## Reproduction and checks

Reuse the commands in [the contention guide](early-read-contention.md), adding
`--prepare cow`, then optionally `--cow-data-image`, with a fresh output directory
for each cohort. Run on an allocated compute node, not a cluster login node.

The independent HTTP test is explicitly opted in and requires an existing Guest:

```sh
PYSOLATE_HTTP_ACCEPTANCE=1 \
PYSOLATE_HTTP_RECORD_DIR=/private/new-http-cancellation-run \
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" \
go test -race ./cmd/pysolate-contention-bench \
  -run '^TestHTTPBackendCancellationBoundary$' -count=1 -v

GOMAXPROCS=2 GOFLAGS=-p=2 make check
```

Both commands passed locally. The explicit HTTP acceptance run executes all four
cases; a default package test intentionally skips the external-process fixture.
`make check` verified the artifact, all Go package tests, 10 Guest Python tests,
22 tools tests and `go vet ./...`. This is a targeted race result, not a claim
that the entire repository passed under the race detector.

**Decision:** keep the existing policy experimental. Linux confirms useful pair
latency reductions and workload-dependent contention costs, while also exposing
a short-task regression. HTTP confirms the need to distinguish client lifetime
from remote work lifetime. No production feature or automatic policy was added.
