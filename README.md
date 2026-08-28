# Pysolate

Pysolate is a small proof of concept for running Agent-authored Python inside a bounded CPython/WASI Guest.

The Agent submits ordinary Python source. The Host derives its static imports, binds an artifact-qualified profile, starts a fresh Guest, and exposes only explicitly configured Host tools. There is no ambient network, process, package-manager, native-extension, credential, or Host-filesystem authority.

## What the PoC proves

```text
Agent Python source
  → conservative Host import inference
  → Host-owned profile and artifact binding
  → fresh CPython/WASI Guest
  → bounded Host tools
  → Host-authored result, receipts and optional observations
```

The active CLI and HTTP paths deliberately do not enable prepared pools, pinned
sessions, schedulers, durable transactions, MCP daemons, trace databases,
production benchmark orchestration, or recovery machinery. An embedding Host may
explicitly opt into the bounded [Prepared Family](docs/prepared-family-v1.md) API:
one immutable NumPy input can back fresh, single-use consumers while RunConfig,
Plan, Broker, invocation identity and private workspace remain per consumer.
Linux may use qualified private COW; the portable path copies into each fresh
Guest. This is not a resettable pool and it is never selected implicitly by
`apyrun` or the HTTP service.

Continuation-preserving cold-I/O and AST-qualified exact whole-Run reuse remain
fail-closed and off by default. Semantic reuse is an internal Experimental
adapter, not an ordinary arbitrary-Python cache. Pysolate also includes an
optional Host-owned rooted workspace and a complete deterministic storage
capsule; neither is a transaction system. Historical findings are summarized in
[docs/research-history.md](docs/research-history.md) and remain available in Git
history.

The refined product direction is an authority-lifecycle runtime for Agent-authored
programs: ordinary Python provides control flow, while the Host freezes
identity-bound authority and independently governs workspace, scratch,
external-effect, and evidence dispositions. CPython/WASI is the current
substrate, not the intended differentiator. See the
[authority-lifecycle positioning decision](docs/authority-lifecycle-positioning.md),
the [Cloudflare comparison reset](docs/research/cloudflare-code-mode-comparison.md),
and the [proof-first roadmap](docs/proof-first-authority-roadmap.md). The active proposed
successor is the
[Logical-Time-Preserving PLM Autonomous Mega-Goal](docs/plans/2026-08-28-logical-time-preserving-plm-autonomous-megagoal.md).
The implemented predecessor may prepare reads admitted by its plan-epoch contract before
the original call and then claim them through Broker materialisation; its verified
end-to-end fixture uses immutable reads. PLM keeps that substrate
but adds original-point temporal validation: prepare may move earlier, linearization stays
at the source call, and V1 materialization stays there as well. PLM is not yet implemented.

The Python Future projection and the hard-coded `split_phase_sources_read` pass have been removed. Retained-prefix Guest execution and the earlier independent semantic pre-dispatch controller are intended to remain research-only. Independent review found lower-level constructor and Run/Broker ownership defects, so the predecessor is not considered closed; PLM Gate 2 owns those repairs. The direct prepared-value lane remains an independent default-off mechanism. The current
[stage-aware pass catalog](docs/source-pass-plugins.md) exposes 17 default-off entries; one bounded pipeline instance selects at most 16. Prior measurements remain attached to their original mechanisms and are not relabelled as PLM speedups. Predecessor correctness and negative cold exact-Guest economics are recorded in [unified split-phase evidence v1](docs/research/unified-split-phase-evidence-v1.md).
The completed foundation records bounded Experimental target-Guest AST planning,
exact whole-Run single-flight/retention, continuation-preserving cold-I/O evidence and a
small static source-pass seam while keeping every mechanism off by default.
Current, Experimental and Proposed claims remain separated in
[docs/product-direction.md](docs/product-direction.md).

An **Experimental** unified execution-profile slice adds deterministic Host
placement, a verified one-shot native OCI/gVisor backend, private Unix-socket
transport to the same capability Broker, and runtime-owned native workspace
leases. It is not an automatic security-equivalent fallback: only preflight or
Host-authored `runtime_unsupported` outcomes with `not_started/not_started`
may start a new native execution. See
[Unified execution profiles](docs/unified-execution-profiles.md).

## Requirements

- Go 1.25+
- a verified Pysolate Guest distribution containing:
  - `agent-python-runtime.wasm`
  - `manifest.json`
  - `import-inventory.json`
  - `import-qualification.json`

