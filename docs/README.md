# Documentation index

This directory separates supported behavior, reproducible evaluation, and
forward-looking research. Start with the repository [`README`](../README.md)
for the execution model and build commands.

## Supported workflows

- [`artifact-profiles.md`](artifact-profiles.md) — the supported `agent-core`
  Guest contents, qualification probes, native relink versus VFS repack, and
  artifact manifests.
- [`common-usecases.md`](common-usecases.md) — qualified repository editing,
  structured-data processing, NumPy, and Host-enriched Python workflows.
- [`workspace.md`](workspace.md) — bounded private filesystem lifecycle,
  snapshots, diffs, reviewable change export, read-only Host conflict checks,
  security boundaries, and executable acceptance.
- [`mcp.md`](mcp.md) — official Go SDK integration, trusted stdio lifecycle,
  result envelopes, authority boundaries, and real-Guest acceptance.
- [`execution-derived-durability.md`](execution-derived-durability.md) — why
  ordinary Python does not need a workflow DSL, how Tool declarations define
  reusable effect boundaries, the Temporal comparison, and the limits of
  automatic recovery.
- [`deterministic-execution-and-replay.md`](deterministic-execution-and-replay.md)
  — the exact controlled-input contract, current evidence and prioritized
  replay-observability work.
- [`runtime-harness-boundary.md`](runtime-harness-boundary.md) — the ownership
  split between isolated execution, replay and local resource lifecycle in
  Pysolate versus goals, model turns, events and global policy in the harness.
- [`service.md`](service.md) — the long-running local HTTP service, hot
  prepared execution, workspace endpoints, admission behavior, and benchmark.
- [`durable-service.md`](durable-service.md) — persisted Run and attempt HTTP
  lifecycle, bounded admission, process-crash recovery, and ownership limits.
- [`corpus-replay.md`](corpus-replay.md) — deterministic HumanEval/BFCL
  adapters and exact Tool-result replay without an LLM in the timed loop.
- [`workload-pack.md`](workload-pack.md) — 20 frozen common Agent Python/Tool
  cases plus workspace, real MCP and process-recovery acceptance lanes.
- [`fullcode-tool-overlap.md`](fullcode-tool-overlap.md) — a deliberately small
  complete-source optimization for overlapping independent Host reads, with a
  real-Guest controlled-delay demo and explicit exclusions.

## Performance and scheduling

- [`linux-density-study.md`](linux-density-study.md) — same-limit Linux throughput and sampled memory comparison for Inline versus ExternalIO, with raw data and reproduction commands.

- [`performance-results.md`](performance-results.md) — measured prepared/COW,
  compilation-cache, historical prefix/PLM, admission, and service results with scope and
  trade-offs.
- [`performance-goal.md`](performance-goal.md) — the completed performance
  goal and its original acceptance boundaries. Keep this as a historical
  implementation record rather than the current roadmap.
- [`semantic-scheduling-study.md`](semantic-scheduling-study.md) — the active
  scheduling direction: common workload shapes, phase measurement, durable
  park/re-admit, and bounded live-I/O slot reuse.
- [`scheduling-evaluation.md`](scheduling-evaluation.md) — per-Tool timing,
  the Linux-oriented phase matrix, trace schema, deterministic policy
  simulator, controlled canonical sweep, and measured calibration replay.
- [`mcp-workspace-scheduling-spike.md`](mcp-workspace-scheduling-spike.md) — a
  deterministic real-MCP tool chain, inline versus live-I/O scheduling, and
  explicit handoff into reviewable private-workspace edits.
- [`performance-data/`](performance-data/) — checked-in raw measurements. Each
  result should be interpreted with the artifact, host, fixture, and sample
  count recorded by its corresponding document or local README.

## Current delivery snapshot

The recent runtime work now covers:

1. **Dynamic Tool ABI:** Host providers expose canonical tool identities as
   generated namespaced Python functions through one JSON Host-call bridge.
   An official-Go-SDK adapter now exercises real MCP stdio initialization,
   discovery and calls. Ordinary and durable paths now share one normalized
   `Capability` definition; durable provider catalogs require an explicit Host
   policy for version, recovery and scheduling. Tool catalogs remain Host-side
   and do not enlarge or rebuild the Guest artifact.
2. **Private workspaces:** a bounded `/workspace` supports normal file editing,
   local imports, snapshots, deterministic diffs, bounded change export, and
   read-only touched-path conflict checks without mounting or modifying the
   source repository from the Guest.
