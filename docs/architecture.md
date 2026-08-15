# Architecture

## Purpose

Pysolate proves that a programming Agent can submit normal Python while the Host retains all authority. It is one execution component, not an Agent planner, package platform, scheduler, transaction service, or general Linux sandbox. Its Current implementation and longer-term Python-native capability-computer direction are separated in [product-direction.md](product-direction.md).

Unless a paragraph says otherwise, the core Run path below is **Current**.
Counterfactual branching, deterministic verification, prepared/COW execution,
and unified execution profiles are marked **Experimental**. The seeded workflow Harness is
**Research-only**; its balanced-order real-Guest v1 result is inspectable through a private
trajectory and makes no CPU/latency improvement claim. The private append-only Agent
trajectory recorder and static inspector are **Experimental**. A concrete live-Harness
attachment, resume, fork and replay are host-owned integration concerns, not the current
Pysolate Runtime roadmap. See [research/agent-trajectory-v0.md](research/agent-trajectory-v0.md).

## Run path

1. Decode a bounded `RunRequest`.
2. Reject authority-bearing request fields and unsupported requirements.
3. Derive simple static import roots from the Agent source.
4. Bind those roots to a Host-owned execution profile and verified Guest distribution.
5. Instantiate a fresh wazero module with bounded memory and timeout.
6. Initialize CPython and apply optional trusted Host preparation.
7. Ask the Guest to validate the Python source contract.
8. Execute the request with an optional per-run Host tool Broker.
9. Replace Guest receipt/metric claims with Host-authored values and emit any
   validated capability observations.
10. Close the Guest module, inspect final workspace state, then emit final
    workspace and terminal observations when enabled.
11. Close remaining per-Run resources.

A failed, trapped, timed-out, or successful module is never reused.

## Authority split

### Agent request

The Agent may provide:

- `run_id` as a diagnostic label;
- Python `code`;
- JSON `inputs`;
- an optional JSON `output_schema`;
- compatibility requirements that can only narrow admission.

### Host configuration

The Host owns:

- artifact and manifest selection;
- execution profile and allowed imports;
- timeout, memory and byte limits;
- workspace contents;
- tool registration and call budget;
- trusted preparation code;
- receipt identity.

Optional Host context also owns logical invocation/retry coordinates, the
physical execution identity, and the observation Session. None is accepted
from `RunRequest`.

## Python compatibility

`BindAgentSource` derives imports and writes the compatibility declaration. Agent-facing callers therefore submit code, not bookkeeping metadata.

The Host scanner intentionally accepts only a small import preamble. It is not a Python parser. The CPython Guest independently parses and validates the source before execution. Conservative Host rejection is acceptable for this PoC; accidentally broad admission is not.

## Host tools

The active tool surface uses one generic Guest-to-Host JSON call envelope and a small Host Registry. Each registration binds a canonical `CapabilitySpec`—capability/version identity, documentation, effect/playback declarations, handler identity, strict input/output schemas, Python projection and any optional bounded pre-dispatch qualification—to a Host handler, plus an opaque `CapabilityGrant` identity derived from the exact Host-owned per-Run policy. Before Guest startup, the Host seals the sorted specs, grants and total call budget into an immutable `pysolate.capability-plan.v6`; late registration is rejected. Handler identity remains stable implementation compatibility while changing target policy changes the grant and plan identities. The Broker accepts only that sealed plan, validates arguments before the handler, and validates results before returning them. The CLI generates module objects and compatibility aliases from those same sealed specs:

```python
workspace.read_text(path)      # alias: read_text(path)
workspace.write_text(path, content)  # alias: write_text(path, content)
workspace.list_files()         # alias: list_files()
```

The backing workspace for this typed surface is an in-memory map, not an ambient Host directory. Paths must be canonical and relative. Calls are bounded and produce small Host receipts. Every capability receipt binds the plan identity, and the Host projects that identity into the response even when no tool is called. Guest-authored plan evidence is rejected.

The CLI can also register the dedicated `sources.demo_catalog()` and
`sources.benchmark_manifest()` information sources. Each exact endpoint, GET
method, redirect denial, expected status/media type, timeout and response-byte
ceiling is Host policy bound through its capability grant. The benchmark
source additionally validates a nested versioned research schema, semantic ID
uniqueness, metric direction/unit and bounds. The Agent sees only typed
structured methods, never a URL, transport controls, headers or a generic HTTP
client. These sources can coexist with a mounted `/workspace`; the mount
remains mutually exclusive only with the separate typed in-memory workspace
tools.

