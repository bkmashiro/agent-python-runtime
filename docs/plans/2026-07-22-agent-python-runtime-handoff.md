# Agent Python Runtime standalone implementation handoff

> **For the implementing Hermes session:** Treat this document as the source of truth. The parent/controller owns architecture, scope decisions, review, and final gates. Use TDD, small signed local commits, and real Linux/WASI artifact evidence. Do not create a remote, push, deploy, publish a package, or use paid infrastructure without explicit approval.

**Goal:** Build an independent, capability-controlled Python execution runtime for AI agents that can run generated Python against host-mediated tools without ambient host authority.

**Architecture:** A versioned CPython/NumPy WASI guest runs inside a neutral Go/wazero host. The host owns capabilities, budgets, lifecycle, state reset, and receipts. Agent orchestration remains outside the runtime and chooses between direct tool calls and a Python run.

**Tech stack:** Go 1.24.x, wazero, CPython 3.14, NumPy WASI subset, WASI SDK 33, `wasm32-wasip1`, JSON ABI, GitHub Actions or equivalent native Linux verification.

---

## 1. Project identity and independence

Workspace:

```text
/Users/yuzhe/projects/agent-python-runtime
```

Provisional Go module path after implementation starts:

```text
github.com/bkmashiro/agent-python-runtime
```

This is a standalone AI Agent execution-infrastructure project.

- Do not import or depend on another product's runtime or protocol.
- Do not integrate with unrelated products in the first implementation.
- Do not copy source from a repository whose redistribution rights are unclear.
- Prior runtime experiments may inform requirements and tests, but they are not product dependencies or part of this project's public narrative.
- `/Users/yuzhe/projects/webassembly-language-runtimes` is an Apache-2.0 artifact-build reference only. Its current Python guest contains domain-specific request handling and must not be copied as the neutral runtime contract.

Before copying any reference code, record its exact source commit, license, changed-file notices, and required attribution in `NOTICE.md`. Independent implementation is preferred when provenance is ambiguous.

## 2. Technical positioning

Canonical description:

> A capability-controlled Python execution runtime for AI agents, using a prepared CPython/NumPy WASI guest and host-mediated tool access.

The runtime is useful when one agent action needs deterministic local control flow such as:

- batching several reads;
- loops and conditional processing;
- joining or filtering multiple results;
- NumPy-based transformations;
- retaining large intermediate payloads outside model context.

Python is not mandatory for every action. A simple operation may remain one direct tool call. Code-versus-tool selection belongs to the agent harness, not this runtime.

The runtime's value is not merely `exec(code)`. It must prove that:

1. guest code has no ambient host authority;
2. every external operation crosses an enforced Host capability boundary;
3. capabilities and secrets remain Host-owned;
4. each internal operation is separately budgeted and receipted;
5. run-local state cannot contaminate the next run;
6. outputs are bounded and schema-checked before returning to an agent.

## 3. Threat model

Treat generated Python, its inputs, and its requested capability names as untrusted.

The first version protects the Host from:

- arbitrary filesystem reads/writes;
- inherited environment variables and secrets;
- arbitrary network access;
- subprocess/exec access;
- unbounded memory growth;
- unbounded CPU or wall-clock execution;
- unbounded stdout/stderr/result payloads;
- excessive or undeclared Host tool calls;
- stale interpreter/module state crossing run boundaries;
- invalid guest pointers and response lengths.

Out of scope for the first version:

- kernel or hypervisor escape resistance comparable to a hardened microVM platform;
- arbitrary native extensions or runtime `pip install`;
- full POSIX behavior, shell sessions, background processes, or PTYs;
- write-side external effects;
- human-confirmation workflows and unknown write-outcome reconciliation;
- arbitrary third-party plugin or MCP server installation;
- multi-host scheduling and distributed control planes.

Security language must remain evidence-bound. A manifest that says `network: false` is not enforcement. If the backend cannot enforce a requested capability boundary, it must reject the run.

## 4. State model

Keep three state classes separate.

### Prepared base state

Reusable and trusted:

