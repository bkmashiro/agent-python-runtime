# Product direction: an authority-lifecycle runtime

## Status and baseline

This document separates the verified implementation from product direction.
`Current` statements describe maintained code and tests present in the
repository. `Experimental` statements describe implemented, bounded prototypes
whose limitations remain part of their contract. `Proposed` statements are
decision constraints for later work, not claims that the named capability,
record, replay mode or guarantee exists today. Release or evaluation records
should pin a concrete commit rather than treating this living document as a
version identifier.

Pysolate's refined long-term direction is:

> A unified effect-aware authority-lifecycle runtime for semantically inspectable
> Agent-authored programs: ordinary Python provides control flow; the exact target
> Guest exposes a bounded analyzable subset; and the Host freezes authority while
> independently governing semantic legality, interpreter, workspace, scratch,
> external-effect, evidence and placement dispositions.

The active paper and evaluation contract is the
[Effect-Compiler Paper and Evaluation Source of Truth](plans/2026-08-16-effect-compiler-paper-evaluation-source-of-truth.md).
The earlier [Unified Effect-Aware Runtime Megagoal](plans/2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md)
and its [architecture recommendation](research/unified-effect-aware-runtime-architecture.md)
remain implementation/research records. New feature work is frozen while the current
authority-bound effect compiler is evaluated on a bounded natural-task cohort.

A complementary **Experimental** feature layer is
[content-addressed Agent Functions](content-addressed-agent-functions.md):
selected explicit Host-instrumented computations use canonical project-private
result reuse and independent single-flight over immutable input identities.
Arbitrary Guest-Python purity admission remains Deferred; live I/O and writes
remain typed Host effects. This does not replace the authority-lifecycle
direction; fresh execution prevents hidden interpreter state from becoming
workflow continuation state.

The primary Agent-specific execution history is
[streaming authority-staged execution](streaming-authority-staged-execution.md).
A minimal mechanism now incrementally validates and executes append-only Python
inside one live target-Guest session, stages filesystem changes in a private
workspace attempt, dispatches reads only when unchanged Python reaches their Broker
boundary, and enforces pre-seal write denial. The historical literal eager-read
metadata path is disabled; the current successor is exact source-bound semantic
pre-dispatch after verified legality. Bounded Experimental
successors now add portable immutable roots, structured recursive child
orchestration, exact AST-qualified whole-Run Agent Function reuse/single-flight,
explicit single-wait fresh re-evaluation, one never-served single-use prepared
module, bounded Linux private-memory COW, and continuation-preserving cold-I/O.
General arbitrary-Python purity, production fan-out scheduling, automatic
cold/reuse policy, generalized write commit/reconciliation, and broad performance
claims remain Deferred or Proposed. The next approved runtime work is bounded by the
[Host-Scheduled Calls and Immutable Value Reuse Mega-Goal](plans/2026-08-24-host-scheduled-python-reuse-autonomous-megagoal.md),
which explicitly rejects a user-visible Future, a full Python/DataOp IR and broad
performance claims without retained end-to-end evidence.

Optional mechanisms must remain orthogonal at their public contract boundaries.
Result caching, single-flight, workflow re-evaluation, immutable workspace
branching, private attempts, playback, external-write lifecycle, prepared
runtime, memory COW, verification, and Lab projection each require an explicit
off-state and tested fallback. Historical dependency and composition records remain
in the [composable mechanism roadmap](proof-first-authority-roadmap.md); current
status and future evaluation decisions come only from the active source of truth.

Generic code execution is necessary infrastructure, not the differentiator.
Sandboxed code, mediated connectors, ledgers, approval replay, and compensation
are also not standalone differentiators. The candidate contribution is an
identity-bound conjunction of least authority, private attempt state, honest
effect ambiguity, terminal disposition, and verification. See
[the authority-lifecycle ADR](authority-lifecycle-positioning.md) and the
[Cloudflare comparison reset](research/cloudflare-code-mode-comparison.md).

