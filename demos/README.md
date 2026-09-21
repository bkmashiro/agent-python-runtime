# Demo scripts

Run from any directory. By default the scripts use `dist/pysolate.wasm`; set `PYSOLATE_GUEST=/path/to/pysolate.wasm` to override it.

```bash
./demos/01-basic-python.sh
./demos/02-namespaced-tools.sh
./demos/03-workspace-edit.sh
./demos/04-hot-service.sh
./demos/run-all.sh
```

- `01-basic-python.sh` shows a fresh private CPython Guest, JSON inputs, NumPy, safe YAML and a synchronous Host tool.
- `02-namespaced-tools.sh` shows `market.get_prices(...)`, dynamic Guest namespace injection, real Host HTTP and repository/config/data processing.
- `03-workspace-edit.sh` shows a private source copy, ordinary Python file editing, deterministic diffs, Host HTTP, parameterized SQLite and an idempotent Host write.
- `04-hot-service.sh` starts the long-running service, sends repeated hot requests, keeps a workspace across a disposable Guest, reads one output file and destroys the workspace. It chooses a free loopback port and stops the service on exit.

## Short code-reading route

1. `examples/agent-core-usecases/main.go`: concrete Host manifest and Python use case.
2. `runner.go`: Runner ownership, Guest lifecycle and `RunWorkspace`.
3. `tool_provider.go` and `bridge.go`: provider discovery, Python paths and the single JSON tool ABI.
4. `guest/bootstrap.py` and `guest/pysolate.py`: generated Python functions and execution convention.
5. `prepared.go` and `cow_linux.go`: clean prepared image and Linux private COW instances.
6. `runtime/workspace/workspace.go`: bounded workspace lifecycle and snapshots.
7. `service/server.go`: bounded HTTP admission and persistent workspace leases.

This branch is a direct continuation of the interview `spine/` implementation, not a wrapper around the old large runtime. The same short `Runner -> Wasm CPython -> Host bridge` path remains, while prepared/COW execution, PLM/prefix, dynamic tool providers, durable replay, workspaces and the HTTP service have been added around it.
