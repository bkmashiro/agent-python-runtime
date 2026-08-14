# Threat model

## Trust boundary

Agent source, inputs, output schemas and optional compatibility declarations are untrusted. The Guest artifact, distribution manifest, Host profile, resource policy, tool registry and trusted preparation are Host-owned.

Host invocation/observation context, Recorder mode, Playback/branch artifacts
and their independently expected identities, deterministic-verification seed,
research-store root and retention/privacy policy are also Host-owned. A digest
or self-identity is not an authority credential.

## Protected assets

- Host filesystem paths and files outside explicitly selected workspaces;
- Host network, processes, environment and credentials;
- execution budgets;
- capability registration and call limits;
- result and receipt integrity;
- observation completeness and causal integrity;
- protected Playback/branch and research-store bodies;
- state belonging to another run.

## Controls retained by the PoC

### No ambient Host authority

The wazero module receives no inherited process arguments, environment, sockets, credentials, package manager, native loader, or arbitrary preopened directory. Optional mounts are selected by the Host.

### Fresh module per run

Every request creates a new module and closes it on every outcome. Python globals, WebAssembly memory, Host-call context and temporary resources are not reused.

### Bounded execution

The Host enforces request/response size, WebAssembly memory and wall-clock timeout. Guest pointer and length frames are checked before Host reads or writes memory.

### Conservative imports

The Agent-facing Host path derives a simple top-level import preamble and compares it with an artifact-qualified profile. Dynamic, relative, nested, late, compound and multiline imports are rejected. The trusted CPython Guest validates source again before execution.

This is compatibility policy, not the primary sandbox. WASI and Host capabilities remain the authority boundary.

### Host-owned tools

Guest code can only call tools present in the Host Registry sealed before Guest startup. `pysolate.capability-plan.v4` binds sorted canonical specs, their opaque Host policy grants and the total call budget; late registration is rejected. Each current spec binds capability/version and handler identities, documentation, effect/playback declarations, strict input/output schemas and generated Python projection metadata. The Host rejects ambiguous JSON, schema-invalid arguments and schema-invalid handler results, then applies canonical workspace path, per-file size and frozen call-budget checks. The active workspace tool has no Host path or network access.

The credential-free `sources.demo_catalog()` and
`sources.benchmark_manifest()` adapters are the two Current external-read
sources. Each exact Host endpoint and transport bounds are grant-bound;
redirects, non-200 responses, non-JSON media types, invalid UTF-8, ambiguous or
trailing JSON and oversized bodies fail closed. The benchmark source has its
own nested versioned schema and rejects duplicate semantic IDs, unknown fields
and invalid metric semantics. The Agent cannot submit a URL, path, query,
method, headers, credentials or transport policy. This does not create ambient
or generic network authority.

Playback Bundle capture uses only Broker-validated successful `captured` calls and refuses incomplete transcripts. Bundle decoding is strict and canonical; operation and payload digests are revalidated. The bundle self-hash is consistency evidence, not authentication: offline Host config must separately supply the capture-issued bundle identity, preventing a re-authored bundle from changing protected claims. Publication is delayed until runner close, response bounds and workspace disposition evidence are final. Offline admission binds the current plan, grants, request, artifact/profile and initial state before Guest startup; the Broker constructs no live HTTP handler, rejects call mismatches and unused records, and the CLI verifies response status plus final result/workspace identities. The artifact format has no fields for Agent source, final result body, workspace body, endpoint policy or credentials.

### Experimental branch routing

An Experimental branch manifest binds the protected parent Bundle and prefix,
fork operation, original request/artifact/profile/initial workspace, complete
child Plan/Grants and suffix policy. Before a child starts, Host config anchors
both parent and manifest identities. The Broker accepts branches only for
`external_read` plus `captured` Specs, consumes the prefix and any recorded
suffix exactly once, revalidates overrides through the selected output schema,
poisons mismatched execution and rejects an unused suffix.

`live_suffix` uses only handlers already present in the sealed child Plan. It
does not authorize a new capability midway through a Run. Each child is a
fresh Guest re-executing the original request and initial workspace; no Python
heap, open descriptor or WASM-memory state is restored. Branch lineage is a
Host research relation, not proof that the child's semantics are correct.

### Unified native capability transport

**Experimental.** Native CPython receives no Broker handler, ambient Host credential or capability implementation. Its generated `_agent_runtime_host.call` bridge reaches a private Unix socket
whose channel is bound to invocation, execution, sealed Plan, expiry and a
transport-only credential injected for that channel. Completed exact retries are replayed from the Host
channel record without redispatch. Changed duplicate IDs, in-flight ambiguity,
identity mismatch, expiry, revocation and oversized frames fail closed. The
socket itself grants no capability.

