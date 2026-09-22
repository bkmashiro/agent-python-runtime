# Demo scripts

Run from any directory. By default the scripts use `dist/pysolate.wasm`; set `PYSOLATE_GUEST=/path/to/pysolate.wasm` to override it.

```bash
./demos/01-basic-python.sh
./demos/02-namespaced-tools.sh
./demos/03-workspace-edit.sh
./demos/04-hot-service.sh
./demos/05-corpus-replay.sh
./demos/06-semantic-phases.sh
./demos/07-live-io.sh
./demos/08-scheduling-simulation.sh
./demos/09-canonical-scheduling-sweep.sh
./demos/10-calibrated-scheduling.sh
./demos/11-mcp-stdio.sh
./demos/12-mcp-workspace-scheduling.sh
./demos/13-durable-restart.sh
./demos/14-agent-workloads.sh
./demos/15-deterministic-replay.sh
./demos/16-fullcode-overlap.sh
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
- `15-deterministic-replay.sh` checks seeded entropy/logical clocks across processes and verifies durable Tool outcome replay and prepared seed state.
- `16-fullcode-overlap.sh` compares ordinary execution with complete-source preparation for two independent, explicitly opted-in Host reads under a controlled delay.

`run-all.sh` intentionally runs the short product demonstrations `01`–`05`.
The measurement-oriented `06`–`10` scripts and dependency-oriented MCP demos
`11`–`16` remain explicit.

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

This branch is a direct continuation of the interview `spine/` implementation, not a wrapper around the old large runtime. The same short `Runner -> Wasm CPython -> Host bridge` path remains, while prepared/COW execution, PLM/prefix, dynamic tool providers, durable replay, workspaces and the HTTP service have been added around it.
