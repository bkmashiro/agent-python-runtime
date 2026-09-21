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
./demos/run-all.sh
```

- `01-basic-python.sh` shows a fresh private CPython Guest, JSON inputs, NumPy, safe YAML and a synchronous Host tool.
- `02-namespaced-tools.sh` shows `market.get_prices(...)`, dynamic Guest namespace injection, real Host HTTP and repository/config/data processing.
- `03-workspace-edit.sh` shows a private source copy, ordinary Python file editing, deterministic diffs, Host HTTP, parameterized SQLite and an idempotent Host write.
- `04-hot-service.sh` starts the long-running service, sends repeated hot requests, keeps a workspace across a disposable Guest, reads one output file and destroys the workspace. It chooses a free loopback port and stops the service on exit.
- `05-corpus-replay.sh` runs frozen Python plus an exact namespaced Host-tool fixture twice through one prepared Runner and validates the output.
- `06-semantic-phases.sh` measures fixed read, chained-tool, durable park/re-admit and local NumPy phases without regenerating code.
- `07-live-io.sh` compares an inline Host wait with an opted-in `ExternalIO` wait that retains the live Guest while releasing its running slot.
- `08-scheduling-simulation.sh` compares FIFO, ready-first, and finish-soon over fixed common-agent phase traces without running an LLM or changing runtime policy.

`run-all.sh` intentionally runs the short product demonstrations `01`–`05`.
The measurement-oriented `06`, `07`, and `08` scripts remain explicit.

## Short code-reading route

1. `examples/agent-core-usecases/main.go`: concrete Host manifest and Python use case.
2. `runner.go`: Runner ownership, Guest lifecycle and `RunWorkspace`.
3. `tool_provider.go` and `bridge.go`: provider discovery, Python paths and the single JSON tool ABI.
4. `guest/bootstrap.py` and `guest/pysolate.py`: generated Python functions and execution convention.
5. `prepared.go` and `internal/cowmem/`: clean prepared-image orchestration and Linux private COW memory.
6. `runtime/workspace/workspace.go`: bounded workspace lifecycle and snapshots.
7. `service/server.go`: bounded HTTP admission and persistent workspace leases.
8. `corpus/` and `cmd/pysolate-corpus/`: strict dataset cases and exact tool replay through the real Guest.
9. `durable/runner.go` and `durable/executor.go`: replay/effect semantics, explicit attempt states, bounded admission and live-I/O slot reuse.
10. `cmd/pysolate-phase-bench/` and `cmd/pysolate-queue-bench/`: fixed phase and scheduling measurements.
11. `scheduling/` and `cmd/pysolate-schedule-sim/`: deterministic population-policy evaluation over explicit traces.

This branch is a direct continuation of the interview `spine/` implementation, not a wrapper around the old large runtime. The same short `Runner -> Wasm CPython -> Host bridge` path remains, while prepared/COW execution, PLM/prefix, dynamic tool providers, durable replay, workspaces and the HTTP service have been added around it.
