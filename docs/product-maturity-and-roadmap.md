# Distance to the Pysolate product direction

Status: **Planning assessment at source baseline
`fe342b2d929dd842c5ce015d23d5a74f8bd014a3`.** This document estimates
remaining engineering and research work. It does not make schedule or release
commitments.

## The final direction

Pysolate's intended destination is a Python-native capability computer for
Agents:

- ordinary Python supplies control flow;
- a Host-owned workspace carries explicit durable state across fresh Runs;
- external authority is available only through frozen typed capabilities;
- each Run is an evidence-bound state transition;
- captured reads support scoped playback and verification;
- external writes use explicit intent, dispatch and reconciliation semantics;
- a separate Harness/Lab retains, queries and compares evidence without moving
  orchestration or authority into Runtime;
- irreducibly ambient, native, interactive or long-lived work uses an explicit
  Computer-last compatibility path selected before execution.

The destination is not a smaller Linux VM, an ambient shell, or a claim that all
computer work belongs in Pysolate.

## Short answer

Distance depends on which finish line is intended:

| Finish line | Current distance | Planning interpretation |
|---|---|---|
| Supervisor/thesis mechanism demo | Close | The execution, workspace, capability, receipt, playback and bounded evaluation mechanisms are runnable; presentation integration is the remaining work. |
| Defensible research prototype | Moderate | Core invariants exist, but representative end-to-end workloads, capability breadth, effect semantics and integrated evidence workflows remain limited. |
| Useful local Agent alpha | Substantial | Needs a practical capability set, SDK/Host integration, durable invocation workflow, recovery UX and routine dogfooding. |
| Production capability computer | Far | Needs an Effect Plane, service-grade Harness/Lab, security/portability qualification, operations, migrations, access control and sustained workload evidence. |

A useful planning estimate, assuming one focused developer and no requirement to
reach production service maturity, is:

- **days to a week** for a rehearsed thesis/demo package;
- **two to four focused months** for a useful local alpha around a deliberately
  small workload family;
- **six to twelve focused months** for a credible end-to-end research prototype
  of the broader capability-computer vision;
- **twelve to twenty-four months or a small team** for production-oriented
  operation, depending mainly on effect adapters, capability coverage,
  multi-user Lab requirements and platform qualification.

These ranges are architectural planning estimates, not measured forecasts. The
thesis does not require the final two finish lines.

## Capability-by-capability assessment

### 1. Fresh Python execution substrate — strong prototype

**Current**

- verified CPython/WASI distribution;
- fresh Guest per Run;
- bounded memory, time, request and response;
- Host-derived import admission;
- structured output validation and Host-authored response evidence;
- real-Guest integration and failure-path tests.

**Remaining**

- broader package/profile qualification;
- additional platform runtime qualification beyond the current evidence;
- release packaging, compatibility policy and supported-version lifecycle;
- sustained workload and soak evidence.

**Distance:** close for research use; moderate for a supported developer
runtime.

### 2. Explicit durable workspace and Capsules — strong bounded prototype

**Current**

- private rooted `/workspace` with fresh `/tmp`;
- bounded snapshot ingress;
- complete deterministic Capsule export/import;
- explicit `export_on_success`, `export_on_response` and `discard` policies;
- Host-authored initial/final state and disposition receipts;
- atomic no-partial publication at the implemented boundary.

**Remaining**

- user-facing revision/history workflow;
- scalable chunking, deduplication and compaction if real workloads require it;
- workspace conflict and collaboration semantics;
- backup, migration and recovery policy;
- practical Git/artifact/document capabilities operating on workspace state.

**Distance:** mechanism largely exists; product workflow remains moderate.

### 3. Typed capability system — architectural core exists, ecosystem is early

**Current**

- one canonical `CapabilitySpec` source for Python projection and Direct tool
  schemas;
- strict argument/result schemas;
- Host handler, effect and playback declarations;
- opaque per-Run grant identities;
- frozen plan and total call budget;
- three typed in-memory workspace operations;
- two credential-free curated read sources.