Successful calls for a `captured` spec can be projected into a canonical minimal Playback Bundle. The bundle contains validated capability arguments/results and bounded transport attribution plus relation identities; it excludes Agent source, final result and workspace bodies, endpoint policy and credentials. Capture publication happens after successful runner close and final Host response validation using a synced `0600` same-directory stage and atomic no-overwrite publication. Offline mode requires the capture-issued bundle identity in trusted Host config, registers the same sealed Spec/Grant with a non-network placeholder, admits all Host identities before Guest execution, and lets the Broker strictly consume records instead of calling the handler. Finalization rejects unused records and the CLI verifies the fresh Guest's status, result and workspace identities.

The plan also derives defensive direct Agent tool schemas from the same definitions. This generated but deliberately small surface does not restore the former generalized SDK generator, plugin discovery or durable effect workflow.

## Runtime observation

**Current.** `runtime/observe` defines the exact-key, canonical and bounded
`pysolate.runtime-observation.v1` envelope. A Host may attach an `off`,
`best_effort`, or `required` Session through the Run context. Sessions bind the
Host physical `execution_id`, serialize appends, validate causal parents, copy
payloads, and impose per-event and per-execution limits. Runtime owns only the
contract and lifecycle integration; the Recorder and any durable store remain
outside engine policy.

The stable evidence boundary is execution lifecycle, Host Broker calls, and
initial/final workspace snapshots with sorted file-level deltas. Syscall order
is unavailable. Pysolate does not claim Python bytecode, locals, heap, stack,
WASM-memory, or complete filesystem-operation visibility. The measurement and
upgrade policy are in
[research/runtime-observation.md](research/runtime-observation.md).

### Workflow-boundary provenance

**Experimental.** `pysolate.workflow-boundary-observation.v0` is a separate sealed,
body-free relation over explicit workflow nodes and typed Host tool/WASI boundaries. It
records model fixture, WASM and measured Host intervals; maps each logical request to one
physical execution and producer; retains every coalesced/reused logical consumer; and
records admitted or rejected preissue, declared-parallel, coalescing and retained-reuse
decisions. It neither executes a decision nor grants authority. The Runtime mechanisms
remain independently disableable and the all-off path performs ordinary fresh work.

The prepared benchmark Harness may issue an exact necessarily-reached read early or run
explicitly declared-independent nodes concurrently. It does not infer sibling independence
from Python AST structure, spawn implicit tasks, migrate started work, or replay an
ambiguous effect. See
[research/workflow-boundary-observation-v0.md](research/workflow-boundary-observation-v0.md).

### Agent development trajectory

**Experimental.** `pysolate.agent-trajectory.v0` is a private Harness-owned append-only
session log. It records exact ordered model context, provider-exposed reasoning/output,
tool calls/results, subagent links and Pysolate logical/physical/workspace identities. Full
bodies live in the private content-addressed Labstore; the metadata JSONL is hash chained
from a sealed session header. A materialized browser export receives a second seal covering
the complete bodies.

The trajectory is not Runtime authority and is not a portable evidence replacement. The
checked-in browser document is an explicitly scripted credential-free fixture. A live
Harness adapter must still record the actual provider, tool and subagent boundaries before
the Lab may claim complete capture of a real Agent run. See
[research/agent-trajectory-v0.md](research/agent-trajectory-v0.md).

## Counterfactual branches

**Experimental.** `pysolate.playback-branch.v1` identifies a protected parent
Bundle, a capability-operation fork, the exact parent prefix, original
request/artifact/profile/initial workspace, the complete child Plan/Grants,
and one Host-owned suffix mode. A fresh Guest re-executes the original request
and initial workspace, strictly consuming parent operations before the fork.
At and after the fork it consumes schema-validated overrides, a complete
recorded suffix, or live calls through the already sealed child Plan for
captured external reads only.

A branch is not a heap or WASM snapshot. Its child result and final workspace
may intentionally differ from the parent. Parent/manifest/child lineage is a
separate research relation; it is not retrofitted into Playback Bundle v1. See
[playback-bundles.md](playback-bundles.md).

