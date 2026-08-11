# Threat model

## Trust boundary

Agent source, inputs, output schemas and optional compatibility declarations are untrusted. The Guest artifact, distribution manifest, Host profile, resource policy, tool registry and trusted preparation are Host-owned.

## Protected assets

- Host filesystem paths and files outside explicitly selected workspaces;
- Host network, processes, environment and credentials;
- execution budgets;
- capability registration and call limits;
- result and receipt integrity;
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

Guest code can only call tools present in the Host Registry sealed before Guest startup. `pysolate.capability-plan.v3` binds sorted canonical specs, opaque per-Run grant/policy identities and the total call budget; late registration is rejected. Each spec binds capability/version, documentation, effect/playback and handler identities, strict input/output schemas and generated Python projection metadata. Grant policy bytes are Host-owned and cannot be selected or overridden in the Guest call envelope. The Host rejects ambiguous JSON, schema-invalid arguments and schema-invalid handler results, then applies canonical workspace path, per-file size and frozen call-budget checks. The active workspace tool has no Host path or network access.

### Host-authored evidence

The Host replaces Guest receipt, capability-plan and capability-call metric claims. Receipts bind the Host run identity, capability-plan identity, capability, operation index, request digest, response digest and outcome. The top-level response carries the plan identity even for a zero-call Run. The PoC deliberately does not claim durable audit, deterministic replay or transaction evidence.

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

These are documented limitations rather than prompts to add production machinery. They should only be revisited when a concrete product workload is blocked.
