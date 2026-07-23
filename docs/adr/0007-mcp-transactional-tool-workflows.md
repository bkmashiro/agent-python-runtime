# ADR 0007: MCP-derived transactional Python tool workflows

- Status: Accepted architecture; implementation active, no write/effect capability claim yet
- Date: 2026-07-23
- Activation baseline: `845757e62d21235d451797a864f97f9acf855af2`

## Context

The runtime already exposes one Host-mediated read capability through the sole custom Wasm import `agent_runtime_v1.host_call`. Generated Python can compose repeated reads, but the Host broker currently hard-codes `fetch_many`; MCP input/output schemas are not projected into Python, and V1 intentionally has no write/effect transaction system.

A useful Agent demo needs one discoverable tool surface for both direct calls and temporary Python workflows. Compound Python should reduce intermediate model orchestration without gaining credentials, provider-native clients, raw network access, policy selection, approval authority, or permission to forge receipts. Write-like tools also have materially different recovery semantics: some support exact state restoration, some only a compensating effect, and some are irreversible after commit.

## Decision

### One canonical tool definition

The Host discovers MCP tool metadata, normalizes it, applies an authoritative local grant/effect-policy overlay, and freezes an immutable `ToolCatalogSnapshot` for each Run. Canonical MCP JSON Schema remains the validation source. Python signatures, `TypedDict`/`Literal` annotations, docstrings, runtime reflection, and the model-visible `.pyi` summary are generated projections from the same snapshot and carry its digest.

Projection is explicitly `exact`, `lossy`, or `unsupported`. Unsupported schemas are not automatically exposed to Python. MCP annotations are hints, not authority. Tool-name collisions, stale catalog digests, malformed schemas, unknown grants, and unavailable effect adapters fail closed.

### One Host path

Direct Agent calls and generated-Python wrappers enter the same typed Host registry, policy, budget, transaction, ledger, receipt, and observability path. The Guest keeps one custom Wasm import; a new Wasm import is not added per tool.

A Python `Runner.Run` is one multi-operation Host transaction. A direct call is one single-operation Host transaction. Transaction IDs and all authority-bearing identities are Host-authored and opaque.

### Transaction outside, operations inside

The public V1 control plane is transaction-granular: inspect, request rollback, request compensation, and read summary. The Host journal is operation- and attempt-granular. It records ordered operations and distinct apply, rollback, and compensation attempts with expected prior state, leases, provider request/idempotency identity when supported, bounded observations, and terminal outcomes.

Arbitrary single-effect rollback is not exposed to generated code. Named groups/savepoints remain deferred until evidence shows whole-transaction rollback causes workflow fragmentation.

### Effect semantics

Every effect adapter is qualified as one of:

- `read_only`: no intentional mutation and no undo;
- `reversible`: adapter-owned, idempotent exact restoration of a declared business projection, guarded by before/after version or digest checks;
- `compensatable`: a separate qualified effect may restore net business meaning, while original history remains and compensation may fail;
- `irreversible`: no rollback claim after apply.

Exact rollback never overwrites concurrent newer state. Compensation never reports `rolled_back`. Mixed transactions advertise their weakest truthful guarantee. Any ambiguous provider outcome enters reconciliation and blocks blind retry, commit, rollback, and compensation until provider-idempotency or readback evidence resolves the operation.

### Commit policy is orthogonal to effect class

The Host preserves the Effect Plane outcomes:

- `DENY`;
- Host/user-preauthorized `AUTO_COMMIT`;
- later-turn `AGENT_COMMIT_REQUIRED` with a fresh Host phase grant;
- trusted-control-plane `USER_APPROVAL_REQUIRED`.

Generated Python cannot choose `AUTO_COMMIT`, approve itself, or obtain commit authority inside a staging Run. Any non-preauthorized commit ends the staging Run first. A commit or approval binds the immutable effect/transaction manifest digest; modified arguments create a new intent.

### Evidence and audit

The canonical ledger records transaction, operation, attempt, catalog, policy, artifact, and bounded provider identities. Metrics and traces are derived views. Raw credentials, headers, unrestricted URLs, approval tokens, full sensitive payloads, and arbitrary provider errors are not stored by default; canonical digests and bounded error codes are used instead.

Business mutation and audit transition are atomic only when they share one qualified store. External providers require intent journaling before dispatch and explicit ambiguous-completion handling. Hash chaining is not described as tamper resistance against an actor with the same Host storage/signing authority.

### Evaluation boundary

The owner deferred live Agent Direct/Python/Hybrid evaluation. Deterministic fixture scenarios remain permitted for semantic and real-artifact E2E verification, but no workflow-efficiency, model-round, or token-saving claim may be made until the evaluation track is explicitly reactivated.

## Consequences

- Existing `fetch_many` behavior must survive migration behind the generic registry.
- Dynamic Host catalog changes take effect only on a later Run; a live Run never changes schema or authority underneath generated code.
- Tool adapters, not the LLM, supply undo/compensation mechanics and provider reconciliation.
- The runtime does not claim distributed ACID transactions across MCP providers.
- The durable store and MCP client dependencies require separate portability, license, crash-recovery, and size gates.
- Session managers, Python-heap persistence, COW, UFFD, QEMU, release, deployment, and production-sandbox status remain outside this roadmap.
- Until implementation and Linux/WASI artifact gates pass, README/status/threat-model wording must continue to describe write/effect handling as unimplemented.

## Required acceptance evidence

- strict schemas and positive/negative fixtures for catalog, transaction, operation, attempt, receipt, approval, commands, and evidence;
- executable transaction/operation/attempt state transitions and abort matrix;
- same-Run commit and self-approval denial;
- stale catalog, forged identity, duplicate attempt, lease expiry, concurrent rollback conflict, and ambiguous-completion denial;
- exact reversible, compensatable, and irreversible local fixture adapters with truthful receipts;
- Host/Guest E2E through the exact Linux-built Wasm artifact;
- durable-ledger crash/reopen/reconciliation evidence if durable storage is promoted;
- independent high-risk review before any write/effect safety claim.
