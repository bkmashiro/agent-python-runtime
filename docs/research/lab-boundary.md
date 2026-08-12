# Runtime and Lab boundary

Status: **Current** as an ownership rule, **Experimental** for the local
`research/labstore` and `research/operator` prototypes, and **Proposed** for a
complete Pysolate Lab/Harness product.

## Decision

Pysolate Runtime remains a fresh-per-Run execution and authority substrate. It
defines bounded evidence contracts and returns Host-owned references. It does
not own Agent conversations, provider traces, research retention, long-term
indexes, swarm orchestration, or a user interface.

The local research packages consume Runtime artifacts from the other side of
that boundary. Runtime core has no dependency on `research/labstore`, the
research operator, a database, or a recorder implementation.

```text
Agent / Harness
  owns conversation, invocation retries and study coordinates
        |
        | Host Run config + InvocationRef
        v
Runtime
  fresh Guest, admission, workspace, Broker, bounded evidence contracts
        |
        | response, observations, Bundle/branch/workspace references
        v
Local research prototype / future Lab
  protected bodies, lineage, retention, compare/query and future UI
```

## Current Runtime responsibilities

Runtime and its Host integration own:

- bounded Run request/response and Host `InvocationRef`/`ExecutionRef`;
- artifact/profile/import admission and a fresh CPython/WASI Guest per Run;
- Host-selected workspace mounting, initial/final identity and disposition;
- sealed capability Specs, Grants, Plan, budget, Broker and compact receipts;
- the `pysolate.runtime-observation.v1` metadata contract and optional Recorder
  interface, but no recorder storage;
- canonical Playback Bundle v1 capture/offline contracts;
- the Experimental branch-manifest and mixed Broker-routing contracts;
- the Experimental/Partial deterministic-verification profile;
- dedicated curated-source adapters, currently `demo_catalog` and
  `benchmark_manifest`.

Runtime does not infer a branch from Agent text, select a research retention
root, parse provider messages, or add authority during a Run.

## Experimental local research substrate

`research/labstore` is a bounded directory content-addressed store. It keeps
immutable typed bodies and relations outside Runtime. Its Current prototype
features include domain-separated kinds, validated `0600` atomic objects,
workspace trees, branch relations, named retention roots, graph reachability,
read-only open, portable/private classification, store statistics, and
synthetic long/branch/swarm/low-reuse benchmarks. The backend choice and
measured overhead are recorded in
[store-backend-decision.md](store-backend-decision.md).

`research/operator` supplies bounded semantic APIs for Bundle inspection,
comparison, branch-DAG export, and fresh-Guest branch execution. The separate
`cmd/pysolate-research` CLI implements human and bounded-JSON `inspect` and
`compare`, protected `branch plan`, bounded `branch dag`, read-only
`store stats`, a synthetic `store benchmark`, and read-only canonical Lab v1
projection from a strict evaluation report plus its matching body-free
measurement summary. The projection cannot reconstruct absent timeline,
workspace, branch or typed-object relations; it emits empty views and distinct
private/unavailable markers under `evidence_incomplete`. It does not execute a
branch. `research/operator.RunBranch` remains the fresh-Guest API and returns an
in-memory outcome for a Host caller. The independent `labstore-bench` command
is also a measurement probe, not a service.

The current DAG export renders one validated parent and caller-supplied
manifest/child pairs. It validates child admission identities and Grants, the
exact parent prefix, and the complete child tape for override and
recorded-suffix branches. For a live suffix it can validate only the admission
bindings and prefix because no suffix result is sealed. These checks reject an
unrelated child Bundle, but do not independently prove that a child's result
was produced by executing the manifest. The Host must protect and preserve the
branch outcome relation.

CLI mutation boundaries are explicit. `inspect`, `compare`, and `branch dag`
read protected Bundle/manifest files and write only their bounded output;
they do not open or migrate LabStore. `store stats` opens an existing store in
read-only mode. In contrast, `branch plan` publishes a new protected manifest
and `store benchmark` creates a new synthetic store destination.