## Positioning

A general Computer places an Agent next to a persistent OS environment containing a shell, arbitrary binaries, processes, package managers, network state and broad implicit configuration. Pysolate instead gives an Agent a persistent file-oriented workspace and repeated bounded Python executions.

```text
Agent / Harness
      |
      | ordinary Python + JSON inputs
      v
fresh CPython/WASI Guest
      |
      | rooted /workspace and one narrow Host-call ABI
      v
Host workspace, capability broker and evidence
```

The Agent and Guest are separate components coordinated by the Host. The Agent proposes source and consumes results; the Guest executes one admitted program and may invoke only Host-granted capabilities. They are not authority peers: the Host owns the artifact, profile, budgets, workspace binding, capability plan and state disposition.

The product may feel computer-like because files and useful operations remain available across steps. It must remain capability-native internally. Pysolate is not a smaller Linux VM, a shell emulator or an environment that silently expands permissions after an execution failure.

A source-pinned assessment of the distance from the present prototype to the
research, local-alpha and production finish lines is maintained in
[product-maturity-and-roadmap.md](product-maturity-and-roadmap.md).

## Current implementation

The current implementation provides:

- a fresh CPython/WASI Guest for every Run;
- bounded request, response, memory and wall-clock execution;
- Host-derived import admission bound to a verified Guest distribution;
- a Host-selected private rooted `/workspace` and fresh `/tmp`;
- complete Workspace Capsules for explicit storage, migration and restoration;
- Host-selected `export_on_success`, `export_on_response` or `discard` disposition;
- a generic Guest-to-Host JSON call ABI, Host Registry and bounded Broker;
- canonical Host-owned `CapabilitySpec` definitions for capability/version identity, documentation, effect class, playback treatment, handler identity, strict input/output schemas, Python wrapper projection and optional bounded pre-dispatch qualification;
- opaque per-Run `CapabilityGrant` identities derived from canonical Host policy documents;
- a sealed `pysolate.capability-plan.v7` identity binding sorted canonical specs, their per-Run grants and the total call budget;
- generated Python module/method objects and direct Agent tool schemas from the sealed specs, with compatibility aliases for the three current workspace functions;
- two credential-free, dedicated external-read sources backed by Host-private exact-endpoint GET adapters with redirect, status, media-type, timeout and byte controls: `sources.demo_catalog()` and the nested versioned `sources.benchmark_manifest()` research manifest;
- canonical minimal Playback Bundle v1 capture and strict offline consumption for curated external reads, with validated capability payloads, bounded transport evidence and plan/grant/request/artifact/profile/status/result/workspace identities; publication is staged `0600`, synced and atomic, trusted Host config anchors the capture-issued bundle identity, playback constructs no HTTP adapter and verifies final identities;
- the bounded Host-authored `pysolate.runtime-observation.v1` contract, including lifecycle evidence for no-Broker Runs, capability and profile references, optional Host Recorder modes, and initial/final file-level workspace deltas with syscall order explicitly unavailable;
- compact Host-authored capability and workspace receipts; capability receipts and the top-level Host response bind the sealed plan identity, including for zero-call Runs.

The generic Registry can register a versioned spec, grant and handler without changing the WASI import surface. Registration canonicalizes and compiles both schemas. The Host seals that Registry before Guest startup; late registration is rejected and the Broker accepts only the resulting immutable plan. The current CLI generates namespaced Python objects and optional compatibility aliases from the sealed specs, while the Plan exposes direct Agent tool schemas from the same definitions. A plugin catalog and installation lifecycle are not Current.

Current receipts and observations bind call, lifecycle and workspace identities,
but the repository does not claim a durable complete audit archive, full
deterministic replay, external-effect reconciliation or transaction semantics.

## Experimental research implementation

The repository also contains bounded **Experimental** work whose label is part
of its contract:

- `pysolate.playback-branch.v1` counterfactual branches at captured
  capability-operation boundaries, with a strictly replayed parent prefix and
  Host-owned override, recorded, or sealed live external-read suffix;
- `pysolate.deterministic-verification.v1`, explicitly
  **Experimental/Partial**, which binds an exact artifact and controls wazero
  random plus wall/monotonic clocks while denying mounted workspaces and known
  unsupported import classes;
- `runtime/composable`, a strict body-free evidence decoder/verifier that accepts
  only claims supported by versioned mechanism records;
- `runtime/workspace` portable immutable root/lineage records and explicit select,
  plus `runtime/subagent` bounded structured fork/join with private child branches;
- `runtime/agentfunction` project-private Host-instrumented result retention and
  independent single-flight; this is not arbitrary Guest-Python purity;
- `runtime/workflow` single-wait fresh re-evaluation over explicit immutable state;
- the Wazero backend's optional one-slot, never-served, single-use prepared module
  with fresh fallback, plus an Experimental Linux `cow-fixed` lane whose sealed
  baseline produces one MAP_PRIVATE slot per request and always discards it;
- `research/labstore`, an independent bounded directory CAS prototype with
  typed identities, workspace/branch relations, privacy policy, read-only
  access, reachability retention and measured synthetic benchmarks;
- `research/operator`, which can inspect/compare Bundles, export bounded branch
  DAGs and run a branch in a fresh Guest from an explicitly sealed Host Plan;
- a separate local `pysolate-research` CLI with bounded human/JSON inspect and
  compare, protected branch-manifest planning, caller-supplied branch-DAG
  export, read-only store stats, a synthetic store benchmark, and read-only
  canonical Lab v1 projection from a strict evaluation report plus matching
  body-free measurement summary. Missing relations remain explicitly
  private/unavailable; the projection does not execute a branch;
- the fixed three-workload mechanism-only evaluation runner and body-free report
  rebuild/measurement contracts. Its bounded observed result and prohibited
  claims are recorded in
  [research/workload-evaluation-v1.md](research/workload-evaluation-v1.md).
  This evidence does not promote the runner, LabStore or Lab projection to the
  Current Runtime surface.

These pieces establish a local research substrate, not a complete Pysolate Lab
or a release claim. The Runtime/Lab ownership boundary is documented in
[research/lab-boundary.md](research/lab-boundary.md).

## Governing design rules

### No shell

Pysolate will not expose an ambient shell or arbitrary subprocess API. Ordinary Python supplies branching, loops, structured data handling, errors and composition. Common computer operations should be ordinary Python libraries or typed Host capabilities:

```python
from pathlib import Path

matches = [path for path in Path("/workspace").rglob("*.py")
           if "TODO" in path.read_text(encoding="utf-8")]
status = git.status()
Path("/workspace/report.txt").write_text("\n".join(map(str, matches)), encoding="utf-8")
result = {"matches": [str(path) for path in matches], "status": status}
```

Ordinary file operations above lower through Python/WASI. The generated
`git.status()` proxy crosses the typed Host Broker because repository semantics
operate on a separately Host-selected authority.

### One canonical capability definition

Current `CapabilitySpec` defines:

- stable capability and version identity;
- Agent-facing documentation;
- a Python module/method projection with an optional compatibility alias;
- strict input and output schemas;
- Host handler identity;
- declared effect class and playback treatment.

Registration canonicalizes and compiles both schemas. It also requires an opaque `CapabilityGrant` identity derived from a canonical Host-owned policy document. Sealing binds every sorted spec, its grant identity and the global budget into `pysolate.capability-plan.v7`; changing a per-Run target policy changes the plan without overloading the stable handler identity. The Broker validates arguments before handler invocation and validates results before returning them to the Guest. The CLI generates trusted module objects and optional aliases from the same sealed specs, and the plan exposes defensive direct Agent tool schemas from those definitions rather than maintaining handwritten second surfaces.