See [docs/development.md](docs/development.md) for building the Guest. For a
five-minute supervisor walkthrough and an explicit assessment of the remaining
product distance, see [the demo guide](docs/supervisor-demo-guide.md) and
[product maturity and roadmap](docs/product-maturity-and-roadmap.md).

## Build

```bash
go build ./cmd/apyrun
```

## Run ordinary Python

```bash
printf '%s' '{"run_id":"demo","code":"result = inputs[\"value\"] + 1","inputs":{"value":41}}' |
  go run ./cmd/apyrun -artifact /path/to/agent-python-runtime.wasm
```

The response is a bounded JSON object with status, result, Host receipts, metrics, an optional Python error, and—when a rooted workspace is configured—a Host-authored workspace disposition receipt.

For a small, paper-oriented progression from pure computation to one and two
curated Host source calls, see the runnable
[controller-boundary examples](examples/controller-boundaries/README.md).

## Agent-intuitive stdlib and workspace tools

The Agent does not submit import metadata. Configure the Host profile and workspace:

```json
{
  "program_surface": "programmatic",
  "execution_profile": {
    "id": "base",
    "allowed_imports": ["csv"]
  },
  "workspace_files": {
    "metrics.csv": "name,value\nalpha,2\nbeta,3\n"
  },
  "max_tool_calls": 4
}
```

Then submit only Python:

```python
import csv

rows = list(csv.reader(workspace.read_text("metrics.csv").splitlines()))
total = sum(int(row[1]) for row in rows[1:])
write_text("summary.txt", str(total))
result = {
    "total": total,
    "files": workspace.list_files(),
    "summary": read_text("summary.txt"),
}
```

The Host derives `csv`, verifies it against the Host profile and distribution manifest, and generates a small `workspace` object plus compatibility aliases from the sealed capability specs:

```python
workspace.read_text(path)      # alias: read_text(path)
workspace.write_text(path, content)  # alias: write_text(path, content)
workspace.list_files()         # alias: list_files()
```

The workspace backend is an in-memory PoC store with canonical relative paths and a one-MiB per-file bound. Every Host call is counted and receives a Host-authored receipt.

For ordinary Python file APIs, the Host can instead project a validated directory snapshot or restore a complete Workspace Capsule into a private `/workspace` mount. The Host must explicitly choose `export_on_success`, `export_on_response`, or `discard`; an export is staged until the augmented response remains within its bound, then atomically published for later restoration by another fresh Guest. This surface is mutually exclusive with the three typed in-memory workspace tools; see [Mounted workspaces and capsules](docs/workspace-capsules.md).

Agent code uses ordinary Python filesystem APIs (`pathlib`, `open`, `shutil`,
`re`, `difflib`, and `hashlib`) against explicit `/workspace` and `/tmp` paths.
Their authority is bounded by the WASI mounts; they are not Broker tool calls.
A separate Host-bound read-only Git capability provides `status`, `diff`, `log`,
`show`, refs and revision resolution. No shell, system binary, Git hook or
network transport is exposed; see
[Bounded developer tools](docs/developer-tools.md).

## Curated information sources

The Current Host CLI can configure two credential-free exact-endpoint sources:
the flat `sources.demo_catalog()` demonstration and the nested versioned
`sources.benchmark_manifest()` research manifest.

```json
{
  "program_surface": "programmatic",
  "information_sources": {
    "demo_catalog": {
      "endpoint": "https://host-selected.example/catalog.json",
      "timeout_ms": 1000,
      "max_response_bytes": 65536
    },
    "benchmark_manifest": {
      "endpoint": "https://host-selected.example/benchmark.json",
      "timeout_ms": 1000,
      "max_response_bytes": 262144
    }
  },
  "max_tool_calls": 2
}
```

Agent Python calls `items = sources.demo_catalog()` or
`manifest = sources.benchmark_manifest()`. It cannot submit a URL, path, query,
method, headers, redirect policy, credentials or transport budgets. Each
private Host adapter performs one GET, refuses redirects, requires status 200
and JSON media type, bounds time and bytes, rejects ambiguous JSON, and applies
its sealed capability schema. The benchmark source additionally validates its
nested schema, version, semantic ID uniqueness, and metric semantics. The
sources may coexist with a mounted `/workspace`; neither is a generic HTTP
client.

The Host may capture schema-validated source calls into a minimal protected [Playback Bundle](docs/playback-bundles.md), then run a second fresh Guest offline after the source is unavailable. Playback requires the capture-issued bundle identity in trusted Host config, constructs no HTTP adapter, strictly consumes the recorded operation sequence through the same Broker schemas and receipts, and verifies response status, Agent-result and final-workspace identities. The bundle binds plan, grants, request, artifact/profile and transcript without storing Agent source, final result body, workspace bodies, endpoint URL or credentials.

