# Threat Model

## Status and protected asset

This is the V1 target threat model. The protected asset is the Host process and its authority while executing generated Python. This runtime is not claimed to provide microVM-grade kernel isolation.

## Adversary

Assume generated code, inputs, capability arguments, returned pointers, response lengths, and Python exceptions are malicious. The adversary may try to persist state across runs, exhaust resources, escape the guest, obtain credentials, access arbitrary endpoints, or forge success evidence.

## Security goals

V1 must prevent or bound:

- ambient filesystem reads/writes;
- inherited environment variables and secrets;
- direct network access;
- process creation and subprocess execution;
- runtime dependency installation;
- unbounded linear-memory growth;
- unbounded wall-clock execution;
- unbounded response and traceback data;
- undeclared or excessive Host tool calls;
- guest-selected credentials or arbitrary destinations;
- stale run-local state contaminating a later run;
- invalid pointer/length reads and writes;
- unhealthy instances returning to the pool;
- receipts that claim operations not mediated by the Host;
- Guest-forged Agent invocation or execution provenance;
- accidental raw prompt/provider/tool content in the portable trace store.

## Non-goals

V1 does not promise:

- defense equivalent to a hardened microVM or separate kernel;
- arbitrary native extensions;
- full POSIX, shell, PTY, daemon, or background-process behavior;
- currently implemented write-side external effects or rollback; the active MCP transactional-workflow roadmap is implementation work, not an existing V1 claim;
- arbitrary MCP/plugin installation;
- distributed scheduling or multi-host isolation;
- instruction-count CPU metering if wazero cannot enforce it.

Unsupported hard limits are rejected or documented as unsupported; they are not represented as enforced flags.

## Attack surfaces and controls

### RunRequest authority injection

Control: schemas reject unknown and authority-bearing fields. `RunConfig` is constructed by trusted Host code and is not decoded from model JSON.

### Execution provenance forgery

Control: `InvocationRef` is supplied through Host context, never `RunRequest`. A Guest response containing `execution_ref` is rejected before the Host projects its own reference. Capability receipts and transaction evidence bind to the Host `execution_id`, not the Guest `run_id`.

### Portable trace disclosure

Control: the optional Harness trace plugin persists only bounded normalized metadata and SHA-256 digests. Recursive payload validation rejects raw prompt, provider-body, source-code, arguments, observations, and content field names. Raw diagnostics remain separate private artifacts and are not copied into SQLite.

### WASI ambient authority

Control: instantiate without inherited arguments, environment, stdio, network sockets, arbitrary preopened directories, or Host process APIs. Packed read-only runtime files are artifact content, not ambient Host paths. An optional workspace binding adds exactly one Host-selected `/workspace` preopen backed by the rooted ordinary-file adapter: the untrusted request cannot select it; path escape, links, special files, mount crossings, Host identity metadata, and out-of-budget mutations fail closed. A per-module virtual gate prevents workspace access during initialization, warmup, and COW image capture.

### Pointer and length corruption

Control: validate arithmetic for overflow, memory bounds, maximum request/response sizes, and length-prefixed response layout before copying or decoding.

### CPU/wall-time denial of service

Control: require a Host deadline and close the module on cancellation. If instruction/fuel metering is unavailable, do not call the wall deadline an instruction budget.

### Memory denial of service

Control: configure maximum pages/bytes before instantiation, reject unsupported requested limits, detect memory-size drift, and discard instances that cannot be reset safely.

### Output and traceback floods

Control: impose limits while reading/copying, not after an unbounded buffer has already been materialized. Tracebacks are structured and truncated.

### State contamination

Control: use a synchronously instantiated fresh guest by default. The optional prepared pool admits only never-served instances that completed trusted initialization; each checkout serves exactly one Run and is then closed on success, structured error, trap, cancellation, or any uncertainty. A miss falls back to synchronous fresh instantiation. No served instance is reset, restored, or returned to the pool. Any future restore/reuse design must separately prove complete reset of Python globals, modules, random state, buffers, memory size/content, mutable globals/tables, WASI resources, and Host state.

### Host capability abuse