## Deterministic verification

**Experimental/Partial.** The Host may select an artifact-bound
`pysolate.deterministic-verification.v1` profile. The wazero module then gets a
deterministic random stream and virtual wall/monotonic clocks for every fresh
Guest. Mounted workspaces and statically detected concurrency/locale import
classes are denied. The profile identity is included in the execution-profile
binding and Runtime observation.

This is qualified repeatability for identical artifact/profile/input and
captured external inputs. It does not claim cross-platform floating-point
equivalence, concurrent scheduling, mounted-directory ordering, locale
mutation, live-source stability, or complete-Agent determinism. See
[research/deterministic-verification.md](research/deterministic-verification.md).

## Semantic execution optimization

**Experimental and default-off.** The exact packaged Guest can emit a bounded
Python-AST analysis bound to source, artifact, execution profile, admitted import
closure, and capability Plan. `semantic.AnalyzeVerified` accepts only stable valid
Runner properties with the exact artifact/profile binding and no workspace/Broker
authority, then mints detached opaque provenance. The Host decodes only a strict
versioned report,
propagates conservative function summaries through recursive components, and
builds one coarse whole-Run region. Dynamic dispatch, unsupported control flow,
unknown imports/calls, live observation, publication, and suspension are opaque
barriers rather than optimization opportunities.

A thin source-bound planner consumes only `VerifiedAnalysis`, the sealed capability
Plan and frozen per-Run legality context. Passes are individually default-off,
versioned and deterministic; they emit canonical admitted/rejected decisions while
opaque `QualifiedCall` values remain Host-private. Passes have no handler/provider
handles and cannot dispatch. Unknown names or versions and duplicate selections
fail closed; any future multi-pass conflict requires an explicit deterministic policy
before admission. The current semantic pre-dispatch path is the first pass and
still executes through the existing staged-observation/Broker boundary.

The same plan projects one digest-bound Python source document and exact static
capability occurrences. A Host-TCB-only resolver may attach `source_bound` evidence
to a real programmatic call only when capability and canonical arguments select one
unique verified occurrence. Ordinary direct calls, mismatches and ambiguous
occurrences remain explicitly unbound. The receipt identity binds document/source,
static occurrence, dynamic occurrence and line/column span together with the
existing call/parent/approval/Plan/request identity. This provenance does not grant
or change capability authority and is not an executed-line claim.

A separate default-off `ReusePass` may convert only the exact module-entry whole-Run
plan into the existing
Agent Function identity. Analyzer, analysis, Plan and region identities augment
rather than replace source/artifact/profile/import/input/root/deterministic/output/
project/privacy/policy plus the exact compatibility declaration all enter or
constrain the invocation identity; non-empty runtime requirements reject semantic
retention. Compatibility/source admission occurs before cache lookup. The first
physical Fresh Guest still passes the runtime effect probe; only a
successful canonical bounded result with no Host-call attempt can be published.
Single-flight and worker-local retention are independent and remain off unless
explicitly selected. A hit returns immutable result bytes and reports no physical
execution; it does not replay or suppress an effect. See
[content-addressed-agent-functions.md](content-addressed-agent-functions.md) and
[evidence/semantic-reuse-observation.json](evidence/semantic-reuse-observation.json).

Executable AST-region reuse is rejected: the verified 19-program census found 69
candidates but zero statically materializable regions and zero materializable
cross-program overlaps. The original Python program remains the sole execution authority;
the overlay supplies occurrence, legality, rejection explanation and pre-execution
placement facts only. Semantic placement replacement is also `no_go` for this corpus:
zero safe precision gains and 19 regressions versus the current imports/requirements
router. See [research/python-region-census-v0.md](research/python-region-census-v0.md)
and [research/semantic-placement-census-v0.md](research/semantic-placement-census-v0.md).

## Research substrate

**Experimental and outside Runtime core.** `research/labstore` provides a
bounded typed directory CAS, workspace trees, branch relations, read-only
inspection, privacy metadata, retention reachability and measured synthetic
benchmarks. `research/operator` provides semantic Bundle inspect/compare,
branch-DAG export, and a fresh-Guest branch API. The separate
`pysolate-research` CLI currently exposes bounded human/JSON inspect and
compare, protected branch-manifest planning, caller-supplied branch-DAG export,
read-only store stats, and synthetic store benchmarks. Fresh-Guest branch
execution remains API-only; a complete research workflow CLI and Lab service
are Proposed.

