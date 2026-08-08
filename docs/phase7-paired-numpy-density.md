# Phase 7: paired NumPy-ready COW density

## Experimental question

For the same manifest-bound `numpy-core` guest artifact and the same guest-defined `numpy-ready-v1` warmup, how much process PSS, private-dirty memory, and ready-build time does `cow-ready-single-use` save relative to independent `single-use-preinitialized` slots as ready capacity increases?

This phase is an experiment. It does not change the Phase 6 request-latency corpus or its conclusions.

## Arms

| Arm | Active Runtime strategy | Warmup lifecycle | Runtime topology |
|---|---|---|---|
| COW | `cow-ready-single-use` | one canonical instance runs `runtime_warmup`; every ready slot restores that sealed image | one Runtime shard per requested capacity |
| non-COW | `single-use-preinitialized` | every independently instantiated slot runs the same `runtime_warmup` before publication | existing hard bound of four slots per Runtime shard |

The topology difference is recorded in every sample. It is part of the supported system comparison and must not be hidden in a per-slot ratio.

## Canonical matrix

- artifact profile: `numpy-core`
- warmup: `numpy-ready-v1`
- slots: `1, 2, 4, 8, 16, 32, 64`
- fresh OS child process per `(slots, repeat)`
- canary: one repeat per arm
- formal: three repeats per arm
- T4 partition only; no A100
- 4 CPU
- 8 GiB process RSS guard
- 2 GiB policy reserve
- greed 50

A sample is accepted only when the pool reaches its exact ready target and all inventory counters conserve supply. The parent kills a child that exceeds its RSS guard or timeout; a killed child is not converted into a successful measurement.

## Lifecycle separation

The evidence records these phases independently:

- host instantiation and compilation;
- guest instantiation;
- `_initialize`;
- `runtime_init`;
- artifact-defined `runtime_warmup`;
- COW restore, where applicable;
- total ready-build wall time.

Non-COW warmup is inserted only in prepared-slot creation. A cold pool miss remains a fresh fallback and does not inherit benchmark warmup semantics.

## Memory measurements

The child records process-level `/proc` measurements after all requested slots are ready:

- RSS;
- PSS;
- private clean and private dirty;
- swap;
- page faults;
- VMA and FD counts.

The COW arm also records named COW mapping attribution. Shared Slurm cgroup counters remain unavailable unless isolation is independently established; cumulative job cgroup peaks are not assigned to a cell.

## Evidence contract

Lifecycle-density schema v2 binds:

- exact artifact SHA-256, size, source commit, target, execution model, and profile;
- `numpy-ready-v1` plus `SHA256(artifact_bytes || 0x00 || profile)` generation;
- requested and active strategy with no fallback;
- exact canonical matrix and runtime-shard topology;
- process, backend, environment, phase, pool, and COW mapping identities;
- duplicate-key rejection and unknown-field rejection.

`tools/phase7_density.py` first invokes the standalone Go validator for each arm and then rejects any cross-arm drift in artifact, warmup, Host source, backend, environment, plan, metric semantics, or observability. Derived ratios use integer parts-per-million to keep rendering deterministic.

The Slurm wrapper accepts only the exact bounded payload file set, copies every source through a one-descriptor size/hash gate, and binds the scheduler's executing script, the checked-out source wrapper, and the running benchmark binary's embedded VCS identity to the requested source commit. Archive, checksum, `READY`, failure, and `ACKED` files use exclusive hard-link publication. ACK input is one bounded regular file read through one `O_NOFOLLOW` descriptor and must equal exactly `<archive-sha256>\n`. Slurm stdout is outside `stage/input`, so scheduler-created log files cannot change the payload inventory. With a four-minute child timeout, 42 formal child cells plus the 30-minute ACK window remain below the four-hour allocation limit.

## Canary commands

```bash
./apyrun-benchmark \
  -kind lifecycle-density \
  -class profile-candidate \
  -strategy cow-ready-single-use \
  -prepared-warmup-profile numpy-ready-v1 \
  -artifact "$ARTIFACT" -manifest "$MANIFEST" \
  -samples 1 -max-rss-bytes 8589934592 -child-timeout 4m \
  -output cow.json

./apyrun-benchmark \
  -kind lifecycle-density \
  -class profile-candidate \
  -strategy single-use-preinitialized \
  -prepared-warmup-profile numpy-ready-v1 \
  -artifact "$ARTIFACT" -manifest "$MANIFEST" \
  -samples 1 -max-rss-bytes 8589934592 -child-timeout 4m \
  -output non-cow.json

python3 tools/phase7_density.py \
  --benchmark ./apyrun-benchmark \
  --schema benchmark/v1/lifecycle-density.schema.json \
  --artifact "$ARTIFACT" --manifest "$MANIFEST" \
  --cow cow.json --non-cow non-cow.json \
  --output paired.json
```

For formal evidence, use `-samples 3`. Run a second independent allocation with reversed arm order before treating a difference as stable across allocation time. The versioned `tools/phase7_slurm_job.sh` wrapper runs both arms inside one allocation, records `cow-first|non-cow-first`, validates each arm independently, and only then renders the paired summary.

## Acceptance gates

- both arm files pass `validate-lifecycle-density` against the checked-in schema, staged artifact, and manifest;
- both files are schema v2 and independently pass structural and semantic validation;
- artifact, warmup generation, Host source, backend, environment, and plan are byte-equivalent across arms;
- every canonical `(slots, repeat)` cell exists exactly once;
- COW reports one Runtime shard and exactly `slots` named COW mappings;
- non-COW reports `ceil(slots / 4)` Runtime shards and no COW mapping attribution;
- pool ready/accounted values equal requested slots;
- no timeout, RSS guard kill, OOM, OOM-kill, or fallback;
- raw evidence hashes are embedded in the paired summary.
