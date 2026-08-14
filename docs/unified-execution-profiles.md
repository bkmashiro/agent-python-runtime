# Unified execution profiles

Status: **Experimental vertical slice**. The existing fresh Wazero Run path remains Current. Native placement, private capability RPC, and gVisor lifecycle become Current only after the real Linux gates in this document pass.

## Purpose

Unify the Agent-facing Python and Host-owned capability/evidence contracts without pretending that WASM and native sandboxes provide equivalent isolation or determinism.

```text
Durable Agent session
├── capability/effect/placement ledgers (Host)
├── fresh or COW Pysolate invocation
└── optional native workspace
    └── disposable native compute lease
```

The common object is the request, capability Plan/Broker, result/effect contract and evidence relation. Interpreter state, file descriptors, sockets, `/tmp`, call stacks and heaps never migrate between profiles.

## Profiles

### `pysolate_wasm`

Fresh bounded CPython/WASI, no ambient Host authority, no live native workspace, no shell, no package installation and no arbitrary network. The bridge is the fixed Wazero Host function. Exact single-flight remains restricted to effect-free, no-workspace, no-Broker invocations.

### `native_sandbox`

Ordinary native Python with profile-declared local filesystem, shell/subprocess and native-package compatibility. It is a compatibility superset, not a security or determinism superset. Host tools still require the same sealed Plan and Broker and cross a private, run-bound RPC channel. No Handler or ambient Host credential enters the sandbox; one expiring channel credential is injected solely for that bound RPC channel.

The first qualified implementation target is pinned gVisor/runsc on Linux. gVisor is a userspace application kernel/OCI runtime, not a hardware VM. A local process implementation is only an insecure contract fixture.

## Artifact and shard identity

`pysolate.execution-artifact.v1` separates a verified WASM distribution from a native OCI artifact. The native backend independently hashes the pinned OCI image config JSON and the exact extracted rootfs tree before Broker or process creation; the rootfs, not a mutable image tag, is mounted for execution. A native artifact is never accepted by the `wasm32-wasip1` verifier. Both artifact kinds bind their backend, profile and shard identity.

`ShardProfile` binds:

- shard ID and execution-profile ID;
- sorted qualified import roots;
- artifact and manifest identities;
- optional prepared/COW baseline identity;
- idle policy.

The first instantiated shard is `plain` / `base`. Static imports choose the smallest Host-qualified shard. A future `numpy` / `numpy-core` shard has a separate artifact and COW pool and is created lazily. Unknown imports, dynamic package installation and unavailable shards route native or fail typed-unavailable; they never mutate a served Pysolate instance.

### Growable COW memory

The experimental Linux COW path accepts one exported, non-imported linear memory when the artifact declares a finite maximum. The sealed memfd separates the prepared baseline extent from the maximum virtual mapping extent: prepared non-zero pages are materialized, baseline zero pages and the growth tail remain sparse, and each request receives a `MAP_PRIVATE` mapping. `memory.grow` exposes zero pages up to the declared maximum without changing the sealed baseline. Every served, failed, cancelled or over-maximum physical execution is destroyed; an executed mapping is never reset and returned to a pool. A later checkout creates a new private mapping, and post-start failures are never transparently replayed on another profile.

The real ARM64 Linux treatment for the current 128 MiB → 512 MiB CPython/WASI artifact is recorded in [`evidence/linux-cow-growable-outcome.json`](evidence/linux-cow-growable-outcome.json). It is an experimental correctness and bounded-resource observation, not production certification or a latency claim.

## Placement

`pysolate.placement-decision.v1` is Host-authored and binds request, analyser version, state class, selected backend, reason, optional shard, and parent decision.

Rules:

```text
portable + positively qualified plain shard -> pysolate_wasm
native requirement or native state          -> native_sandbox
indeterminate source/import analysis        -> native_sandbox
model risk signal                           -> native_sandbox only
required backend unavailable                -> typed unavailable
```

A model may add risk but may never admit Pysolate. Placement happens before backend execution, workspace acquisition, Broker creation, Guest creation or native process creation.

Implicit promotion is limited to:

1. preflight selection before either backend starts;
2. a new linked native execution after a Host-authored `runtime_unsupported` outcome whose workspace and effect dispositions are both `not_started`.

