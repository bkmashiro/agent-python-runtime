# Long-running local service

`cmd/pysolate-server` keeps the compiled Guest and prepared clean images hot behind a bounded HTTP control plane. It is intended for a trusted local caller or a separately authenticated application tier. It does not implement authentication, TLS, multi-tenant policy, billing, or a network sandbox.

## Start

```sh
go run ./cmd/pysolate-server \
  -guest dist/pysolate.wasm \
  -listen 127.0.0.1:8080 \
  -max-active 4 \
  -max-run-duration 30s
```

The default workspace root is a private temporary `0700` directory removed at shutdown. `-workspace-root` selects a persistent Host-owned root, which must itself be a clean `0700` directory. Do not expose this server directly to an untrusted network.

Linux uses the private COW prepared-image backend. Other platforms use the full-copy prepared backend. The no-workspace and workspace Runners share a service-owned in-memory wazero compilation cache, while each clean Python image is initialized independently. A submitted Run still gets a fresh isolated Guest; a hot hit reuses compiled code and the clean initialized image rather than Python process state.

The standalone binary grants no Host tools. Applications that need filesystem services, databases, HTTP APIs, stock prices, or MCP tools construct a `Manifest` from trusted providers and pass it to `service.New`. The same artifact can expose a different catalog without relinking CPython/NumPy or rebuilding `pysolate.wasm`. The catalog is fixed for one service instance, so changing it requires rebuilding the prepared service state, not the Guest artifact.

## Optional Linux data-image preparation

Add `-cow-data-image` to `pysolate-server` to skip redundant initial-data copying. It is off by default, requires Linux and a compatible artifact, and rejects unsupported configurations rather than falling back. It applies to both ordinary and workspace requests.

Embedding applications pass `service.Options{COWDataImage: true}` as the optional final argument to `service.New`. The service still supplies a fresh Guest per Run. Read [the measured setup, latency and memory tradeoffs](cow-data-image.md) before enabling it; lower latency does not imply lower peak process memory.

`-max-run-duration` and `service.Options{MaxRunDuration: ...}` are optional execution caps. Zero preserves the uncapped behavior. A request may set a smaller positive `timeout_ms` in milliseconds, but a request cannot exceed the configured cap; an earlier HTTP caller deadline still wins. The deadline is cooperative: Guest execution and Host callbacks must honor their context. A timed-out ordinary Run releases its execution slot. A timed-out workspace Run releases only the per-attempt busy ownership; the persistent workspace remains available until its explicit `DELETE`.

The HTTP benchmark accepts the same `-cow-data-image` flag and records it in metadata. Omit the flag to measure the unchanged baseline.

## HTTP API

All request and response bodies are JSON. Request bodies are capped at 1 MiB. `[]byte` fields use standard JSON base64 encoding.

- `GET /healthz`
  - Returns `{"ready":true}` while the service accepts work.
- `POST /v1/run`
  - Body: `{"source":"result=inputs['value']+1","inputs":{"value":41},"timeout_ms":5000,"early_reads":true}`
  - Executes without a workspace.
  - `early_reads` is false by default and is the only HTTP switch for `RunWithEarlyReads`; the trusted Host manifest's `AllowEarlyRead` bit remains authoritative.
- `POST /v1/workspaces`
  - Body: `{"files":[{"path":"notes.txt","data":"YmVmb3Jl"}]}`
  - Creates and exclusively leases a bounded private workspace.
- `POST /v1/workspaces/{ref}/run`
  - Same body as `/v1/run`; `/workspace` is mounted read/write.
  - The response includes before/after revisions and an added/modified/deleted summary.
  - `early_reads:true` is rejected; workspace execution has no early-read mode.
- `GET /v1/workspaces/{ref}/snapshot`
  - Returns deterministic path, mode, size, and content-digest metadata. File contents are not included.
- `GET /v1/workspaces/{ref}/files?path=notes.txt`
  - Returns one ordinary file, capped at 1 MiB, without loading all workspace contents.
