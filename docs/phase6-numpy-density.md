# Phase 6: NumPy-ready COW density and admission qualification

## Status

Accepted implementation and experiment plan. Results remain `profile-candidate` until every gate below passes on one exact Host revision and one checksum-verified Guest artifact.

## Question

Under fixed Host memory and CPU limits, how does a single-use `numpy-ready-v1` COW shard trade request latency, ready/active memory density, throughput, queueing, and rejection as offered load and per-request dirty working set change?

This phase does not test served-slot reuse, arbitrary packages, stateful sessions, real providers, production deployment, or arbitrary Runtime outcome equivalence.

## Fixed boundaries

- Runtime strategy: `cow-ready-single-use`; every served slot retires.
- Artifact profile: `numpy-core`; evidence class: `profile-candidate`.
- Warmup: exact `numpy-ready-v1` profile and non-empty warmup generation digest.
- Host source, Guest source/artifact, manifest, workload version, machine, cgroup and effective policy are recorded.
- Results are compared only within the same workload, artifact/profile, machine class and source revision.
- `A` is CPython/WASI readiness, `B` is NumPy warmup, and `C` is one ready-slot request. `A+B` is not charged to each `C`.

## Workloads

### `numpy-v1`

Every request executes `np.arange(integer_work).sum()` through the NumPy-ready image. The Host verifies `prepared == 41`, a non-empty NumPy version, and the exact arithmetic sum.

### `numpy-mixed-v1`

A deterministic 20-request cycle:

- 12 NumPy tiny requests;
- 5 NumPy CPU requests;
- 2 NumPy 4 MiB dirty-array requests with a controlled hold;
- 1 NumPy 16 MiB dirty-array request with a controlled long hold.

The Host records started/completed/failed counts per class, verifies requested wait lower bounds, and verifies exact NumPy results. Dynamic allocated-block counts are not treated as prepared-image identity.

## Arrival modes

### `closed-loop`

Existing fixed consumers repeatedly issue work. Correlated burst remains a closed-loop consumer step and is never called open-loop.

### `open-loop-fixed-v1`

A deterministic arrival tape is generated before execution. Arrival index `i` has offset `floor(i * 1s / rate)`. A bounded queue feeds a fixed worker count.

Required conservation:

```text
offered = accepted + rejected
accepted = started
started = completed + failed
```

The offered request count is bounded and must equal the exact tape length derived from the recorded `window_ns` and rate. Accepted jobs receive contiguous execution IDs after admission, so rejected offers cannot create holes in the versioned workload cycle. Open-loop mode cannot also request a correlated consumer burst.

## Evidence

The `cow-pressure` schema v11 records:

- independent Host revision and Guest artifact-source revision, plus artifact/profile/warmup identities;
- arrival mode, window, rate, queue capacity and offered/accepted/rejected totals;
- started/completed/failed and per-class totals;
- result-oracle version and validated-result count; NumPy uses `numpy-exact-v1` and requires one validated exact result per completion;
- latency sum, p50 and p99;
- ready/leased/queued/refilling/retiring/waiting snapshots;
- process RSS/PSS, named COW mappings, active anonymous/private-dirty bytes, faults, cgroup memory/PSI and reclaim state;
- public policy inputs: maximum memory, maximum CPU and greed;
- JSON-safe effective policy telemetry.

The Host revision and Guest artifact-source revision belong to different repositories and are not required to be equal. Evidence binds each to its own exact source. Hard memory and CPU limits do not move with greed. CPU quota telemetry is not CPU enforcement unless the deployment layer applies and reads back the cgroup value.

## Execution ladder

1. Local RED/GREEN tests for schema, profile binding, NumPy response validation, arrival tape, conservation and invalid combinations.
2. Local full Go/race/vet/build and schema gates.
3. Signed source checkpoint and source-bound payload with SHA-256 manifest.
4. ICL `shell2` staging, then one `gpucluster2` canary with minimal slots/workers and short duration.
5. Small matrix: one repetition for selected ready-slot, worker/load and dirty-working-set points.
6. Formal matrix: only retained points, three exact-source repetitions each.
7. After every cell, rerun duplicate-key rejection, JSON Schema v11 and Go semantic validation with the same exact binary, then recheck the clean Host revision. Fetch evidence and verify job/app exits, sentinels, checksums and both source identities before analysis.

## Planned matrix

The canary chooses safe memory limits. The initial candidate matrix is:

- ready slots: 64, 256, and a budget-limited high point;
- workers: 1, 4, 8, 16 and 32, capped by effective policy;
- open-loop offered rates around the measured service knee;
- NumPy dirty working set: 0, 4 and 16 MiB;
- greed: 0, 50 and 100 only after one conservative policy row passes.

Unsupported or unsafe rows are recorded as rejected plan points, not fake passes. Failed/incomplete jobs remain evidence but do not enter result curves.

## Acceptance

- Zero hidden refill failures or open breaker.
- Exact prepared-image/profile identity throughout a run.
- Complete workload and arrival conservation.
- No timer returns earlier than its requested wait.
- Final target inventory restored after drain.
- Zero final active anonymous/private-dirty COW bytes.
- No unexplained OOM, timeout, response mismatch or artifact drift.
- Paper-facing points have at least three exact-source repetitions and report median plus min-max.

## Stop conditions

Stop and analyze once NumPy-ready static density, active dirty growth, open-loop saturation/rejection, recovery and three-knob policy behavior are supported. Do not expand into served-slot reuse, sessions, arbitrary PyPI/SciPy, real providers or production SLA work without a separate evidence review.