This prototype is deliberately not a service boundary. It has no database,
authentication, remote API, multi-user authorization, cross-process writer
lock, migration engine, or production recovery workflow. Deterministic
child-process probes show that immutable publication and final fail-private
classification converge at the tested boundaries, but crashed or live stages
make aggregate traversal fail closed and cannot yet be distinguished safely.
The measured next step is explicit filesystem ownership and offline repair;
SQLite metadata is not admitted for evaluation v1 because it would not remove
external object stages and no indexed-query or multi-record transaction need
was demonstrated.

The repository has both focused and combined real-Guest evidence. The focused
branch test calls `research/operator.RunBranch` directly. The combined research
workflow starts two loopback curated sources, captures a parent with Required
observation events and a mounted workspace, closes both servers, verifies a
fresh strict offline Guest with no additional source hits, executes two fresh
counterfactual children, validates the semantic DAG/compare APIs, and persists
shared prefix, observation, parent, child and branch objects in LabStore. The
public CLI remains covered by separate output-contract/subprocess tests; the
combined Guest test deliberately does not introduce a subprocess into the
Runtime execution path.

## Proposed future Lab/Harness responsibilities

A future independent Lab may own:

- Agent/provider event ingestion and provider-specific normalization;
- logical Run, retry, branch and swarm orchestration;
- joining Harness coordinates to Runtime `execution_id` values;
- protected body ingestion, redaction review and retention policy;
- indexed semantic queries, pagination and study-level comparison;
- branch planning UX and execution through explicitly sealed Host plans;
- a timeline, branch DAG, operation detail, workspace diff, reuse view and
  swarm lanes;
- authentication, authorization, audit logging, migrations, replication,
  backup and recovery if the prototype becomes a service.

Those features must consume Runtime contracts. They must not move conversation
semantics, UI-specific records, provider credentials, or a database into
Runtime core.

## Body and relation rules

The storage model separates immutable content from mutable policy:

```text
typed immutable body
  -> domain-separated content reference
  -> bounded run/execution/workspace/branch relation
  -> named retention root and mutable privacy policy
```

Equal bytes under different semantic kinds do not alias. A content digest
proves equality of canonical bytes, not authorship, safety, or authority.
Plans, Grants and separately protected expected identities remain the admission
anchors; putting an artifact in a CAS does not authorize its use.

Observation metadata should reference large/private bodies instead of embedding
them. Playback Bundles are intentionally different: they contain the validated
capability result bodies needed for offline execution and therefore remain
protected artifacts. Workspace Capsules and file blobs likewise follow their
own privacy and retention policy.

## Privacy boundary

The local store requires callers to declare credentials absent. Structured
objects additionally reject common credential-bearing field names. This is
defense in depth, not secret detection. Callers must redact before ingestion;
credentials are forbidden even in `private` objects.

Objects are `private` or `portable`. Privacy is mutable metadata, excluded from
content identity, and private wins when classifications conflict. Portable
export recursively checks every reachable child, so a private file or payload
blocks export of a relation that references it. Missing privacy metadata fails
safely to private.

Portable does not mean harmless or public. Code, prompts, provider bodies,
paths, identifiers, digests, and results can still be identifying. A future
Lab needs an explicit export review and access-control layer beyond this binary
prototype classification.

## Retention boundary

Named roots are the retention authority. Reference counts are diagnostic;
transitive reachability from pinned roots determines what remains live. A
pinned child branch retains all referenced parent, prefix, manifest, initial
workspace tree, and file content objects. Collection validates every exact
deletion target before deleting unreachable objects.

Retention is not transactionality. The current filesystem backend does not
atomically update several roots, does not coordinate independent processes, and
does not recover staging debris automatically. Read-only open performs no
creation, migration, repair, pinning, collection, lock-file creation, or
journal write.

## Authority invariants

Research features must preserve all of the following:

- every child execution starts a fresh Guest from the original request and
  initial workspace;
- a branch changes only an explicitly Host-owned captured external-read suffix;
- no heap, Python stack, module-global, descriptor, or WebAssembly-memory
  snapshot becomes continuation state;
- no shell, subprocess, generic HTTP, credential-bearing source, or external
  write effect is introduced;
- a Runtime recorder failure affects evidence validity according to its mode,
  never capability authority;
- the future Lab may retain and compare evidence but cannot reinterpret a
  digest or self-identity as authorization.

See [runtime-observation.md](runtime-observation.md) and
[deterministic-verification.md](deterministic-verification.md) for the two
bounded Runtime profiles consumed by research workflows.