- `DELETE /v1/workspaces/{ref}`
  - Releases and removes the private workspace.

Successful Runs return the Python value, captured stdout, transformed source when applicable, `run_ns`, and `total_ns`. `run_ns` covers the Runner call. `total_ns` also covers service-side workspace snapshots and diffing, but not HTTP transport or JSON decoding before the handler timer starts.

Python failures return HTTP 422 and preserve any private workspace changes in the response's after-revision and diff. Cooperative execution deadlines return HTTP 408. Invalid requests, including non-positive or over-cap `timeout_ms`, return 400. An unknown workspace returns 404. When all execution slots are occupied, admission is non-blocking and returns 429 instead of growing an unbounded queue.

Workspace publication remains a Host decision. The service never accepts an arbitrary Host source or destination path and never writes changes back into a project tree.

## Linux HTTP data-image comparison

Three alternating process pairs each ran 50 plain and 50 workspace requests at concurrency 1 after warm-up: 600 measured responses, all HTTP 200. The real loopback service used two execution slots and `GOMAXPROCS=2` inside a Slurm allocation requesting two CPUs and 2 GiB. Setup is excluded; this is not a remote-network or capacity benchmark.

Median of process p50s, baseline versus opt-in:

- Plain: **25.36 → 6.02 ms**; median paired speedup **4.22×**. Median process p95: **29.04 → 6.33 ms**.
- Workspace: **26.47 → 7.57 ms**; median paired speedup **3.40×**. Median process p95: **30.60 → 8.14 ms**.

Paired speedup is not the ratio of the two displayed medians. This campaign uses a different workload/host observation from the standalone Runner results; do not subtract them to estimate HTTP overhead. The option's retained-memory and startup tradeoffs still apply.

[Raw rows and metadata](performance-data/service-data-image-292712/), [summary](performance-data/service-data-image-292712/summary.json) and [Linux acceptance output](performance-data/service-data-image-292712/verification.txt) are retained. Acceptance covered both default and optimized ordinary/workspace services, overload rejection, real process-kill recovery and history privacy. Both service CLIs were separately started and called over HTTP. `tools/run-service-data-image.py` contains the bounded reproduction campaign.

## Bounded mixed-load lifecycle check

`TestMixedLoadReleasesResources` reuses one service through short Python,
controlled slow Tools, Python failures, execution timeouts, and workspace
create/edit/read/destroy cycles. A slow Tool holds one of two slots while the
other requests run. Every cycle checks that slots and leases return to zero,
workspace directories are removed, and Linux Guest COW mappings are released.
After service close, sealed-image descriptors must also be gone.

The normal test runs eight cycles. A Linux check ran 64 cycles per preparation
mode: 1,024 HTTP requests, including 128 expected Python failures and 128
expected timeout responses. FD counts stayed at 14 for default COW and 16 for
data-image COW. Without forced GC, sampled PSS fell from about 465 to 238 MiB
and 460 to 140 MiB respectively, with six and ten natural GC cycles observed
between the first and last snapshots. Data-image PSS first peaked near 593 MiB;
a rising early sample alone was not evidence of a leak.

This checks finite lifecycle/resource invariants, not production capacity,
long-term leak freedom or scheduler fairness under saturation. The two modes
ran sequentially in one process; do not treat their memory samples as an
independent A/B performance ranking. Go heap and sampled process PSS have
different accounting, and neither is an exact peak measurement.

```sh
PYSOLATE_MIXED_CYCLES=64 PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" \
  go test ./service -run '^TestMixedLoadReleasesResources$' -count=1 -v
```

The test-only cycle limit is 1–128. [Raw samples](performance-data/mixed-load-292806/samples.jsonl),
[summary](performance-data/mixed-load-292806/summary.json) and
[verification](performance-data/mixed-load-292806/verification.txt) are retained.
No runtime GC or scheduling changes were needed.

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