Native compatibility is not a security or determinism superset. Its image,
workspace, mount mode and lifecycle have separate Host-owned evidence. A dirty
served instance cannot cross sessions. Only preflight selection or a
Host-authored `runtime_unsupported` with both workspace and effects
`not_started` may create a linked native execution. Exceptions, timeout, OOM,
capability denial and ambiguous completion cannot trigger implicit replay.

### Host-authored evidence

The Host replaces Guest receipt, capability-plan and capability-call metric claims. Receipts bind the Host run identity, capability-plan identity, capability, operation index, request digest, response digest and outcome. The top-level response carries the plan identity even for a zero-call Run. The curated-source slice now supports protected capture and bounded offline playback verification; the PoC still does not claim a durable audit service, general deterministic replay across uncontrolled nondeterminism, or transaction/effect evidence.

The Current Runtime observation contract is Host-context-only and binds its
Session to the physical execution identity. Exact-key canonical payloads,
one-based sequences, earlier causal parents, fixed event/payload limits and
defensive copies prevent Guest forgery and mutation aliasing. In
`best_effort`, Recorder loss is marked incomplete and cannot later be reported
as complete. In `required`, Recorder rejection fails the research Run path but
does not expand authority or roll back already performed work.

The stable visibility boundary is lifecycle, validated Broker receipts and
initial/final workspace snapshots. The Host reports sorted bounded file deltas
and explicitly reports syscall order unavailable. It does not claim a complete
WASI trace, unchanged-file reads, Python bytecode/locals, heap/stack, or
WebAssembly-memory visibility. See
[research/runtime-observation.md](research/runtime-observation.md).

### Experimental deterministic verification

The Experimental/Partial deterministic profile is Host-selected and bound to
an exact verified Guest artifact. It replaces wazero's random source and
wall/monotonic clocks for a fresh Guest and rejects mounted workspaces plus
statically identified concurrency/locale import classes. It does not
monkey-patch Agent code. Artifact substitution fails before compilation.

This control is not a full deterministic sandbox. Concurrency, mounted
directory enumeration, locale mutation, cross-platform floating-point
equivalence, implementation upgrades, live-source changes and unqualified
libraries remain outside the claim. Strict Playback or a protected branch
suffix is required to hold external inputs constant. See
[research/deterministic-verification.md](research/deterministic-verification.md).

### Experimental research store

The local `research/labstore` prototype is outside Runtime core. It uses
domain-separated typed identities, bounded framed reads, digest validation,
protected `0600` regular files, exclusive atomic publication, and rooted path
operations with traversal/symlink rejection. Read-only open performs no
creation, migration, repair, pinning or collection.

Portable export recursively checks privacy across reachable objects; private
wins conflicting classification. Named-root reachability, not a raw reference
count, governs retention. Callers must explicitly declare credentials absent,
and structured content rejects common credential field names. That check is
defense in depth, not reliable secret discovery or an authentication boundary.
See [research/lab-boundary.md](research/lab-boundary.md).

### Artifact binding

When an execution profile is configured, the CLI requires the adjacent distribution manifest and import inventory/qualification files. Artifact, manifest, profile and qualified import roots must agree before execution.

## Accepted PoC limitations

- The Host import scanner is intentionally not a complete Python parser and can reject valid programs.
- The typed in-memory workspace is not durable and has no transaction rollback. The optional rooted workspace can be explicitly exported or discarded under Host policy and records initial/final identities in a Host-authored disposition receipt, but capsule publication remains a snapshot operation rather than transactional rollback or per-file effect governance.
- `write_text` mutations remain if later Python code raises an exception in the same process.
- The PoC does not provide multi-tenant scheduling, automatic crash recovery or live-service workspace adoption; migration requires an explicitly exported and re-imported capsule.
- The private workspace root and Host projection source are assumed not to be concurrently mutated by another same-UID Host process. Guest APIs cannot create links, and ingress uses descriptor-rooted traversal with parent-identity checks, but same-UID Host peers are outside the Agent sandbox threat model.
- Availability under adversarial workload is not characterized.
- Side-channel resistance is not claimed.
- A malicious replacement Guest artifact is outside the model; artifact verification is required.
- Required observation failure and multi-artifact research workflows are not
  transactions and cannot undo a Guest workspace mutation or external read.
- The local research store has no cross-process writer lock, multi-object
  transaction, authentication, encryption, migration/recovery service or
  multi-user ACL. Same-UID Host peers remain outside the Agent sandbox boundary.
- The Experimental deterministic profile has been qualified only for bounded
  probes under its exact artifact/profile/input conditions; unsupported cases
  are admission-denied where recognized or explicitly outside the claim.

These are documented limitations rather than prompts to add production machinery. They should only be revisited when a concrete product workload is blocked.