3. **Agent-core artifact:** CPython, NumPy, PyYAML, common stdlib workflows,
   qualification probes, and separate native-relink/VFS-repack paths are
   documented and tested.
4. **Hot service:** compiled code and clean prepared images stay warm behind a
   bounded local HTTP service with explicit 429 admission behavior.
5. **Deterministic corpus replay:** frozen HumanEval programs and BFCL calls can
   test runtime and Tool ABI compatibility without repeatedly generating code.
6. **Execution-derived durability:** ordinary Python calls Host-declared Tools;
   the runtime records those calls as effect boundaries and replays persisted
   outcomes. Provider-owned recovery contracts handle unresolved effects.
   Completed, parked, blocked, Python-failed and cancelled attempts are
   structured results; only infrastructure failures remain Go errors. A durable
   park destroys the Guest, then re-admission reconstructs the attempt.
7. **Live external-I/O scheduling:** an opted-in `ExternalIO` Tool releases its
   running slot while retaining the live Guest. Running, resident, external
   Tool, and queued limits are independent. The initial two-Run pilot reduced
   the controlled 100 ms-wait batch median from 238.16 ms to 125.91 ms.
8. **Scheduling evaluation:** real Tool calls now expose queue, Host service,
   and continuation-resume durations with bounded mean/EWMA aggregation. A
   deterministic simulator compares FIFO, ready-first, and finish-soon over
   explicit phase traces. A canonical CPU/I/O sweep isolates resident, running,
   and Tool-capacity effects without changing production scheduling.
9. **Measured calibration replay:** phase-bench rows can be converted into
   deterministic simulator tasks and checked against bounded real 2/4/8-Run
   batches. Reports preserve the CPU-split assumptions and prediction error.
10. **MCP-to-workspace workflow spike:** two dependent real MCP calls retain
    the live-I/O scheduling benefit before validated JSON is handed to a
    separate writable-workspace Guest. The split makes the current durable /
    writable-workspace boundary explicit rather than coupling their state.
11. **Durable service:** a trusted-local HTTP control plane now persists Run
    definitions, executes bounded attempts and survives a real process kill.
    Acceptance commits an idempotent external effect, kills the process before
    completion, restarts from SQLite, and proves replay produces one effect.
12. **Artifact workflow:** `make bootstrap`, `guest`, `repack`,
    `verify-artifact`, `artifact-bundle`, and `artifact-install` separate the
    expensive pinned Linux build from verified prebuilt artifact consumption.
13. **Agent workload pack:** 20 fixed common Python and exact Tool-replay cases
    now emit artifact-bound setup/run timings. Separate real-workspace, MCP
    stdio and process-kill lanes preserve boundaries that unordered fixtures
    cannot represent honestly.

The next scheduling work remains evidence-led: repeat calibrated replay and the
MCP workflow on Linux, add matching real batches for read-then-compute and any
workspace-internal Tool calls observed in real workloads, and introduce policy
only where bounded admission and live-I/O yielding leave a measured gap. Do not
infer arbitrary Python runtime behavior or retry unsafe effects.

## Demonstrations

The recommended presentation entry is `../demos/run-core.sh`: dynamic Host
tools, workspace continuation, hot service execution and visible deterministic
Tool replay. The complete script index is grouped in
[`../demos/README.md`](../demos/README.md).

Individual scripts provide narrower acceptance and research paths:

- `01` basic Python and artifact contents;
- `02` ordinary Python calling one dynamically injected namespaced Host tool;
- `03` private workspace editing;
- `04` hot local service;
- `05` deterministic corpus replay;
- `06` durable semantic phases;
- `07` inline versus live external-I/O scheduling.
- `08` deterministic scheduling-policy simulation.
- `09` controlled canonical scheduling sweep.
- `10` measured phase replay versus real bounded batches.
- `11` official MCP Go SDK over stdio into a generated Guest Python tool.
- `12` real MCP tool-chain scheduling followed by workspace ChangeSet export.
- `13` process-kill durable recovery with an idempotent Host effect.
- `14` the complete fixed Agent workload pack.
- `15` deterministic randomness/clock/Tool replay conformance.
- `16` full-code overlap of independent read-only Host calls.
- `17` Host-owned workspace continuation across disposable Guests.

`demos/run-all.sh` retains the original acceptance suite (`01`–`05`). The
measurement-oriented and dependency-heavy scripts remain explicit so setup
time is not hidden inside the core presentation path.
