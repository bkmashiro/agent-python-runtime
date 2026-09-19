# Durable replay verification

The bounded implementation recovered the same logical workflow after process kills and VM hard stops. This record is a correctness campaign, not a latency or throughput benchmark.

## Scenario and observed outcomes

The real CPython/Wasm Guest executes:

```text
provider read → deterministic calculation → approval wait → idempotent write
```

The provider is a separate SQLite fixture with independent transactions. Its declared idempotency key is tested explicitly; this does not infer or guarantee idempotency for another provider.

Two interruption windows were exercised in each environment:

1. The wait and prior read were committed; the process stayed alive with its database open. Kill/HardStop, reopen, and deliver approval.
2. The provider fixture committed its write, but its handler had not returned and the Run journal still had a pending call. Kill/HardStop, reopen, and resume with the same operation key.

In both campaigns:

- the read was physically dispatched **once**;
- changing the fixture's current read value from 7 to 999 did not change the recovered value **7**;
- write requests numbered **2**, while the provider's unique committed effect count remained **1**;
- the recovered result was `computed=976273`, with approval and write successful;
- repeated Resume after completion returned the saved final payload and caused no new provider activity.

Raw outcome records: [process kill](process-kill.json), [VM hard stop](vm-hard-stop.json).

## Environments

Process case: macOS arm64, real `numpy-core` Guest, SIGKILL of the held demo processes. Reproduce with [test-durable-crash.py](../../scripts/test-durable-crash.py).

VM case: vfkit 0.6.4 / Apple Virtualization.framework, Alpine Linux 3.24, kernel 6.18.52-0-virt, 2 vCPU and 2 GiB RAM. SQLite lived on `/dev/vda3`, an ext4 partition of a 4 GiB sparse virtio-blk disk. It was not stored on virtiofs or tmpfs. The test binary was compiled for Linux arm64 with CGO disabled.

The Host issued `POST /vm/state` with `{"state":"HardStop"}` only after receiving the relevant checkpoint event. No Guest shutdown or explicit `sync` was inserted at the fault point. The same disk booted again between phases. Each run checked persisted state and provider counts. The successful final-schema campaign contains the two hard stops reported in its JSON; earlier environment/harness diagnostics are excluded.

The VM was shut down after verification. This tests abrupt Guest OS loss and recovery from its persistent virtual disk. It does not test loss of power to the Mac, APFS/device write-cache failures, or disk destruction.

## Other coverage

- Eleven real-Guest determinism cases/controls across independent processes: explicit/default Python and NumPy RNGs, entropy, logical time, container ordering, exceptions, and different-seed controls.
- Python `except BaseException` cannot swallow the Host's journal park or produce later success.
- Store reopen, WAL/FULL settings after connection replacement, concurrent first Open, path aliases and ownership locking.
- Single-assignment outcomes/decisions, terminal admission, cancellation, logical payload bounds and declaration drift.
- Real Guest approval reopen, repeated unresolved waits, and stable failed final outcomes.

The first fault-harness runs found harness transport issues and a repeated-wait state bug. Those were corrected, and both final-schema interruption windows were rerun. The records here describe that final successful campaign.

The unrelated `research/labview` accepted-evidence mismatch also fails on the pre-change baseline. Its frozen data and assertions were not rewritten to make this feature pass.

## Scope

The supported recovery mechanism is fresh-Guest reconstruction using recorded calls. It does not save linear memory or resume an arbitrary machine instruction. Pure computation can repeat. No PLM/COW/prefix optimization is enabled in this mode yet. Future combinations need their own equivalence tests.

The process example's single observed recovery duration includes process startup, artifact admission, compilation and replay. It is not a statistical performance result. Payload size grows with retained inputs/results; the default 64 MiB logical limit is not a physical database/WAL-size bound.

See [the usage and declaration contract](../../docs/durable-execution.md).