Python exceptions, import errors, timeout, OOM, cancellation, capability denial, disconnect and ambiguous completion never trigger implicit replay.

## Capability transport

The sealed `pysolate.capability-plan.v4` and `Broker.Call` remain authoritative.

- WASM: generated projection -> `_agent_runtime_host.call` -> Host function -> Broker.
- Native: the identical generated projection -> native `_agent_runtime_host.py` -> bounded HTTP/JSON over a private Unix socket -> channel registry -> same Broker.

`pysolate.capability-rpc.v1` binds channel, invocation, execution and Plan identities. The credential is carried only in the private transport authorization header, expires, and is revocable. Completed exact retries return the cached Broker response without redispatch. Same call ID with changed bytes, in-flight duplicate/lost-response ambiguity, identity mismatch, oversize input, expiry and revocation fail closed.

The Unix socket is an implementation transport, not an authority grant. A future vsock transport must preserve the exact protocol through a dialer boundary.

## Native lifecycle

Native state classes are:

- `portable_value`: eligible for a later Pysolate admission;
- `native_workspace_ref`: requires the native workspace but not a permanent compute instance;
- `native_process_ref`: requires an explicit bounded live-process lease;
- `opaque`: native-affine and never inferred portable.

Lease classes are one-shot, workspace grace and live process. No-state compute is destroyed immediately. Workspace may outlive compute under a short measured grace. Live process state exists only under an explicit deadline. No dirty served instance crosses session boundaries.

The gVisor workspace root is runtime-owned. Agent input cannot nominate a Host path. The workspace Manager issues an exclusive lease and only that lease can expose a privileged bind source. Every invocation records before/after tree identity and releases the lease after its disposable container exits. The current bind-mount vertical slice enforces bounded post-run snapshots but does not yet provide a live filesystem quota; this is an explicit Experimental limitation, not production multi-tenant readiness.

## Qualified gVisor vertical slice

The repository includes an Experimental OCI backend in `runtime/engine/native` and bounded probes in `cmd/pysolate-native-probe` and `cmd/pysolate-native-workspace-probe`. Qualification used pinned `runsc release-20260810.0` on Linux/arm64 with:

- verified OCI image-config identity plus deterministic rootfs tree digest checked before Broker or process creation;
- read-only rootfs, pre-created mountpoints, private tmpfs `/tmp` and `/dev`;
- isolated network namespace without external interfaces, and `host-uds=open` only for the private bind-mounted Broker socket;
- OCI cgroup memory+swap and PID limits, no-new-privileges and an empty process capability set;
- direct privileged launcher ownership of runsc. The backend intentionally does not invoke `sudo` internally because that breaks process-group cancellation;
- unconditional `runsc delete --force`, empty-container-list reconciliation, cgroup removal, control-root namespace unmount and scratch removal;
- one-shot workspace reactivation across different container/execution identities.

Real gates covered success, Host-tool RPC, network denial, timeout, exit-17 crash, output flood, dirty-page OOM (exit 137), artifact substitution, workspace write/reactivation and portable re-placement. `pysolate.lifecycle-evidence.v1` records aggregate CPU, cgroup memory, RSS, PSS/private dirty, I/O, PID and cleanup observations. These bounded results qualify the vertical slice, not gVisor generally or production safety.

## Current implementation seams

- `runtime/execution_contract.go`: backend/artifact/shard/state/lease contracts.
- `runtime/capabilityrpc`: private channel and replay/ambiguity contract.
- `native/python/_agent_runtime_host.py`: native implementation of the existing generated bridge.
- `runtime/placement`: deterministic analyser and L1/L2 orchestrator.
- `runtime/engine/native`: verified OCI bundle, bounded runsc lifecycle, cgroup/resource sampling and workspace bind adapter.
- `runtime/lifecycle`: backend-neutral body-free lifecycle/resource evidence.
- `runtime/engine/wazero`: existing WASM adapter with lazy workspace acquisition after request admission.

## Non-goals

- Pysolate live filesystem or native package installation;
- migration of WASM/native interpreter state;
- post-start transparent replay;
- public Broker TCP endpoint;
- generic Host filesystem, URL, credential or shell capability;
- reuse of dirty native instances;
- production Firecracker/Kata support in this slice;
- a NumPy/WASI build without measured demand.
