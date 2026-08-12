# Pysolate Lab v1 read contract

Status: **Current** for the bounded wire schemas, canonical projection, privacy rules and strict validation; **Experimental** for branch/comparison interpretation; **Proposed** for any transport or deployed Web product.

## Decision

Lab v1 is a read-only, body-free projection over Host-owned research evidence. It is not the LabStore disk format, a capability grant, an execution request, an authority token, a scheduler or a mutation API.

Every document carries:

- an exact `schema_version`;
- `source_sha256`, identifying the upstream study/evaluation source bytes;
- `generated_at_policy: "omitted"`.

The source digest and all object/document digests are references and equality evidence. They do not authorize access or execution. Wall-clock generation time is deliberately omitted from canonical content.

## Documents

| Schema | Purpose | Required bounded content |
|---|---|---|
| `lab-index.v1` | Entry point | Exactly eight ordered read-projection links and the four closed view flags `branch_dag`, `comparison`, `timeline`, `workspace_diff` |
| `study-summary.v1` | Cohort summary | Workload/treatment counts, four explicit status totals, evidence class, exact prohibited claims, storage/reuse totals |
| `run-detail.v1` | One evaluated run | Workload/treatment/status/oracle/completeness plus exactly one invocation, execution, artifact, execution-profile, capability-plan, result and workspace-tree ref |
| `timeline-page.v1` | Host-observed event page | Ordered lifecycle/capability metadata, causal sequence, bounded cursor page and evidence completeness |
| `branch-dag.v1` | Immutable lineage page | Run nodes and typed `override`, `recorded_suffix` or `live_suffix` edges with fork operation and branch identity |
| `workspace-diff.v1` | Body-free tree delta | Normalized Guest-relative path, added/removed/modified kind, size, executable bit and digest |
| `run-comparison.v1` | Bounded comparison | Closed same/different dimensions, operation/workspace deltas, reason codes and page state |
| `object-ref.v1` | One typed reference | Kind, digest, privacy and export availability; never body bytes |
| `problem.v1` | Fail-closed structured problem | Closed code/severity/scope with bounded optional run/ref linkage; no free-text detail |

Schemas live in `schemas/lab/v1/`. Matching Go projection/codec/fixture code lives in `research/labview/`, outside Runtime core.

## Projection sets and canonical cases

A complete projection set has all nine documents. The index references the other eight by exact canonical document SHA-256. Missing, private, incomplete or empty data is represented explicitly through...[truncated]

Canonical checked-in cases are:

- `empty`: a valid study/run with explicitly empty timeline, DAG delta, workspace delta and comparison pages;
- `ordinary`: complete non-branched evidence;
- `branched`: a typed capability-boundary branch and bounded differences;
- `incomplete`: failed oracle/run with explicitly incomplete evidence;
- `truncated`: returned items are smaller than total and an opaque next cursor is present;
- `private`: protected result/workspace references remain typed and digest-addressed but are unavailable; no body is projected.

`empty` means empty projections, not an invented absence of study identity.

The 54 JSON fixtures live under `research/labview/testdata/canonical/<case>/`. `research/labview/testdata/canonical/manifest.sha256` is producer-generated, lexically ordered and checked byte-for-byte by tests. Fixtures contain only digests, enums, bounded counts and normalized Guest metadata.

## Pagination and ordering

All page objects use:

- `cursor` and `next_cursor`: empty or opaque base64url-like identifiers (`[A-Za-z0-9_-]`, at most 128 bytes);
- `returned`: exact count of items in the document;
- `total`: at least `returned`;
- `truncated`: exactly `total > returned`;
- a non-empty `next_cursor` iff truncated.

A page returns at most 256 items. Timeline sequence remains globally monotonic across pages: the first page starts with causal parent zero; continuation pages retain the preceding sequence as the first returned event's parent.

Canonical ordering is fixed where identity depends on arrays:

- index links: study, run, timeline, branch, workspace, comparison, reference, problem;
- run refs: artifact, capability plan, execution, execution profile, invocation, result, workspace tree;
- status totals: completed, failed, timed out, unsupported, including explicit zero counts;
- timeline: sequence;
- DAG nodes: run ID; edges: parent then child;
- workspace changes/deltas: normalized path;
- comparison dimensions and reason/problem codes: lexical order;
- call deltas: operation index.

## Privacy and authority

