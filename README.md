# Agent Python Runtime

A capability-controlled CPython/WASI runtime for executing generated Python without giving guest code ambient access to the host.

The agent framework stays outside the sandbox. It supplies code and JSON inputs; the Go host owns runtime limits, tool grants, credentials, instance lifecycle, and receipts.

## Why this project

Agent-generated Python is useful for local control flow, filtering, aggregation, and multi-step data processing. Running it in a normal Python process also gives it the process's filesystem, environment, network, and subprocess authority. This runtime places CPython in a WebAssembly guest and exposes only explicitly registered host capabilities.

Current properties:

- CPython 3.14 targeting `wasm32-wasip1`;
- Go host with a backend-neutral `Runner`/`Factory` interface and a wazero backend;
- fresh guest per request by default;
- optional single-use prepared and Linux COW-ready execution strategies;
- strict JSON request and response contracts;
- host-owned time, memory, output, and capability-call limits;
- no ambient guest filesystem, environment, socket, process, or credential access;
- provenance manifests, source locks, checksums, SBOMs, and Linux/WASI integration tests;
- optional `numpy-core` profile, including a `numpy-ready-v1` pre-COW warmup profile.

## Quick start

### 1. Run host-side tests

Requirements: Go 1.25 and Python 3.

```bash
git clone git@github.com:bkmashiro/agent-python-runtime.git
cd agent-python-runtime

go test ./...
python3 -m unittest discover -s tests -v
python3 -m unittest discover -s guest/tests -v
```

These checks exercise the Go runtime, schemas, source locks, and build contracts. They do not execute the real WASI guest.

### 2. Build and test the real guest

The guest build currently requires Linux x86-64, `build-essential`, `pkg-config`, `unzip`, and `xz-utils`. It downloads only SHA-256-locked inputs declared in `guest/build/sources.lock.json`.

```bash
AGENT_RUNTIME_ARTIFACT_PROFILE=base \
  guest/build/build-guest.sh

python3 guest/build/verify-artifact.py \
  dist/agent-python-runtime.wasm \
  dist/manifest.json

(
  cd dist
  sha256sum --check SHA256SUMS
)

AGENT_RUNTIME_GUEST="$PWD/dist/agent-python-runtime.wasm" \
  go test ./integration/e2e -count=1 -v
```

The `numpy-core` profile is manual and experimental:

```bash
AGENT_RUNTIME_ARTIFACT_PROFILE=numpy-core \
AGENT_RUNTIME_COW_FIXED_MEMORY=1 \
  guest/build/build-guest.sh
```

### 3. Execute one request

After building the base artifact:

```bash
go run ./cmd/apyrun \
  -artifact dist/agent-python-runtime.wasm \
  < abi/v1/fixtures/valid/request/basic.json
```

The request contains only generated code, JSON inputs, an untrusted run label, and an optional output schema. Host capabilities and credentials are configured separately; they cannot be granted by the request.

## Integration model

```text
agent framework
    │ generated code + JSON inputs
    ▼
Go host
    ├── validates the request
    ├── selects fresh / prepared / COW execution
    ├── enforces resource limits
    ├── mediates explicitly granted tool calls
    └── validates the bounded result
    ▼
CPython/WASI guest
```

For a first integration, start with a deterministic read-only fake tool and verify success, denial, timeout, and cross-run isolation before connecting any real provider. See [Framework integration test drive](docs/framework-integration.md).

## Execution strategies

| Strategy | Preparation boundary | Served instance reuse |
|---|---|---|
| `fresh` | instantiate and initialize for each request | no |
| `single-use-preinitialized` | initialize a never-served instance before checkout | no |
| `cow-ready-single-use` | restore a never-served slot from a sealed Linux COW image | no |

A configured COW warmup profile runs after CPython initialization and before the canonical image is sealed. For example, `numpy-ready-v1` imports NumPy before readiness so a matching request does not repeat the import.

On the retained Linux benchmark, a fresh native CPython process importing NumPy took a median `324.067 ms`; a request hitting an already prepared NumPy-ready COW slot took `3.863 ms`. This is a lifecycle comparison, not a claim that WebAssembly executes NumPy kernels faster than native code. See the [benchmark report](docs/reports/scheduler-experiment-results.md#8-python-and-numpy-request-lifecycle).

## Repository map

```text
abi/v1/                    request, response, tool, and evidence schemas
cmd/apyrun/                local CLI
cmd/apyrun-benchmark/      artifact-bound benchmark harness
runtime/engine/            backend-neutral runtime interface
runtime/engine/wazero/     wazero fresh, prepared, and COW implementations
runtime/capability/        host-owned capability registry and adapters
guest/                     CPython/WASI source, bootstrap, and build pipeline
integration/e2e/           real guest integration tests
eval/                      deterministic agent-workflow evaluation fixtures
docs/                      architecture, threat model, integration, and results
```

## Documentation

- [Framework integration test drive](docs/framework-integration.md)
- [Architecture](docs/architecture.md)
- [Threat model](docs/threat-model.md)
- [Local CLI](docs/operator-cli.md)
- [Development and test gates](docs/development.md)
- [Benchmark methodology](docs/benchmarking.md)
- [Scheduler and NumPy-ready results](docs/reports/scheduler-experiment-results.md)
- [Supply chain](docs/supply-chain.md)
- [Research roadmap](docs/research-roadmap.md)

## Project status

This is a research prototype; it is not released or deployed, and it is not a general Linux sandbox. The base profile is the conservative default. The manual-only `numpy-core` profile remains experimental; served instances are never reused.

The repository does not currently grant a public release license. Select and add a root license before making a public release or inviting unrestricted reuse. Private test drives should be arranged with the repository owner.
