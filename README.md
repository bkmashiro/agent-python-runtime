# Pysolate

Python sandbox for AI agents, embedded in Go. Runs CPython + NumPy through
WebAssembly/WASI, with a fresh Guest for each execution.

## Features

- **Code mode:** Python scripts call Host-provided Go functions and MCP tools.
  Credentials and network clients stay in the Host. [Tools](docs/mcp.md)
- **Private workspaces:** bounded filesystems shared across successive executions,
  with snapshots, diffs and change export. [Workspace](docs/workspace.md)
- **Prepared execution:** reuse compiled code and initialized Python state through
  full-copy images or Linux COW mappings. [COW](docs/cow-data-image.md)
- **Concurrent I/O:** overlap explicitly approved independent reads; release
  execution slots during opted-in external Tool waits.
  [Early reads](docs/fullcode-tool-overlap.md) · [Scheduling](docs/scheduling-evaluation.md)
- **Replay and recovery:** persist Tool outcomes, reconstruct interrupted runs and
  resume according to each Tool's declared recovery policy. [Durability](docs/execution-derived-durability.md)
- **Go API, CLI and HTTP service**, with execution deadlines and bounded admission.
  [Service](docs/service.md) · [Durable service](docs/durable-service.md)

Host tools enforce their own authorization. External effects require provider
recovery support; writable workspaces are excluded from durable replay.

## Run

Requires Go 1.25+ and `dist/pysolate.wasm`.
[Build the Guest or install a verified bundle](docs/artifact-profiles.md).

```sh
printf 'result = inputs["value"] + 1\n' | go run ./cmd/pysolate -inputs '{"value":41}'
go run ./examples/python-tools -guest dist/pysolate.wasm
./demos/run-core.sh
```

Scripts receive `inputs`, assign their output to `result`, and call the tools
granted by the Host:

```python
quotes = [market.get_price(symbol=s) for s in inputs["symbols"]]
result = max(quotes, key=lambda quote: quote["price"])
```

[Go embedding example](examples/python-tools/main.go) ·
[All demos](demos/README.md) · [Agent loop](docs/agent-loop.md)

## Performance

Recorded Linux measurements; each linked report includes configuration and raw data.

- **Short Python: 17.82 → 2.90 ms** warm request median with opt-in COW data images.
  Two CPUs, 2 GiB limit, three process pairs. Sampled process PSS increased from
  **450.57 → 589.48 MiB**. [Results and memory diagnosis](docs/cow-data-image.md)
- **I/O-heavy batches: 7.70 → 26.99 tasks/s** when waiting Tools release execution
  slots. Synthetic 200 ms I/O, two running slots, eight resident slots, five repeats.
  Sampled peak PSS: **466.0 → 517.3 MiB**.
  [Results](docs/linux-density-study.md)
- **128-call history replay: 516.35 → 19.46 ms** warm request median with seeded
  COW, before the subsequent read-ahead optimization. Three process repeats on a
  2-vCPU Linux arm64 VM. [Results](docs/performance-results.md#matched-history-replay)

[Performance reports](docs/performance-results.md) ·
[Documentation](docs/README.md)

## Test

```sh
make check
make workloads
```

Real-Guest tests require the Wasm artifact. COW tests require Linux.
