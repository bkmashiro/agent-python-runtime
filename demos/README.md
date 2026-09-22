# Demo scripts

Run from any directory. By default the scripts use `dist/pysolate.wasm`; set `PYSOLATE_GUEST=/path/to/pysolate.wasm` to override it.

## Core product path

```bash
./demos/run-core.sh
```

Run this path first. It presents the same product boundary in five steps:

1. `02-namespaced-tools.sh` — **approved capability:** ordinary Guest Python
   calls `market.get_price(...)`, a namespaced function that the Host injected
   from one allowlisted Tool.
2. `17-workspace-continuation.sh` — **file continuity:** the first disposable
   Guest writes progress and the second reads and extends it through one
   Host-owned workspace. This is not `RunRecorded` replay.
3. `04-hot-service.sh` — **warm local service:** repeated HTTP Runs use the
   bounded service, then a workspace is created, edited, read back, and
   destroyed through the HTTP API.
4. `16-fullcode-overlap.sh` — **safe early reads:** two approved independent
   reads overlap, with identical output and measured timing.
5. `15-deterministic-replay.sh` (visible portion) — **replay:** the same seeded
   program produces the same result while a completed Tool outcome is used once
   live and once from the journal.

The output labels what to observe at each boundary. The core path skips the
longer replay conformance tests; run `15-deterministic-replay.sh` directly when
those checks are wanted. The process-kill recovery story is separate in
`13-durable-restart.sh` so workspace continuity and durable replay stay distinct.

## Complete index

### Basic acceptance

```bash
./demos/01-basic-python.sh
./demos/02-namespaced-tools.sh
./demos/03-workspace-edit.sh
./demos/04-hot-service.sh
./demos/05-corpus-replay.sh
./demos/14-agent-workloads.sh
./demos/17-workspace-continuation.sh
```

### Host integration and durability

```bash
./demos/11-mcp-stdio.sh
./demos/12-mcp-workspace-scheduling.sh
./demos/13-durable-restart.sh
./demos/15-deterministic-replay.sh
```

### Measurement and research

```bash
./demos/06-semantic-phases.sh
./demos/07-live-io.sh
./demos/08-scheduling-simulation.sh
./demos/09-canonical-scheduling-sweep.sh
./demos/10-calibrated-scheduling.sh
./demos/16-fullcode-overlap.sh
```

### Script suites

```bash
./demos/run-core.sh
./demos/run-all.sh
```

- `01-basic-python.sh` shows a fresh private CPython Guest, JSON inputs, NumPy, safe YAML and a synchronous Host tool.
- `02-namespaced-tools.sh` runs the README's compact `strategy.py` example and shows `market.get_price(...)` injected from one Host-owned capability.
- `03-workspace-edit.sh` shows a private source copy, ordinary Python file editing, deterministic diffs, bounded changed-content export, read-only Host conflict checks, Host HTTP, parameterized SQLite and an idempotent Host write.
- `04-hot-service.sh` starts the long-running service, sends repeated hot requests, keeps a workspace across a disposable Guest, reads one output file and destroys the workspace. It chooses a free loopback port and stops the service on exit.
- `05-corpus-replay.sh` runs frozen Python plus an exact namespaced Host-tool fixture twice through one prepared Runner and validates the output.
- `06-semantic-phases.sh` measures fixed read, chained-tool, durable park/re-admit and local NumPy phases without regenerating code.
- `07-live-io.sh` compares an inline Host wait with an opted-in `ExternalIO` wait that retains the live Guest while releasing its running slot.
- `08-scheduling-simulation.sh` compares FIFO, ready-first, and finish-soon over fixed common-agent phase traces without running an LLM or changing runtime policy.
- `09-canonical-scheduling-sweep.sh` isolates running, resident, Tool-capacity and I/O-ratio effects with identical deterministic CPU-I/O-CPU tasks.
- `10-calibrated-scheduling.sh` measures a real single-Run phase trace, deterministically replays it, and compares prediction error with real 2/4-Run Executor batches.
- `11-mcp-stdio.sh` starts an official-SDK MCP stdio subprocess, discovers its catalog tool, injects `catalog.lookup(...)`, and executes it from the real Guest.
- `12-mcp-workspace-scheduling.sh` compares inline and `ExternalIO` execution of a dependent real-MCP tool chain, then hands the validated data to private-workspace Guests and exports conflict-checked `ChangeSet`s.
- `13-durable-restart.sh` commits an idempotent Host effect, kills the service process before completion, restarts from SQLite and proves replay does not duplicate the effect.
- `14-agent-workloads.sh` runs 20 fixed common Python/Tool cases with phase timings, then real workspace, MCP stdio and durable process-restart lanes.
- `15-deterministic-replay.sh` prints a concrete seeded-randomness and Tool-outcome replay, then runs the deterministic and durable replay conformance tests.
- `16-fullcode-overlap.sh` compares ordinary execution with complete-source preparation for two independent, explicitly opted-in Host reads under a controlled delay.
- `17-workspace-continuation.sh` has one disposable Guest write progress and a second Guest read and extend it through the same Host-owned workspace. It does not use `RunRecorded` replay.

`run-all.sh` retains the original acceptance suite `01`–`05` for compatibility.
`run-core.sh` is the recommended presentation path. Measurement, MCP,
durability and broad workload scripts remain individually runnable.

## Short code-reading route

1. `examples/python-tools/strategy.py` and `main.go`: the smallest complete ordinary-Python plus dynamic-Tool path.
2. `examples/agent-core-usecases/main.go`: a richer Host manifest and repository/config/data use case.
3. `runner.go`: Runner ownership, Guest lifecycle and `RunWorkspace`.
4. `tool_provider.go`, `mcpadapter/`, and `bridge.go`: provider discovery, official MCP SDK adaptation, Python paths and the single JSON tool ABI.
5. `guest/bootstrap.py` and `guest/pysolate.py`: generated Python functions and execution convention.
6. `prepared.go` and `internal/cowmem/`: clean prepared-image orchestration and Linux private COW memory.
7. `runtime/workspace/workspace.go`, `snapshot.go`, and `export.go`: bounded workspace lifecycle, snapshots, change handoff, and conflict checks.
8. `service/server.go`: bounded HTTP admission and persistent workspace leases.
9. `corpus/` and `cmd/pysolate-corpus/`: strict dataset cases and exact tool replay through the real Guest.
10. `durable/runner.go` and `durable/executor.go`: replay/effect semantics, explicit attempt states, bounded admission and live-I/O slot reuse.
11. `service/durable/` and `cmd/pysolate-durable-server/`: the persisted HTTP lifecycle and real process-restart acceptance.
12. `cmd/pysolate-phase-bench/` and `cmd/pysolate-queue-bench/`: fixed phase and scheduling measurements.
13. `scheduling/`, `cmd/pysolate-schedule-sim/`, and `cmd/pysolate-calibrate/`: deterministic population-policy evaluation and measured-trace calibration.

This branch is a direct continuation of the interview `spine/` implementation, not a wrapper around the old large runtime. The same short `Runner -> Wasm CPython -> Host bridge` path remains, while prepared/COW execution, complete-source early reads, dynamic tool providers, durable replay, workspaces and the HTTP service have been added around it.
