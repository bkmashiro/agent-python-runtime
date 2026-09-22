# Deterministic computation and replay

## Current contract

Recorded execution is already deterministic for the inputs Pysolate controls:

- the Run pins source, JSON inputs, artifact digest, environment version, Tool
  snapshot and seed;
- WASI randomness is backed by a per-attempt ChaCha8 stream derived from the
  seed before CPython starts;
- wall and monotonic clocks are logical clocks; `sleep` advances them without
  waiting for real time;
- Python hash seeding, `os.urandom`, an unseeded `random.Random`, and an
  unseeded NumPy generator therefore reproduce for the same Run seed;
- Tool calls are matched by sequence, generated call identity, canonical
  capability identity and exact JSON arguments;
- a completed Tool outcome is persisted before Guest delivery and returned on
  replay instead of dispatching that operation again;
- a mismatch or an unresolved unsafe effect blocks recovery rather than
  guessing.

`recording_test.go` proves identical random, clock, hash-container and NumPy
results across separate OS processes. Durable tests prove completed Tool errors
and values replay without a second dispatch, and that prepared images preserve
the seed-specific RNG/clock state.

Run the compact conformance path:

```sh
./demos/15-deterministic-replay.sh
```

The script first prints a concrete replay tour: two executions with the same
seed produce identical randomness, logical time and result, while the Host Tool
dispatches once and its recorded outcome is consumed once. It then shows the
separate workspace boundary by having one disposable Guest write a file and a
second Guest read it from the same Host-owned workspace. This is workspace
continuation, not `RunRecorded` filesystem replay.

## What is intentionally not deterministic

The contract does not make the whole service schedule deterministic:

- admission order, queue delay, Host latency, timeout position and cancellation
  depend on real concurrency and load;
- an unresolved effect's outcome depends on its provider recovery contract;
- ordinary non-recorded Runs use real system clocks and entropy;
- writable workspace replay is not currently part of `RunRecorded`;
- changing source, inputs, artifact, environment, Tool version, call order or
  arguments creates a different execution or a replay divergence;
- floating-point results are covered for the pinned Wasm artifact and engine,
  not promised across arbitrary native implementations.

This separation is useful: deterministic *program-visible inputs and effects*
make recovery correct, while wall-clock scheduling measurements remain honest.

## Workload evidence

The first `agent-workloads@v1` run on the current macOS host and artifact
`sha256:d75b6f...3729` passed all 20 frozen cases. Observed values were:

- cold first setup: 2,648.17 ms;
- later per-case setup median: 144.61 ms;
- run median: 8.97 ms;
- run median excluding NumPy import: 7.35 ms;
- frozen Tool-case run median: 6.79 ms;
- NumPy import/aggregation case: 3,386.87 ms.

These are one local observation, not service SLOs. They do show that the next
performance question is repeated heavy-import reconstruction, while ordinary
Python and replayed Tool calls are already small enough that adding a complex
determinism mechanism would have little return.

## Highest-value next features

### 1. Read-only replay transcript

Expose an ordered, bounded Host API for one Run's Tool records: sequence,
capability/version, arguments, state, operation key, outcome class and whether
execution dispatched, replayed, looked up or waited. This would make divergence
and recovery auditable without opening SQLite directly. It should not include a
new hash protocol or become another source of truth.

### 2. Structured divergence details

Return a typed blocked reason with the sequence and which field differed
(call identity, capability or arguments). Keep raw payload inclusion opt-in so
errors do not accidentally leak sensitive Tool arguments. This is more useful
than the current generic `durable history mismatch` message.

### 3. Determinism coverage in the workload pack

Repeat a bounded subset with the same seed in separate processes and compare
exact JSON output and Tool transcript. Then rerun with another seed and require
only declared entropy-dependent fields to change. Keep load/admission timing out
of this oracle.

### 4. Investigate NumPy reconstruction only if common

The fixed pack observed a multi-second NumPy case while normal cases were
single-digit milliseconds. Before adding snapshots or package-specific policy,
measure repeated NumPy workloads in the actual hot service on Linux. If this is
frequent, a single simple full-code prepare path is a better candidate than
reviving prefix/PLM heuristics.

## Not recommended now

- instruction-level checkpoints;
- filesystem-tree hashing;
- trying to replay writable workspaces as an unordered lookup table;
- automatic inference of Tool retry safety;
- deterministic scheduling or timeout simulation in production;
- additional prefix/PLM branches for replay.

The current replay boundary is strong because it stays small: deterministic
Guest inputs plus explicit Host effects. The next implementation should improve
visibility into that boundary before broadening it.
