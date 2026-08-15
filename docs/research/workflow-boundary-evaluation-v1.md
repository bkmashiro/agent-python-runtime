# Workflow-boundary evaluation v1

Status: **Measured real-Guest development trajectory; no latency or CPU-improvement claim**
Date: 2026-08-15

## Frozen identities

- Seed: `20260815`
- Guest and benchmark source: `d365ade0cbf2e9141ff798515011967022b1826b`
- Guest artifact: `sha256:26b1544bf35ef511b812c7162d72d384d8155a330a21bb038001c9594f44f474`
- Trajectory converter source: `eb89278fda9e801c9f44499daf87755129abfe4f`
- Portable evidence schema: `pysolate.workflow-benchmark-evidence.v1`
- Portable evidence file SHA-256: `sha256:8a30e3e0dd65ca47e43aa51819262a0b2d65e3cf3acfa0ceb436b7956f04dbac`
- Portable evidence internal seal: `sha256:8ba99c58c976194d9076a0bba4b12779c8d7ae9644593e8abeb1540d08935ba4`
- Checked-in private trajectory: `apps/lab-web/public/lab-data/experiment.json`

The portable evidence was retained as a private local input to the trajectory converter rather
than restored as a second checked-in Lab schema. The sealed trajectory materializes the
complete task metrics, model fixture records, tool calls/results and Runtime physical
executions from that evidence.

## Balanced paired result

The seeded task order contains 14 tasks and exactly seven pairs in each treatment order:

- `baseline_optimized`: 7;
- `optimized_baseline`: 7.

All task outputs and read-only effect ledgers matched. There were zero unclassifiable
divergences. Baseline performed 25 physical executions and optimized performed 23. Exact
in-flight coalescing and retained reuse each removed one physical execution; all eight
near-match dimensions remained separate and rejected.

## Wall and CPU observations

| Metric | Baseline | Optimized | Optimized − baseline |
|---|---:|---:|---:|
| Summed wall duration | 30.993831335 s | 30.999084752 s | +5.253417 ms |
| Process user+system CPU | 30.782849000 s | 30.802806000 s | +19.957000 ms (+0.0648%) |
| Physical executions | 25 | 23 | −2 |

The CPU metric is the process-wide `getrusage` user+system delta around each treatment. It
includes the Harness and every active goroutine, not Guest-only CPU. The paired task deltas
change sign across classes and controls; the 19.957 ms aggregate difference is small relative
to about 30.8 seconds of CPU and is not evidence that optimized execution costs more.
Conversely, this single frozen run does not demonstrate CPU savings from the two eliminated
read fixtures. Guest execution dominates the fixture, while the eliminated reads are not
CPU-heavy work.

The defensible v1 result is therefore:

> Under balanced seeded AB/BA treatment order, exact output/effect equivalence held and the
> qualified mechanisms reduced physical executions from 25 to 23. No wall-time or CPU-time
> improvement was measured.

## Trajectory coverage

The checked-in real-Guest trajectory contains 198 hash-chained events:

- 15 exact model requests and provider-labelled scripted reasoning/output records;
- 14 `workflowbench.execute_pair` tool calls and complete results;
- 76 Runtime events, including treatment roots and every physical execution;
- 14 explicit unchanged-workspace events;
- one final model request whose exact context contains every task result.

The scripted model records are labelled `scripted` and are not claims of a paid/live model
provider or hidden chain-of-thought. The Guest WASM and Runtime/tool timings are measured.

## Remaining gate

Before claiming representative performance, run multiple independent repetitions per task,
retain balanced treatment order, and report paired distributions. A future real Harness
adapter should record live provider events directly into the same trajectory contract rather
than reconstructing them after execution.