Branch-DAG export validates child admission bindings, the exact parent prefix,
and sealed override/recorded suffix tapes. Live suffix results are not sealed,
and DAG validation is not proof that a result was produced by executing a
manifest; the Host-owned outcome relation remains the execution evidence.

## Unified execution profiles

**Experimental.** `runtime/placement` can select a positively qualified
`pysolate_wasm` invocation or a separately governed `native_sandbox` invocation
before either backend starts. `runtime/capabilityrpc` projects the same sealed
Plan/Broker through a private invocation-bound Unix-socket protocol for native
CPython. This does not make the profiles equivalent: native execution has a
wider local compatibility surface and separate artifact/lifecycle evidence.
Only Host-authored `not_started/not_started` rejection permits an implicit
linked native execution; post-start failures never do. See
[unified-execution-profiles.md](unified-execution-profiles.md).

## WASI filesystem

The CLI can alternatively bind a Host-selected `runtime/workspace` lease as `/workspace` plus a fresh `/tmp`. The request cannot select the backing Host path, Capsule path, workspace limits or final disposition. Mounted workspaces and the typed in-memory workspace tools are mutually exclusive. The Host may restore or snapshot a complete Capsule and must explicitly choose `export_on_success`, `export_on_response` or `discard`; see [workspace-capsules.md](workspace-capsules.md).

## Active packages

```text
runtime/engine             runtime-neutral Runner contract
runtime/engine/wazero      fresh Guest implementation
runtime/capability         generic bounded Host calls
runtime/capabilityrpc      Experimental private native transport
runtime/placement          Experimental Host placement and L1/L2 orchestration
runtime/workspace          optional rooted WASI workspace
runtime/receipt            compact Host call receipts
runtime/observe            bounded optional Host observation contract
runtime/playback           protected Bundle and Experimental branch contracts
runtime                    request/profile/artifact/response contracts
cmd/apyrun                 operator and PoC CLI
research/labstore          Experimental local CAS and retention prototype
research/operator          Experimental semantic research APIs
cmd/pysolate-research      partial local research CLI
```

## Approval continuation and programmatic tool calling

**Implemented hot v0; cold tiers remain Proposed and default-off.** Programmatic
tool calling, approval suspension, approval leases, audit records and future
continuation memory tiering are separate mechanisms with explicit dependency
validation. Programmatic calls re-enter the same Host-owned capability plane;
they do not receive ambient authority or imply approval, caching, replay or cold
memory. A bounded Plan-bound approval lease can preserve one real pending ABI
call in the same Wazero/CPython execution. Rejection and expiry do not dispatch
the handler; cancellation that wins the explicit dispatch-commit gate also stays
pre-dispatch. Pysolate does not claim complete arbitrary
CPython/Wazero snapshot and restore.

The source-backed DeepSeek Harness PTC comparison, lifecycle, package boundaries,
independent controls and falsifiable slice order are recorded in
[approval-continuation-and-programmatic-tool-calling.md](research/approval-continuation-and-programmatic-tool-calling.md).

## Authority-transparent campaign status

**Current:** the Runtime packages remain campaign-agnostic. Authority attenuation,
private workspace branches, exact invocation sharing, sealed roots and fresh workflow
resume are generic primitives.

**Observed:** `research/workflowbench` composed those primitives for a fixed 20-program
real-Guest campaign. Five balanced pairs preserved all registered oracles and authority
rejections while qualified execution reduced physical work from 19 to 17 executions.
The measured scope and source identities are recorded in
[authority-transparent-campaign-results.md](research/authority-transparent-campaign-results.md).

**Deferred:** no production scheduler, semantic workspace merge, arbitrary-Python reuse,
live Harness attachment or general performance conclusion follows from this campaign.

## Explicit non-goals

- served-instance reset or reuse;
- production scheduling of prepared/COW execution (the bounded Wazero mechanism remains Experimental);
- durable effect transactions and compensation;
- generic network tools, Guest-controlled URLs or credentials;
- MCP/daemon/plugin lifecycle;
- production scheduler and benchmark-study orchestration;
- package installation and native extensions;
- production recovery, migration or multi-tenant operations.