**Remaining**

- a small but genuinely useful semantic capability set, likely starting with
  workspace search, Git inspection, artifact/document transformation and
  bounded publication;
- qualification and version lifecycle for adapters;
- per-target cost and rate budgets;
- credential-reference handling without exposing credentials;
- a catalog and Host policy UX that never permits mid-Run authority growth.

This is the largest gap between a convincing mechanism and a useful everyday
Agent computer. The architecture is ahead of the available operations.

**Distance:** substantial.

### 4. Evidence, observation and playback — strong for captured reads, incomplete
for general effects

**Current**

- Host lifecycle, capability and workspace observations;
- compact receipts and relation identities;
- capture and strict offline playback for two curated external reads;
- fresh-Guest final-result and workspace-identity verification;
- protected acceptance artifacts;
- Experimental capability-boundary branches;
- Experimental/Partial deterministic-verification profile.

**Remaining**

- a complete durable Run record joining invocation, execution, workspace,
  capability transcript and outcome bodies under access policy;
- broader capability-specific playback treatments;
- versioned migration between future evidence schemas;
- independent verifier and oracle tooling suitable for routine use;
- explicit replay levels in product UX;
- more negative and corruption-path qualification at release boundaries.

**Distance:** close for the scoped read-only mechanism; moderate-to-substantial
for a general evidence system.

### 5. External writes and ambiguous outcomes — major missing plane

**Current**

- effect classes and playback treatments are represented in capability specs;
- current public capabilities avoid credential-bearing external writes;
- the design forbids treating playback as repetition of a real-world effect.

**Remaining**

- immutable intent before dispatch;
- stable operation identity and idempotency integration where providers support
  it;
- explicit `not_dispatched`, `applied`, `rejected`, `ambiguous` and reconciled
  outcomes;
- provider-specific status lookup and reconciliation;
- compensation semantics where possible;
- operator approval and audit UX;
- safe retry policy.

Without this Effect Plane, Pysolate can safely demonstrate bounded reads and
workspace transitions but cannot support the full class of useful actions such
as publishing, messaging, pushing or paying.

**Distance:** far; this is the most important unimplemented architectural
component.

### 6. Harness and Pysolate Lab — substrate exists, service workflow is early

**Current/Experimental**

- invocation/execution identities and observation contracts;
- local typed LabStore CAS with retention and privacy metadata;
- Bundle inspect/compare, branch planning/DAG and fresh-Guest branch API;
- frozen body-free Lab v1 read schemas and canonical projection fixtures;
- evaluation reports and bounded study summaries;
- fixture-backed Web viewer under active demo development.

**Remaining**

- live ingestion that joins Harness coordinates to Runtime evidence;
- durable study/run indexing and semantic queries;
- pagination over real stored data;
- access-controlled protected body retrieval;
- orchestration of retries, branches and cohorts;
- authentication, authorization and audit logging if multi-user;
- migrations, backup, recovery and service operations;
- explicit export review rather than treating `portable` as automatically safe.

The browser UI is not the principal gap. The gap is the trusted ingestion,
relation, query and access-control path behind it.

**Distance:** moderate for a local single-user research Lab; far for a deployed
multi-user service.

### 7. Computer-last compatibility and coverage — policy clear, implementation
minimal

**Current**

- explicit unsupported/admission failures;
- no automatic authority escalation;
- design rule that broader compatibility is selected before execution and does
  not become continuation state.

**Remaining**

- a concrete placement contract between Pysolate profiles and a separately
  governed Computer/VM;
- workspace revision handoff in both directions;
- a representative long-tail workload taxonomy;
- measured admission/completion/Computer-avoidance rates;
- operator UX for explicit escalation decisions.

**Distance:** substantial, but it should follow real workload pressure rather
than precede it.

### 8. Security, portability and operations — research controls exist,
production assurance does not

**Current**

