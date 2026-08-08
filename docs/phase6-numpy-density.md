# Phase 6: NumPy-ready COW density and admission qualification

## Status

Accepted implementation and experiment plan. Results remain `profile-candidate` until every gate below passes on one exact Host revision and one checksum-verified Guest artifact.

## Question

Under fixed Host memory and CPU limits, how does a single-use `numpy-ready-v1` COW shard trade request latency, ready/active memory density, throughput, queueing, and rejection as offered load and per-request dirty working set change?

This phase does not test served-slot reuse, arbitrary packages, stateful sessions, real providers, production deployment, or arbitrary Runtime outcome equivalence.

## Fixed boundaries

- Runtime strategy: `cow-ready-single-use`; every served slot retires.
- Artifact profile: `numpy-core`; evidence class: `profile-candidate`.
- Artifact memory model: manifest-bound `cow-fixed`, with `initial_pages == maximum_pages` and `fixed: true`; a growable NumPy artifact is ineligible for this COW phase and is rejected before shard initialization.
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

- independent Host revision and Guest artifact-source revision, plus artifact/profile/warmup identities; each revision must match its own exact source or manifest. A frozen Guest artifact may intentionally predate the Host harness revision, so equality between these two revisions is not an acceptance condition and must not replace either binding;
- arrival mode, window, rate, queue capacity and offered/accepted/rejected totals;
- started/completed/failed and per-class totals;
- result-oracle version and validated-result count; NumPy uses `numpy-exact-v1` and requires one validated exact result per completion;
- up to 250,000 sorted raw request-latency samples; count, sum, mean, p50, p95, p99 and max are rederived during semantic validation;
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

### Controlled Slurm transport

- `tools/phase6_slurm_job.sh` is the source-bound worker entrypoint. It runs only on the low-cost `t4` partition with one T4, one node/task, four CPUs, and 16 GiB, starts from `--export=NIL`, exclusively claims and ownership-marks its node-local compute root, requires an exact path-safe and coverage-complete staged payload checksum set, verifies the staged payload, clones the exact clean Host bundle, and compares its Slurm script with the committed copy before invoking the matrix controller.
- `tools/phase6_slurm_watch.py` is the local controller. Every expected Host, tree, validator and Guest artifact identity is an explicit argument. It opens each remote archive/control file once with `O_NOFOLLOW`, validates the same file descriptor as a bounded regular file before streaming, applies a second local byte bound, rejects unsafe or duplicate archive members and duplicate JSON keys, requires the exact canonical cell/repetition/evidence set for the selected tier, verifies the worker environment and final requested/allocated Slurm resource shape, verifies every evidence checksum with the exact standalone Go validator, and only then publishes the archive-hash ACK.
- A Slurm job is accepted only when `READY` was validated, the hash-matching `ACKED` sentinel exists, and the exact Slurm controller record reports the requested job as `COMPLETED` with exit `0:0` on `t4`; `sacct` is also checked when that service is available. Scheduler completion alone is not evidence acceptance.

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
- The first request failure cancels in-flight request contexts, queued admission, burst release waits and active samplers before Runtime cleanup.
- Paper-facing points have at least three exact-source repetitions and report median plus min-max.

## Stop conditions

Stop and analyze once NumPy-ready static density, active dirty growth, open-loop saturation/rejection, recovery and three-knob policy behavior are supported. Do not expand into served-slot reuse, sessions, arbitrary PyPI/SciPy, real providers or production SLA work without a separate evidence review.
