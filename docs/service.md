# Long-running local service

`cmd/pysolate-server` keeps the compiled Guest and prepared clean images hot behind a bounded HTTP control plane. It is intended for a trusted local caller or a separately authenticated application tier. It does not implement authentication, TLS, multi-tenant policy, billing, or a network sandbox.

## Start

```sh
go run ./cmd/pysolate-server \
  -guest dist/pysolate.wasm \
  -listen 127.0.0.1:8080 \
  -max-active 4
```

The default workspace root is a private temporary `0700` directory removed at shutdown. `-workspace-root` selects a persistent Host-owned root, which must itself be a clean `0700` directory. Do not expose this server directly to an untrusted network.

Linux uses the private COW prepared-image backend. Other platforms use the full-copy prepared backend. The no-workspace and workspace Runners share a service-owned in-memory wazero compilation cache, while each clean Python image is initialized independently. A submitted Run still gets a fresh isolated Guest; a hot hit reuses compiled code and the clean initialized image rather than Python process state.

The standalone binary grants no Host tools. Applications that need filesystem services, databases, HTTP APIs, stock prices, or MCP tools construct a `Manifest` from trusted providers and pass it to `service.New`. The same artifact can expose a different catalog without relinking CPython/NumPy or rebuilding `pysolate.wasm`. The catalog is fixed for one service instance, so changing it requires rebuilding the prepared service state, not the Guest artifact.

## HTTP API

All request and response bodies are JSON. Request bodies are capped at 1 MiB. `[]byte` fields use standard JSON base64 encoding.

- `GET /healthz`
  - Returns `{"ready":true}` while the service accepts work.
- `POST /v1/run`
  - Body: `{"source":"result=inputs['value']+1","inputs":{"value":41}}`
  - Executes without a workspace.
- `POST /v1/workspaces`
  - Body: `{"files":[{"path":"notes.txt","data":"YmVmb3Jl"}]}`
  - Creates and exclusively leases a bounded private workspace.
- `POST /v1/workspaces/{ref}/run`
  - Same body as `/v1/run`; `/workspace` is mounted read/write.
  - The response includes before/after revisions and an added/modified/deleted summary.
- `GET /v1/workspaces/{ref}/snapshot`
  - Returns deterministic path, mode, size, and content-digest metadata. File contents are not included.
- `GET /v1/workspaces/{ref}/files?path=notes.txt`
  - Returns one ordinary file, capped at 1 MiB, without loading all workspace contents.
- `DELETE /v1/workspaces/{ref}`
  - Releases and removes the private workspace.

Successful Runs return the Python value, captured stdout, transformed source when applicable, `run_ns`, and `total_ns`. `run_ns` covers the Runner call. `total_ns` also covers service-side workspace snapshots and diffing, but not HTTP transport or JSON decoding before the handler timer starts.

Python failures return HTTP 422 and preserve any private workspace changes in the response's after-revision and diff. Invalid requests return 400. An unknown workspace returns 404. When all execution slots are occupied, admission is non-blocking and returns 429 instead of growing an unbounded queue.

Workspace publication remains a Host decision. The service never accepts an arbitrary Host source or destination path and never writes changes back into a project tree.

## Measure the hot path

`pysolate-service-bench` starts the real Handler on a loopback TCP listener, excludes service construction and one warmup per worker from request samples, and emits JSON Lines with every sample plus p50/p95 summaries:

```sh
go run ./cmd/pysolate-service-bench \
  -guest dist/pysolate.wasm \
  -iterations 50 \
  -concurrency 1,2,4 \
  -max-active 4 > service-bench.jsonl
```

The benchmark reports:

- artifact SHA-256 and size;
- service/prepared-image setup time separately;
- client-observed loopback E2E latency;
- server-reported Runner and total handler latency;
- plain and persistent-workspace cases;
- a real saturated-slot probe that must return 429.

Each concurrency level uses that many workers and `iterations` measured requests per worker. Workspace workers receive separate leases. Results describe this artifact, Host, workload, and concurrency only; they are not a generic Python or remote-network latency claim.