- fail-closed schemas and bounds;
- no ambient shell, generic HTTP or Guest credentials;
- protected file modes and body-free portable projections;
- race, crash, fault and recovery probes at selected boundaries;
- Unix locking and Windows cross-compile evidence for LabStore.

**Remaining**

- an explicit production threat model and adversarial campaign;
- dependency/SBOM and release provenance;
- runtime sandbox/platform qualification on each supported OS;
- fuzzing and sustained resource-exhaustion testing;
- telemetry, upgrade and rollback procedures for the Host product;
- service recovery objectives and operator runbooks;
- multi-tenant isolation and incident response if deployed.

Cross-compilation is not runtime qualification, and the existing mechanism tests
are not a production security certification.

**Distance:** far for production; acceptable for a carefully labelled local
research prototype.

## What the demo changes—and what it does not

The supervisor demo improves three things:

1. comprehension of the authority and state-transition mechanism;
2. evidence that the real Guest path is runnable;
3. an inspectable presentation of already frozen Lab documents.

It does **not** materially close the capability-ecosystem, Effect Plane,
live-ingestion, service-operations or platform-qualification gaps. A polished
viewer can make the prototype easier to understand, but it does not move the
system from research prototype to production by itself.

## Recommended route after the demo

### Phase A — thesis-grade closure

- rehearse the three teaching examples and one playback acceptance path;
- retain evaluation v2.1 as a bounded mechanism study;
- use Lab as a read-only explanatory viewer;
- write the paper around authority placement, explicit state and evidence-bound
  transitions;
- avoid broad performance, security or production claims.

**Exit condition:** a reader can reproduce the mechanism and distinguish
Current, Experimental and Proposed surfaces.

### Phase B — one useful local alpha slice

Choose one real, bounded workload family—for example inspecting a local
repository and producing a reviewed artifact—and add only the semantic
capabilities it requires. Dogfood the complete sequence:

```text
Host workspace revision
  -> admitted Python program
  -> several typed reads/transforms
  -> explicit final workspace revision
  -> receipts and inspectable evidence
```

Do not build a broad plugin marketplace or generic HTTP layer.

**Exit condition:** the workflow is easier to operate and audit than a
Computer-first equivalent for its stated users, without ambient authority.

### Phase C — Effect Plane research

Design and implement one external-write adapter with intent, dispatch,
ambiguous-outcome reconciliation and playback-safe semantics. A bounded artifact
publication is a safer first candidate than messaging, Git push or payments.

**Exit condition:** retries and uncertain provider responses cannot silently
repeat or misreport the effect.

### Phase D — integrated local Lab/Harness

Add live ingestion and query over real Runs only after the relation and privacy
model is stable. Keep Runtime free of database, UI and study-orchestration
policy.

**Exit condition:** a locally executed Run appears in Lab with validated links
to its timeline, result/workspace references and playback/branch relations,
without a second wire shape.

### Phase E — qualification and release decision

Only then decide whether to remain a research artifact, ship a local developer
tool, or build a service. Each destination has different security, operations
and access-control requirements.

## Stop conditions

Do not interpret progress as “add more APIs.” Pause or redirect when:

- a capability cannot state bounded maximum authority;
- useful coverage requires generic shell, sockets, Host paths or Agent-owned
  credentials;
- an external write cannot reconcile ambiguous completion;
- a Lab feature treats a digest as authorization or private export approval;
- a compatibility path silently broadens authority after a failed Run;
- a benchmark repeats a known call-count identity without answering a new
  product or research question.

## Bottom line

Pysolate is **past the idea stage**: the fresh Python runtime, explicit workspace,
frozen capability authority, receipts, scoped playback and research evidence
all execute in real tests. It is also **not near a final production system**:
its semantic capability ecosystem is small, external writes lack an Effect
Plane, Lab lacks live service ingestion, and production qualification has not
begun.

The most accurate summary is:

> The mechanism and architecture are credible; usefulness and operations are
> the remaining project.