Control: resolve only pre-granted capability IDs; enforce destination allowlists, per-call timeout, per-call byte cap, and total-call budget. Credentials never enter guest memory. Direct guest network access remains unavailable. The production-style client ignores ambient proxies, resolves hostnames at dial time, rejects the whole resolution if any address is private, loopback, link-local, unspecified, multicast, or reserved, and dials a validated IP directly. Only an IP-loopback literal is accepted as an explicit local fixture; DNS names resolving to loopback are denied.

### Dynamic catalog and schema confusion

Control: canonical MCP JSON Schema is normalized under bounded depth/count/size limits, then combined with a Host-owned grant and effect-policy overlay. Python annotations/docstrings and the model-visible SDK summary are projections from one immutable per-Run catalog digest. Duplicate normalized names, unsupported/lossy schema features outside policy, code/docstring injection, stale catalog digests, and next-Run grant revocation fail closed. MCP annotations never grant authority.

### Transaction identity, replay, and confused deputy

Control: direct calls receive single-operation Host transactions and Python workflows receive one multi-operation Host transaction. Run, transaction, operation, attempt, lease, provider request, undo, compensation, approval, and receipt identities are Host-authored and ownership-scoped. Guest-selected identities, duplicate calls, stale leases, changed argument digests, and cross-transaction commands are rejected. Provider dispatch identity is persisted before external dispatch; lease expiry never authorizes blind retry.

### Rollback and compensation integrity

Control: an exact-reversible adapter must verify the current resource version/projection still matches its recorded post-apply state before idempotent reverse-order undo. Concurrent drift blocks mutation. A compensatable adapter creates a separate effect and receipt; it never reports exact rollback. Mixed transactions derive their weakest terminal guarantee from operation evidence. Partial undo/compensation and unknown outcomes remain explicit.

### Irreversible effect authority

Control: commit policy is Host-owned. Generated Python cannot select `AUTO_COMMIT`, approve itself, or receive commit authority in the staging Run. `AGENT_COMMIT_REQUIRED` uses a later Agent turn and new Host phase grant; `USER_APPROVAL_REQUIRED` originates in a trusted control plane. Commit/approval binds an immutable manifest digest, policy version, actor, issue/expiry, and one-time authority. Changed arguments require a new intent.

### Ambiguous provider completion

Control: every external apply, rollback, and compensation has a separate attempt identity, expected prior state, lease, and bounded observation. A crash/timeout after dispatch enters reconciliation unless validated provider idempotency or readback proves the outcome. Reconciliation blocks normal retry, commit, rollback, and compensation. Provider errors are reduced to bounded codes; secrets and arbitrary raw text are not persisted.

### Audit leakage and tampering

Control: the canonical operation/attempt ledger is append-only at its API boundary; corrections append transitions. Business mutation and audit are atomic only in a qualified shared store. Credentials, headers, unrestricted URLs, approval tokens, full sensitive payloads, and raw provider errors are omitted or replaced by bounded digests. Hash chaining detects accidental mutation only and is not claimed to resist an actor with the same Host storage/signing authority.

### Evidence forgery

Control: Host creates receipts from operations it actually mediates. Bind receipts to run ID, capability ID, operation index, bounded request/response digests, outcome, and timing. Guest-provided receipt-like JSON has no authority.

### Supply-chain substitution

Control: build from an immutable source lock with SHA-256 and license metadata. Verify downloaded bytes, exact artifact imports/exports, manifest digest, SBOM, and CI run identity. Mutable `latest` URLs are forbidden.

## Fail-closed events

Close/discard the instance on:

- trap or deadline cancellation;
- memory shape drift outside configured bounds or observed lifecycle assumptions;
- malformed or out-of-bounds response pointers;
- unsupported capability import;
- preparation, refill, or pool health failure;
- any future reset/restore failure;
- Host import protocol violation;
- output cap violation where continued instance health is uncertain.

## Evidence required before security wording

A configuration flag is not evidence. Each relevant goal requires an executable denial test or live probe using the actual Linux-built artifact and Go/wazero dispatch path. Remaining gaps must be listed in the artifact manifest and evidence report.