## Observation and research prototypes

**Current:** `runtime/observe` defines the bounded Host-only
`pysolate.runtime-observation.v1` contract. A Go Host can attach an `off`,
`best_effort` or `required` Session through the Run context. It records
lifecycle, validated capability calls, and initial/final file-level workspace
deltas; syscall order is explicitly unavailable. `apyrun` does not currently
expose a durable Recorder configuration. See
[Runtime observation](docs/research/runtime-observation.md).

The following surfaces are **Experimental**:

- `runtime/compensation` provides Host-owned graded compensation contracts,
  reverse-topological `plan`/`validate` dry runs, exact-plan review binding,
  execute-time revalidation and deterministic fake-provider evidence. It is not
  yet a production external-write journal or real-provider integration; see
  [Reviewable tool compensation v1](docs/reviewable-tool-compensation-v1.md);
- capability-boundary branches start a fresh Guest, strictly replay a parent
  prefix, then use a Host-owned override, recorded suffix, or already sealed
  live external-read suffix;
- the **Experimental/Partial** deterministic-verification profile controls the
  wazero random source and virtual clocks for an exact artifact while denying
  mounted workspaces and named unsupported workload classes;
- `research/labstore` is a bounded local typed CAS and retention prototype,
  outside Runtime core;
- `pysolate-research` provides bounded inspect/compare, protected branch-plan
  authoring, caller-supplied DAG export, read-only store stats and synthetic
  store benchmarking.

Research branch execution is currently available through
`research/operator.RunBranch`, not through the research CLI. A branch is not a
Python heap or WebAssembly-memory snapshot, and a child may intentionally
diverge from its parent. The local store and CLI are not a complete Lab,
database service, authentication boundary, or combined release proof. See
[Playback Bundles](docs/playback-bundles.md),
[Deterministic verification](docs/research/deterministic-verification.md), and
the [Runtime/Lab boundary](docs/research/lab-boundary.md).

## Security boundary

The request can provide code, JSON inputs, an optional output schema, and compatibility requirements. It cannot provide:

- capabilities or credentials;
- environment variables or process arguments;
- network targets;
- Host paths or mounts;
- resource budgets;
- package installation authority.

Each run gets a newly instantiated Guest module. The Host owns timeout, memory and byte limits. Static imports must appear as simple top-level single-line statements in the initial import preamble. Dynamic, nested, relative, multiline, compound, or late imports fail closed. The trusted CPython Guest validates source again before execution.

See [docs/threat-model.md](docs/threat-model.md) and [docs/source-compatibility.md](docs/source-compatibility.md).

## Repository map

```text
cmd/apyrun/                 JSON stdin/stdout CLI
runtime/                    request, profile and response contracts
runtime/engine/wazero/      fresh-instance WASI execution
runtime/engine/native/      Experimental verified one-shot OCI/gVisor execution
runtime/placement/          deterministic Host-owned execution placement
runtime/capability/         small Host tool registry and broker
runtime/capabilityrpc/      Experimental private native transport adapter
runtime/compensation/       Experimental reviewable tool compensation contract
runtime/lifecycle/          backend-neutral lifecycle/resource evidence
runtime/verification/       independent native evidence verifier
runtime/workspace/          bounded WASI workspace mount and capsule storage
runtime/receipt/            Host-authored call receipts
runtime/observe/            optional bounded Host observation contract
runtime/playback/           Bundle and Experimental branch contracts
research/labstore/          Experimental local content-addressed store
research/operator/          Experimental semantic research APIs
cmd/pysolate-research/      Experimental local research CLI
guest/                      CPython/WASI Guest source and build
abi/v1/                     active JSON schemas
integration/e2e/            focused real-Guest checks
docs/                       active design and historical summary
```

## Verification

```bash
go test ./...
go vet ./...

AGENT_RUNTIME_GUEST=/path/to/agent-python-runtime.wasm \
  go test ./integration/e2e -count=1
```

With the artifact configured, focused real-Guest tests cover ordinary Python,
timeout recovery, workspace isolation, automatic import admission, Host
receipts, Runtime observations, two-source capture/offline playback,
Experimental/Partial deterministic repeats, and fresh counterfactual branch
children through the operator API. These focused tests do not by themselves
claim that the full combined research acceptance workflow has passed.

## Scope

This is a concept verification, not a mature multi-tenant service. The current design prefers small, inspectable code and conservative rejection over compatibility with every valid Python program or operational edge case.
