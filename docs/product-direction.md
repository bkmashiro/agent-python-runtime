# Product direction: a Python-native capability computer

## Status and baseline

This document separates the verified implementation from product direction. `Current` statements describe code and tests present in the repository; `Proposed` statements are decision constraints for later work, not claims that the named capability, record, replay mode or deterministic guarantee exists today. Release or evaluation records should pin a concrete commit rather than treating this living document as a version identifier.

Pysolate's long-term direction is:

> A Python-native capability computer for Agents: ordinary Python provides control flow, a Host-owned workspace carries named durable state, and every external authority crosses a narrow Host-mediated boundary.

Generic code execution is necessary infrastructure, not the differentiator. The intended differentiator is an auditable, evidence-bound state transition that can support scoped playback and verification without introducing ambient Computer authority.

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

## Current implementation

The current implementation provides:

- a fresh CPython/WASI Guest for every Run;
- bounded request, response, memory and wall-clock execution;
- Host-derived import admission bound to a verified Guest distribution;
- a Host-selected private rooted `/workspace` and fresh `/tmp`;
- complete Workspace Capsules for explicit storage, migration and restoration;
- Host-selected `export_on_success`, `export_on_response` or `discard` disposition;
- a generic Guest-to-Host JSON call ABI, Host Registry and bounded Broker;
- a sealed per-Run capability plan whose canonical identity binds sorted capability names, declared handler identities and the total call budget;
- three prebound in-memory workspace functions as the current tool demonstration;
- compact Host-authored capability and workspace receipts; capability receipts and the top-level Host response bind the sealed plan identity, including for zero-call Runs.

The generic Registry can register a named handler plus a stable declared handler identity without changing the WASI import surface. The Host seals that Registry before Guest startup; late registration is rejected and the Broker accepts only the resulting immutable plan. The current CLI's Python-facing wrappers are still fixed to `read_text`, `write_text` and `list_files`; a generated dynamic Python SDK, strict capability schemas and a plugin catalog are not Current.

Current receipts bind call and workspace identities, but the repository does not yet claim a durable complete audit archive, deterministic replay, external-effect reconciliation or transaction semantics.

## Governing design rules

### No shell

Pysolate will not expose an ambient shell or arbitrary subprocess API. Ordinary Python supplies branching, loops, structured data handling, errors and composition. Common computer operations should be ordinary Python libraries or typed Host capabilities:

```python
from pysolate import artifacts, git, workspace

matches = workspace.grep(query="TODO", path="/workspace")
status = git.status(repository="/workspace/repo")
published = artifacts.publish(path="/workspace/report.md")
result = {"matches": matches, "status": status, "published": published}
```

A familiar method name is presentation, not authority. `git.status()` does not imply a Git binary, credentials, arbitrary network access, hooks or Host paths. Its implementation remains behind a reviewed Host contract.

### One canonical capability definition

A Proposed `CapabilitySpec` should define at least:

- stable capability and version identity;
- Python module and method projection;
- strict input and output schemas;
- read/write/effect classification;
- target and credential policy;
- call, byte, time and external-cost budgets;
- Host handler identity;
- receipt and playback treatment.

The Python SDK, direct Agent tool schema, Host admission and validation, documentation and replay adapter should derive from that definition. Python wrappers must not create a second policy path.

### Dynamic catalog, frozen per-Run authority

Current Pysolate freezes and identity-binds the registered capability set, declared handler identities and total call budget before Guest startup. The Host may construct a different Registry between Runs. A future plugin catalog may install or qualify implementations from which Host policy selects each Run's subset; plugin discovery and schema generation are not Current.

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

Workspace files are inspectable state. Python heap, module globals, WASM memory, open descriptors, Broker handles, credentials and `/tmp` are not continuation state. A future pinned interpreter session, if evidence justifies one, must be an explicit profile with identity, revision, TTL, budgets, health and failure semantics; it must not silently change ordinary Run behavior.

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

### Effect reconciliation

An external write such as a push, publication, message or payment is not replayed. A future Effect Plane must journal an immutable intent before dispatch, use stable provider identity or idempotency where supported, and reconcile ambiguous outcomes. Playback returns the recorded effect state; it never repeats an already applied effect.

### Deterministic verification

An exact deterministic claim requires all relevant nondeterminism to be controlled or captured, including runtime artifact, initial state, clock, randomness, locale, filesystem ordering and external capability results. Until those prerequisites exist and are verified, the stronger honest claim is attribution: no unrecorded ambient authority may affect the Run, and recorded external inputs explain the observed transition within the stated threat model.

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

The first foundation is Current. Remaining work should proceed in this order, subject to concrete workload evidence:

1. Define one versioned `CapabilitySpec` and generate a Python proxy plus direct tool projection.
2. Add one useful read-only Host capability with strict schemas, budgets and receipts.
3. Add protected execution records and capability playback for pure/read-only calls.
4. Control or capture clock, randomness and other relevant nondeterminism for a bounded deterministic-verification profile.
5. Add write effects only through an explicit Host Effect Plane with reconciliation.
6. Expand safe computer coverage through qualified Git, HTTP, artifact, document, media or browser capabilities.
7. Consider pinned interpreter sessions only after measured workloads show that explicit workspace state is insufficient.

The success metric is not the number of APIs or the percentage of Linux commands imitated. It is the share of real Agent work completed without a general Computer while preserving bounded authority, evidence coverage, final-state correctness and honest replay semantics.