- CPython bootstrap;
- approved standard library files;
- verified NumPy subset;
- trusted runtime SDK module;
- optional trusted preparation code.

Capture only at a quiescent boundary after initialization returns to the Host.

### Run-local state

Ephemeral and untrusted:

- Python variables and imported modules;
- temporary arrays and buffers;
- intermediate capability results;
- guest stdout/stderr;
- per-run counters and receipts.

Reset after every successful or failed run. If reset cannot be proven, discard the instance instead of returning it to the pool.

### Cross-run Artifact state

Explicit and deferred until after the execution kernel is proven.

The initial runtime may return immutable bytes/JSON plus a digest. A durable Artifact store, TTLs, ownership, pinning, Arrow/Parquet, and cross-turn retrieval are later work and must not block the first vertical slice.

Never use arbitrary Pickle or a persistent mutable interpreter heap as the durability contract.

## 5. Layer boundaries

```text
┌─────────────────────────────────────────────────────────────┐
│ Agent harness                                                │
│ Chooses direct tool call vs Python run                       │
└──────────────────────────────┬──────────────────────────────┘
                               │ RunRequest + Host RunConfig
┌──────────────────────────────▼──────────────────────────────┐
│ Neutral Go runtime                                           │
│ ABI, pool, lifecycle, limits, cancellation, receipts         │
└──────────────────────────────┬──────────────────────────────┘
                               │ WASM exports/imports
┌──────────────────────────────▼──────────────────────────────┐
│ CPython/NumPy WASI guest                                     │
│ Bootstrap, generated-code execution, runtime SDK             │
└──────────────────────────────┬──────────────────────────────┘
                               │ named capability requests
┌──────────────────────────────▼──────────────────────────────┐
│ Host capability broker                                       │
│ Policy, credentials, budgets, real I/O, per-call receipts    │
└─────────────────────────────────────────────────────────────┘
```

The guest never decides what authority it has. Capability grants and budgets are supplied through Host-owned `RunConfig`, not accepted as trusted fields inside model-generated JSON.

## 6. Provisional contracts

The implementing session must write ADRs before freezing public identifiers.

### Host API direction

```go
type Runtime interface {
    Start(context.Context) error
    Run(context.Context, RunRequest, RunConfig) (RunResult, error)
    Close(context.Context) error
}
```

Untrusted request:

```go
type RunRequest struct {
    RunID        string
    Code         string
    Inputs       json.RawMessage
    OutputSchema json.RawMessage
}
```

Host-owned configuration:

```go
type RunConfig struct {
    Capabilities []CapabilityGrant
    Budget       Budget
}
```

`Budget` must eventually cover wall time, memory pages/bytes, output bytes, tool calls, and per-tool timeout. Unsupported limits fail closed.

### Guest ABI direction

Recommended minimal exports for ADR review:

```text
_initialize()
runtime_init(ptr, len) -> status
runtime_prepare(ptr, len) -> status       # optional trusted preparation
alloc(len) -> ptr
dealloc(ptr)
execute(ptr, len) -> response_ptr         # [u32 little-endian length][JSON]
```

Names may change in the ABI ADR. The v1 contract must not contain product-specific operation names or domain semantics.

### Initial result shape

```json
{
  "status": "ok|error",
  "result": {},
  "receipts": [],
  "metrics": {},
  "error": null
}
```

Do not include `needs_confirmation` in v1 because write-side operations are out of scope.

### Capability direction

The first Python SDK surface should be narrow:

```python
from agent_runtime import tools

rows = tools.fetch_many(requests)
```

The exact guest-to-Host import ABI belongs in an ADR. It must define:

- memory ownership and maximum request/response lengths;
- named capability resolution;
- per-call timeout and cancellation;
- structured partial failures;
- deterministic receipt identity;
- behavior when the capability is absent or exhausted;
- prohibition on guest-supplied credentials or arbitrary URLs outside an allowlist.

## 7. Target repository shape