Portable projection fields may contain IDs, closed enums, counts, normalized Guest-relative metadata paths and SHA-256 references. They may not contain:

- prompt or Agent-source bodies;
- result or workspace file bodies;
- Capsule payloads;
- provider/tool raw bodies;
- Host filesystem paths;
- endpoints, credentials or authorization headers;
- capability grants, authority tokens or executable requests;
- arbitrary free-text summaries/details.

`privacy=private` requires `availability=unavailable`. Availability describes this projection/export, not LabStore existence and not authorization. Digest retention does not disclose the protected body.

The index `capabilities` field is unfortunately named for Web compatibility but is a closed list of available **read views**. It is unrelated to Runtime capabilities or grants and cannot contain execute/mutate/authority values.

## Evidence semantics

Run status/oracle status and evidence completeness are independent. A failed oracle may have complete evidence. A successful task may have incomplete evidence if required recording was lost.

Timeline v1 projects only Host-observed lifecycle, typed capability call digests and workspace-finalization metadata. It does not claim Python bytecode, locals, heap, WASM memory, complete syscall ordering or hidden model state.

Branch DAG values preserve the Runtime vocabulary. `live_suffix` is Experimental and must not be interpreted as strict offline suffix equivalence. Branches remain fresh-Guest executions at a capability operation boundary, not VM/heap restoration and not relaxed playback.

Storage totals describe logical bytes, stored bytes, object counts and reused-object counts. They do not imply economic advantage.

## Validation layers

Each document is validated twice:

1. Draft 2020-12 JSON Schema: required fields, closed objects, enums, patterns, item limits and locally expressible unions;
2. Go strict/cross-set validation: canonical bytes, exact source identity, index document hashes, link targets, run/problem links, pagination conservation, timeline causality, DAG acyclicity/single parent, cross-field delta semantics and privacy rules.

Schema validation alone is insufficient for graph/link integrity. Consumers presenting a complete study must validate the projection set or consume producer output that has passed the same cross-set gate.

All JSON decoders reject unknown fields, trailing values, non-canonical bytes, `null` required arrays and documents larger than 16 MiB.

## Versioning

Lab v1 is frozen as an exact strict wire contract. Because v1 consumers reject unknown fields, silently adding even an optional field would not be backward compatible in practice. Therefore:

- documentation clarifications and validator bug fixes that do not change accepted canonical wire shapes may remain v1;
- any field addition/removal, requiredness change, enum change, identity/order/pagination/privacy semantic change or relaxed unknown-field behavior requires v2;
- producers and consumers must not maintain a second hand-written shape.

After this freeze, Web must consume the Go producer's checked-in canonical fixtures. The `pysolate-research lab project` read command and Web fixtures both use `research/labview` canonical encoding; the command does not maintain a second JSON shape. The earlier `pysolate.lab-draft.v0` shape is disposable and must not become a compatibility layer.

Evaluation-report projection is intentionally incomplete: the strict report and matching strict measurement summary provide study status/storage totals, but do not carry timeline events, branch lineage, workspace entries, or the seven underlying typed object identities required by `run-detail.v1`. The bridge requires both documents, rejects identity/count drift, and emits the measured storage totals. For absent relations it emits empty observation/delta pages, `evidence_incomplete`, and a distinct canonical unavailable-relation marker digest for each required ref kind. Markers are always `privacy=private` and `availability=unavailable`; they identify the missing relation statement, not an artifact/result/workspace body and not execution authority.

No Lab branch-execution command is added. An evaluation report cannot supply the explicitly sealed Host Plan, Grants, source handlers and fresh Guest required by `operator.RunBranch`. The existing `branch plan` command may publish a protected, exclusive manifest, but projection and all `lab project` commands remain read-only.

## Current, Experimental and Proposed

**Current**

- nine bounded schemas and Go projection types;
- strict canonical codec and cross-set validation;
- canonical/adversarial fixture gates;
- privacy/body/authority exclusion and deterministic identities.

**Experimental**

- branch DAG interpretation;
- comparison reason-code usefulness;
- storage reuse as diagnostic evidence;
- projection from the current research operator/LabStore prototype.

**Proposed, not implemented by v1**

- HTTP/API transport, authentication or authorization;
- deployed Web hosting;
- live execution/mutation controls;
- multi-user/multi-tenant service;
- production database or SQLite decision;
- full-text search, provider traces or hidden-state inspection.
