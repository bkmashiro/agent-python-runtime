# Pysolate

Pysolate runs Agent-authored Python in a fresh, bounded CPython/WASI Guest.

The Host chooses the Guest artifact, import profile, workspace, capabilities, budgets and external providers. Guest code receives only the authority granted for that run. It has no ambient network, shell, subprocess, package-manager, credential or Host-filesystem access.

## Why Pysolate

A fresh Guest gives every run private Python state, but isolated execution can still waste time in two places:

- repeated runtime and module setup across runs;
- synchronous Host calls that wait one after another inside a run.

Pysolate keeps the logical run private while allowing two narrower physical optimisations:

- **Prepare–Linearize–Materialize (PLM)** may start an eligible Host request early. Python still receives its value or error at the original call.
- **Image-backed copy-on-write (COW)** shares clean, request-independent linear-memory pages while each Guest receives private pages when it writes.

Both mechanisms are explicit opt-in research paths. The default CLI and HTTP paths create a fresh Guest and do not enable them automatically.

## Architecture

```text
Agent or harness
      |
      | Python source + JSON input
      v
Go Host
  - verifies the Guest artifact
  - derives and checks imports
  - binds the workspace and capability Plan
  - owns credentials, providers and budgets
      |
      v
fresh CPython/WASI Guest
  - runs normal Python control flow
  - sees only mounted files and Host-granted tools
  - returns a bounded result and Host receipts
```

The Host does not become a second Python scheduler. Branches, loops, exceptions and ordinary computation remain inside CPython.

## Current runtime

The maintained execution path provides:

- a fresh CPython/WASI Guest for every run;
- verified Guest artifacts and Host-derived import admission;
- wall-clock, memory, input, output and Host-call bounds;
- a private `/workspace` and `/tmp`;
- ordinary Python file APIs over the mounted workspace;
- typed Host capabilities with strict input and output schemas;
- per-run capability Plans, budgets and receipts;
- optional workspace export, storage and restoration;
- two bounded, credential-free external JSON sources;
- capture and offline playback for those curated reads;
- optional Host-side lifecycle and workspace observations.

See [Product direction](docs/product-direction.md) for the boundary between maintained, experimental and proposed work.

## Research mechanisms

### Prepare–Linearize–Materialize

PLM separates the start of a Host request from its original Python call:

```text
Prepare      start eligible Host work early
Linearize    validate it when Python reaches the original call
Materialize  wait if needed, then return the value or raise the error there
```

Prefix analysis can find a complete call while later source is still arriving. A whole-program pass can place `Prepare` after data dependencies and inside the controlling Python block. The Host, not source analysis, decides whether an operation may be observed earlier.

PLM is implemented, exact-Guest tested and default-off. Unsupported calls keep their normal synchronous behaviour.

- [PLM contract](docs/research/logical-time-plm-v1-contract.md)
- [Source-pass integration](docs/source-pass-plugins.md)
- [PLM evidence](docs/evidence/plm-v1-multiread-economics.json)

### Image-backed copy-on-write

The Linux COW path captures trusted, request-independent WebAssembly linear memory and maps it privately into fresh single-use Guests. Clean pages share the sealed image. Pages changed by a run become private.

Run identity, capability Plan, Broker, workspace and request data are created or bound separately for each Guest. The portable fallback copies the prepared state instead of using Linux private mappings.

- [Prepared Family contract](docs/prepared-family-v1.md)
- [COW evidence](docs/evidence/copy-on-write-economics.json)

Other experimental and historical mechanisms remain documented under [`docs/`](docs/) but are not part of the default runtime path.

## Requirements

- Go 1.25 or newer
- a verified Pysolate Guest distribution containing:
  - `agent-python-runtime.wasm`
  - `manifest.json`
  - `import-inventory.json`
  - `import-qualification.json`

Build the Guest with:

```bash
guest/build/build-guest.sh
```

See [Development](docs/development.md) for the pinned CPython/WASI build and verification workflow.

## Build and run

Build the CLI:

```bash
go build ./cmd/apyrun
```

Run a small program:

```bash
printf '%s' '{"run_id":"demo","code":"result = inputs[\"value\"] + 1","inputs":{"value":41}}' |
  go run ./cmd/apyrun -artifact /path/to/agent-python-runtime.wasm
```

The response is bounded JSON containing the status, result, metrics, Host receipts and an optional Python error.

## Workspace example

The Host can mount a private workspace and allow a small import set. Agent code then uses ordinary Python APIs:

```python
from pathlib import Path

values = [int(line) for line in Path("/workspace/values.txt").read_text().splitlines()]
Path("/workspace/total.txt").write_text(str(sum(values)))
result = {"total": sum(values)}
```

External systems remain behind typed Host capabilities. Guest code cannot choose arbitrary URLs, credentials, mounts or resource budgets.

- [Mounted workspaces and capsules](docs/workspace-capsules.md)
- [Bounded developer tools](docs/developer-tools.md)
- [Playback Bundles](docs/playback-bundles.md)

## Security boundary

A request may provide Python source, JSON input, an optional output schema and compatibility requirements. It cannot provide:

- capabilities or credentials;
- environment variables or process arguments;
- network targets;
- Host paths or mounts;
- resource budgets;
- package-installation authority.

Pysolate is a research prototype, not a mature multi-tenant service or a completed adversarial-security evaluation. See [Threat model](docs/threat-model.md) and [Source compatibility](docs/source-compatibility.md).

## Repository map

```text
cmd/apyrun/                 JSON stdin/stdout CLI
runtime/                    request, execution and lifecycle contracts
runtime/engine/wazero/      CPython/WASI execution and prepared images
runtime/capability/         typed Host capabilities, Plans, Broker and PLM
runtime/workspace/          bounded workspace mounts and capsules
runtime/playback/           curated-read capture and playback
guest/                      CPython/WASI Guest source and build
integration/e2e/            real-Guest integration tests
scripts/                    verification and experiment runners
docs/                       design, evidence and research records
```

## Verification

```bash
go test ./runtime/... ./cmd/apyrun ./cmd/pysolate-httpd
go vet ./runtime/... ./cmd/apyrun ./cmd/pysolate-httpd

AGENT_RUNTIME_GUEST=/path/to/agent-python-runtime.wasm \
  go test ./integration/e2e -count=1
```

The exact-Guest suite requires a built and verified Guest distribution. Research packages have separate evidence-pinned gates documented in [Development](docs/development.md). Repository scripts never silently substitute a missing artifact.