```text
agent-python-runtime/
├── README.md
├── NOTICE.md
├── go.mod
├── cmd/apyrun/
├── abi/v1/
│   ├── request.schema.json
│   ├── response.schema.json
│   └── fixtures/
├── guest/
│   ├── include/
│   ├── src/
│   ├── bootstrap/
│   ├── tests/
│   └── build/
├── runtime/
│   ├── abi/
│   ├── engine/
│   ├── pool/
│   ├── snapshot/
│   ├── capability/
│   └── receipt/
├── integration/
│   ├── fixtures/
│   └── e2e/
└── docs/
    ├── architecture.md
    ├── threat-model.md
    ├── adr/
    ├── evidence/
    └── plans/
```

Do not create empty package trees preemptively. Add a directory only when its first tested behavior is implemented.

## 8. Implementation tranches

### Tranche 0: Freeze scope, provenance, and contract

**Create:**

```text
docs/architecture.md
docs/threat-model.md
docs/adr/0001-runtime-boundaries.md
docs/adr/0002-guest-abi-v1.md
NOTICE.md
abi/v1/request.schema.json
abi/v1/response.schema.json
abi/v1/fixtures/
```

Requirements:

- record the standalone project boundary;
- document untrusted request versus Host-owned grants;
- define unknown-field behavior;
- define pointer/length and maximum-output rules;
- define state-reset and instance-discard rules;
- record every reference artifact/source by commit, license, and digest;
- leave public remote/package publication blocked.

Gates:

```text
JSON schemas parse
positive fixtures validate
unknown fields and authority-bearing inputs fail
markdown links resolve
git diff --check
```

Commit after the contract is reviewed.

### Tranche 1: Build a neutral Python/WASI guest

Start with pure Python, then add the verified NumPy subset.

Requirements:

- neutral `execute(code, inputs)` behavior;
- no product-specific handler names;
- structured Python errors with type and bounded traceback;
- no runtime package installation;
- exact artifact input versions and digests;
- manifest records ABI version, exports/imports, packages, output SHA-256, and build provenance.

Tests first:

- simple expression and JSON input;
- exception path;
- output-size rejection;
- missing/invalid request fields;
- mutable module/global state canary;
- NumPy import and one deterministic array operation;
- unsupported package returns a clear error.

Authoritative gates must run on Linux:

```text
artifact is WebAssembly
exact exports/imports match the ABI
pure Python smoke passes
NumPy smoke passes
no ambient network/filesystem/env imports are granted
manifest and checksum are deterministic
```

Do not call a native Python test WASI evidence.

### Tranche 2: Implement the minimal Go host

Create only the packages required for one complete run.

Initial behaviors:

- load and compile one pinned guest artifact;
- instantiate with no inherited stdio, environment, filesystem, or network;
- enforce memory/page and output bounds;
- context cancellation closes unhealthy instances;
- validate guest pointers and length-prefixed responses;
- capture a quiescent prepared snapshot;
- restore state after success and error;
- detect memory-size drift and discard/replace the instance;
- maintain a bounded pool and clean shutdown.

Tests first:

- valid ABI fixture;
- bad pointer/length fixture;
- infinite loop/timeout fixture;
- memory-growth fixture;
- output flood fixture;
- repeated-request state canary;
- error-path reset;
- pool concurrency and shutdown race.

Gates:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Run Linux-specific snapshot and denial gates on native Linux CI. Apple-Silicon local results are development evidence only.

### Tranche 3: Add one real read-only capability

Implement `fetch_many` as the only initial external capability.

Use deterministic local fixtures for unit tests, then one allowlisted real HTTP/JSON service for the end-to-end proof. Guest code must not receive a network socket or credential.

Required evidence:

- no grant: capability call denied;
- matching grant: bounded request succeeds;
- arbitrary destination: denied;
- per-call and total-call budgets enforced;
- partial failure has stable structured output;
- timeout cancels without recycling an unhealthy instance;
- credentials remain Host-side;
- each internal operation emits a receipt;
- guest cannot bypass the broker with direct network access.

Do not add writes, arbitrary MCP servers, vendor SDKs, or nested model calls.

### Tranche 4: Build the canonical Agent workflow proof

Demo:

```text
one outer Python Run
→ five allowlisted reads
→ Python/NumPy combines results
→ intermediate payloads stay outside model context
→ final JSON + receipts returned
→ next run proves state freshness
```

Compare with a baseline using five model-visible direct tool results.

Measure separately:

- model-visible tool calls;
- LLM turns;
- model input/output tokens;
- intermediate bytes exposed to the model;
- end-to-end latency;
- guest execution time;
- internal capability-call count;
- result correctness;
- next-run freshness;
- server ready, first request, and steady request latency.

Do not describe reduced model-visible calls as reduced underlying API operations. Do not compare persistent warm state with fresh execution without naming the lifecycle.

Required deliverables:

```text
docs/evidence/<date>-agent-workflow.json
docs/evidence/<date>-agent-workflow.md
integration/e2e/...
```

The report must link to raw evidence and exact source/artifact digests.

### Tranche 5: Decide whether Artifacts are justified

Do not implement a general Artifact store automatically.

Proceed only if the canonical workflow needs data across runs and returning immutable bytes/JSON is insufficient. If activated, start with owner/session-scoped immutable blobs, digest identity, byte size, source run, TTL/pin metadata, and capability-scoped reads. Mutation creates a new version.

Arrow/Parquet waits for an actually verified runtime/package path.

## 9. Security and correctness invariants

A tranche is incomplete unless its relevant invariants are exercised by tests or live denial probes.

- no ambient filesystem, environment, network, subprocess, or secret authority;
- no runtime dependency installation;
- Host—not guest metadata—enforces every grant;
- unsupported capabilities are rejected before execution;
- memory, time, output, and tool-call budgets are hard bounds;
- output limits are enforced while reading, not only after buffering;
- cancellation cannot return an unhealthy instance to the pool;
- guest pointers and sizes are bounds-checked;
- run-local state resets after both success and failure;
- memory shape drift causes instance discard/replacement;
- external operations produce bounded receipts;
- cross-run state is explicit and immutable;
- artifact/source inputs and output digests are pinned;
- benchmark claims name lifecycle, workload, and endpoints.

## 10. Performance claim boundaries

Do not market this as universal Python acceleration or millisecond cold start.

The design intentionally prepays CPython/package initialization and compilation, then amortizes it over a prepared pool. Report:

1. artifact download/cache state;
2. compile time with and without cache;
3. server-ready time;
4. first request;
5. steady request;
6. memory footprint per prepared instance;
7. restore cost versus guest work;
8. break-even request count where preparation is amortized.

The first product claim is safe capability-mediated composition with fresh state. Runtime speed is secondary and must come from the new project's own evidence.

## 11. Stop conditions

Stop and report instead of guessing when:

- a required source/artifact license or provenance is unclear;
- the neutral ABI would require importing product-specific semantics;
- a requested capability cannot be enforced by the selected backend;
- CPython/NumPy artifact reproduction fails on native Linux;
- state reset cannot cover observed mutable state;
- output or CPU limits cannot be made hard bounds;
- a public module/repository name must be chosen;
- deployment, remote push, package publication, or paid infrastructure is needed;
- the proposed slice expands into an agent planner, MCP marketplace, or general Linux sandbox.

## 12. Handoff prompt

```text
Work in /Users/yuzhe/projects/agent-python-runtime. Read README.md and docs/plans/2026-07-22-agent-python-runtime-handoff.md as the source of truth. This is a standalone AI Agent execution-infrastructure project with no dependency on or integration with unrelated systems. Verify the repository status first. Start with Tranche 0 only: write and review the architecture, threat model, provenance notice, ABI ADRs, JSON schemas, and positive/negative fixtures. Use TDD and small signed local commits. Do not create a remote, push, deploy, publish, use paid infrastructure, or start broad implementation until the contracts and license/provenance gate are complete. After each coherent slice, run the listed gates, inspect the diff, update the handoff status, and continue only within the accepted tranche.
```

## 13. Preparation status

Prepared as a documentation-only local repository handoff.

No runtime source, guest artifact, remote repository, release, deployment, hosted service, or public package has been created. The first implementing session must verify this status before starting Tranche 0.