The following remain Proposed extensions:

- qualified source adapters beyond the two current credential-free sources,
  and any credential-bearing adapter;
- per-target external-cost budgets;
- external-write intent/effect records and reconciliation;
- a plugin catalog, durable research service and full Lab UI.

The replay adapter should derive from that definition as well. Generated presentation surfaces do not create a second policy path: execution still crosses the same Broker.

### Dynamic catalog, frozen per-Run authority

Current Pysolate freezes and identity-binds the registered canonical specs, per-Run grant identities and total call budget before Guest startup. Grant identities are opaque digests: authority-bearing policy bytes remain Host-private. The Host may construct a different Registry between Runs. A future plugin catalog may install or qualify implementations from which Host policy selects each Run's subset; plugin discovery and installation are not Current.

Handler identities are trusted Host-owned declarations. A plugin or registry builder must change the identity whenever behavior relevant to authorization, side effects or replay compatibility changes; Guest code cannot supply or override it.

A capability must not appear or gain authority midway through a Run. If more authority is required, the current Run terminates with an explicit unsupported or escalation outcome; the Host may checkpoint the workspace and construct a new Run with a newly approved plan. Ordinary Python exceptions never trigger automatic authority escalation.

### Persistent explicit state, disposable hidden state

The default lifecycle remains:

```text
persistent Host workspace
  -> fresh Guest Run
  -> persistent Host workspace
  -> fresh Guest Run
```

Workspace files are inspectable state. Python heap, module globals, WASM
memory, open descriptors, Broker handles, credentials and `/tmp` are not
continuation state. Pinned interpreter sessions are outside the Current and
Experimental Runtime paths. Any separately governed compatibility environment
must not silently change the fresh-per-Run contract or become branch state.

### Computer-last compatibility

Coverage should grow by qualifying Python packages and Host-mediated semantic capabilities, not by reopening ambient shell, socket, credential, subprocess, installer or Host-filesystem authority.

Irreducibly native, interactive or long-lived work may use a separately governed Computer/VM selected before execution. Escalation must be explicit, policy-authorized and state-revision-bound. Pysolate does not catch an execution failure and automatically rerun the program with broader authority.

## Authority and observability boundary

Every external authority available to Guest code must cross one of the Host-controlled boundaries:

- canonical Run input and output;
- rooted WASI filesystem projection;
- the generic Host-call ABI;
- explicitly configured clock, randomness and resource interfaces;
- Host cancellation and lifecycle control.

This makes authority crossings observable and enforceable. It does not imply instruction-level tracing or visibility into every Python local variable. Pure Guest computation may remain opaque; its admitted source, initial state, external interactions, resource outcome and final state are the relevant evidence surface.

Adding a capability therefore means adding a bounded Host adapter, not giving the Guest a raw provider client:

```text
Guest Python call
  -> strict capability envelope
  -> frozen grant and budget check
  -> Host adapter
  -> bounded structured result
  -> Host-authored evidence
```

Credentials remain Host-private. A record may carry a stable credential reference or `[REDACTED]`, never the secret value.

The Current observation boundary is narrower than all WASI activity. Stable
wazero integration exposes Host lifecycle, Broker calls and workspace
snapshots. Pysolate therefore reports bounded initial/final file deltas and
sets syscall-order availability to false. It never claims Python bytecode,
locals, heap, stack or WebAssembly-memory visibility. See
[research/runtime-observation.md](research/runtime-observation.md).

## Evidence model

The intended unit of evidence is a state transition:

```text
Run(
  runtime artifact,
  admitted source and inputs,
  initial workspace,
  frozen capability plan,
  Host policy,
  captured external inputs
)
  -> result, final workspace, capability transcript and evidence
```

A future complete execution record should bind:

- logical invocation and physical execution identities;
- source, canonical input and output-schema identities;
- runtime artifact, manifest, profile and Host implementation identities;
- initial workspace, tree and Capsule identities;
- frozen capability-plan and policy identities;
- ordered capability calls, arguments, outcomes and bounded results;
- lifecycle outcome, metrics and final workspace disposition;
- final result, response, tree and Capsule identities.

Compact receipts and protected replay records serve different purposes. A receipt can expose identities and outcomes without retaining sensitive bodies. Actual playback requires access-controlled source, state and capability-result bytes in a separate record or artifact store. A digest alone proves consistency with later-provided bytes; it does not make those bytes available or establish semantic correctness.

## Replay and determinism vocabulary

Pysolate must not use `replayable` or `deterministic` as unqualified booleans.

### Playback

A playback Run replaces live capability calls with captured, validated responses. Under the same runtime artifact, admitted source, canonical inputs, initial Capsule, policy and capability tape, it may verify the resulting response and final workspace identities without repeating real-world effects.

### Live re-execution

A live re-execution invokes current external systems again. It may test behavior under equivalent declared conditions, but it is not deterministic replay because remote data, service behavior and time may have changed.

### Counterfactual branch

An Experimental branch starts a fresh Guest from the parent's original request
and initial workspace, strictly re-executes the captured prefix, then follows a
complete Host-owned suffix policy. It may intentionally produce a different
result or final workspace. It is not a source-line breakpoint, a heap/WASM
snapshot restore, or authority selected by the Agent at the fork.

### Effect reconciliation

An external write such as a push, publication, message or payment is not replayed. A future Effect Plane must journal an immutable intent before dispatch, use stable provider identity or idempotency where supported, and reconcile ambiguous outcomes. Playback returns the recorded effect state; it never repeats an already applied effect.

### Deterministic verification

The implemented profile remains **Experimental/Partial**. It controls the
wazero clocks and random source for an artifact-bound, no-mounted-workspace
profile and proves repeatability only for qualified real-Guest probes under
identical captured inputs. Concurrency, mounted-directory ordering, locale
mutation, cross-platform floating-point behavior and complete-Agent/provider
execution are outside the claim. See
[research/deterministic-verification.md](research/deterministic-verification.md).

An exact broader claim would require every relevant source to be controlled,
captured or denied and identity-bound, including runtime implementation,
initial state, locale, filesystem ordering and external results. Without that
evidence, the honest claim remains attribution within the stated threat model.

## Capability admission test

A new capability belongs in Pysolate only when all of the following are true:

1. Its maximum authority can be stated as a typed Host contract.
2. Targets, credentials, budgets and effects remain Host-owned.
3. Inputs and outputs are bounded and validate strictly.
4. A Guest cannot use it to obtain a generic shell, socket, provider client or Host path.
5. Its handler and policy versions can be identity-bound.
6. Its calls can produce useful receipts and a defined playback treatment.
7. Failure and ambiguous external outcomes do not require blind retry.
8. Adding it preserves fresh-Run and workspace-disposition invariants.

If these conditions cannot be met, the workload belongs in an explicit compatibility environment rather than being smuggled through the Pysolate boundary.

## Directional priorities

The first foundation is Current. Remaining work should proceed in this order,
subject to concrete workload evidence:

1. Complete and independently review the local research acceptance proof and
   operator surface without moving Lab storage or orchestration into Runtime.
2. Expand the Experimental/Partial deterministic profile only through a real
   RED probe, an explicit control or admission denial, and artifact-bound GREEN
   evidence.
3. Extend curated information sources only where a concrete workload justifies
   another dedicated adapter.
4. Add write effects only through an explicit Host Effect Plane with
   reconciliation.
5. Expand safe computer coverage through qualified Git, artifact, document,
   media or browser capabilities rather than generic HTTP or shell access.

The success metric is not the number of APIs or the percentage of Linux commands imitated. It is the share of real Agent work completed without a general Computer while preserving bounded authority, evidence coverage, final-state correctness and honest replay semantics.
