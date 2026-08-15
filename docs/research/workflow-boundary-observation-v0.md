# Workflow-boundary observation contract v0

Status: **Host-authored evidence contract; no execution authority**
Date: 2026-08-15

## Purpose

`pysolate.workflow-boundary-observation.v0` is the single canonical, bounded evidence
contract for telling the difference between work requested by logical tasks and work
actually performed by the Host. It does not authorize execution, reuse, pre-dispatch,
parallelism, placement, retry or replay. Runtime mechanisms decide first through their
existing contracts; the observer records the resulting facts.

The v0 source lives in `runtime/observe/optimization.go`, alongside the existing
Host-authored runtime observation envelope rather than in an optimizer-specific identity
registry.

## Vocabulary

- **run** — one task treatment with artifact/profile/Plan identity and terminal state;
- **span** — a body-free model invocation/output, Guest WASM, Host tool or typed WASI
  interval;
- **logical request** — one workflow-node occurrence demanding one typed boundary;
- **physical execution** — one Host-owned attempt, with one producer and an exact sorted
  consumer set;
- **decision** — a recorded `preissued`, `declared_parallel`, `coalesced` or `reused`
  admission/rejection and reason.

A logical request always points to the physical execution that satisfied it. A reused or
coalesced request additionally points through the decision to the producer logical
request; every non-producer physical consumer must be covered exactly once by such an
admitted decision. A declared-parallel relation carries an independent Harness
declaration digest
and must correspond to distinct physical intervals that truly overlap. A rejected
relation carries no physical or producer authority.

## Identity and time

The observer reuses existing Host identities:

- explicit workflow ID and node ID;
- exact capability/WASI boundary identity digest;
- physical execution ID and producer logical request ID;
- artifact, execution-profile and capability-Plan digests;
- explicit authority, freshness and privacy partition digests on every logical and
  physical boundary record.

The report also binds a nonzero shuffle seed and exact workload-manifest SHA-256, so a
randomized issue order remains reproducible rather than becoming an untracked story.

It does not compare AST regions, source similarity or subset relationships. Time is
unsigned nanoseconds relative to one study monotonic origin. Runs may overlap. A
preissued physical execution must start strictly after qualification and before demand; an
in-flight coalesced follower must demand during the producer interval; retained reuse
must demand strictly after successful producer completion.

## Privacy and integrity

Every report identifier has a schema-specific prefix (`study-`, `run-`, `span-`,
`logical-`, `physical-`, `workflow-`, `node-`, `occurrence-`, or `decision-`) followed
by 16–64 lowercase hexadecimal characters. Descriptive or free-form IDs are rejected.
The report otherwise contains allowlisted enums, timing, counts and SHA-256 bindings only.
Span labels are fixed by kind and each span declares `measured` or
`replayed` evidence; a deterministic replay may therefore show an LLM phase without
claiming a live provider or private chain-of-thought. Producers must never derive IDs
from prompt text, model output, tool arguments/results, Python source, workspace paths or
credentials. Decision reasons and error classes are allowlisted rather than free-form.
Decisions are `host_recorded`, not self-attested as measured. Every physical execution
has exactly one measured Host tool/WASI span with matching kind, interval, boundary input
digest and terminal output digest. The report is bounded to 256 runs, 4096 items per
collection, the shared decoder token-node budget and 2 MiB canonical JSON.

`BuildOptimizationReport` validates cross-object provenance, canonical ordering and
temporal claims and runs the same token-node scanner as the decoder before sealing.
`DecodeOptimizationReport` rejects duplicate keys,
unknown fields, non-canonical JSON, a changed seal or any broken relation and returns an
opaque `VerifiedOptimizationReport`. Callers receive deep copies. `consumer_admitted`
is fixed to false: Lab and evaluation projections may read a verified report but cannot
turn it into Runtime authority.

## Observable optimization meanings

- `preissued`: the same physical operation started before its logical demand;
- `declared_parallel`: distinct Harness-declared requests have measured overlapping
  physical intervals;
- `coalesced`: multiple concurrent logical requests consumed one in-flight physical
  execution;
- `reused`: a later logical request consumed one already completed successful physical
  execution;
- `rejected`: the candidate was considered but no optimization authority was granted.

“Saved work” is therefore never represented by deleting a request. Every logical request
remains visible and points to its producer. Counterfactual savings must come from a
paired baseline run, not from this contract inventing hypothetical durations.
