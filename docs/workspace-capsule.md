# Workspace Capsule v1

## Status

Implemented in `runtime/workspace` and the wazero `Factory` binding.

This version provides a Host-owned mutable ordinary-file tree at guest path `/workspace`. The same tree can be handed, without a per-Run tree copy, to a sequence of fresh or never-served single-use reactor instances. It does not preserve an interpreter, Python heap, WebAssembly memory, Broker, file descriptor, socket, process, or native handle.

## Lifecycle

```text
Host creates Workspace Manager under a private 0700 base
→ Host creates ws-<random 128-bit id> and optionally copies trusted initial files once
→ wazero Factory acquires one exclusive Runner lease
→ prepared module initializes with virtual, closed `/workspace` and `/tmp` gates
→ Runner checks out a never-served module
→ `/workspace` gate activates and the Host creates a fresh private `/tmp`
→ guest reads/writes the continuing `/workspace` and run-local `/tmp`
→ module and all WASI descriptors are closed and discarded
→ `/tmp` is removed before the workspace lease can be released
→ the same workspace directory is opened by the next never-served module with a new empty `/tmp`
→ Runner.Close releases the lease
→ Host destroys the workspace, or Manager.Close destroys all inactive workspaces
```

A workspace-bound Runner serializes Runs. Prepared capacity may still hide initialization latency, but two active instances never write the same workspace concurrently.

The gates are part of the security contract. During `_initialize`, `runtime_init`, trusted prepared warmup, and COW image creation, the guest can observe both virtual preopen names but cannot list or open either filesystem. The real workspace root is opened lazily only after checkout. `/tmp` does not even have a backing directory until checkout; activation creates a fresh Manager-owned rooted filesystem and module close destroys it. Filesystem-derived values and live descriptors therefore cannot enter a prepared heap or sealed COW baseline.

## Host API

`RunRequest` contains no workspace field. An untrusted request cannot select a Host path, workspace identity, mount, or source.

```go
base := "/private/host-owned/workspaces" // existing clean 0700 directory
manager, err := workspace.NewManager(base)
if err != nil { /* fail closed */ }

ref, err := manager.CreateFromDirectory(
    "/private/host-owned/materialized-source",
    workspace.DefaultLimits(),
)
if err != nil { /* fail closed */ }

// Small generated inputs can instead be supplied directly as copied values:
// ref, err := manager.Create([]workspace.InitialFile{
//     {Path: "input/config.json", Data: configBytes},
// }, workspace.DefaultLimits())

factory := wazero.Factory{
    Strategy:         engine.StrategySingleUsePrepared,
    PreparedCapacity: 1,
    WorkspaceManager: manager,
    WorkspaceRef:     ref,
    WorkspaceOwner:   "runner-opaque-identity",
}
runner, err := factory.New(ctx, wasm, runtime.DefaultRunConfig())
```

`WorkspaceRef` is random opaque identity, not a path. The Manager alone resolves it. All three Factory fields are required together. A second Runner cannot acquire the same workspace until the first Runner closes.

Initial files or a trusted source directory are copied once when the Host provisions the workspace. `CreateFromDirectory` accepts only a clean absolute Host path, opens it through `os.Root`, validates every entry as an ordinary same-filesystem object, streams it under the configured limits, preserves empty directories, canonicalizes modes, and rejects the whole import atomically on any invalid entry or detected mutation. The source path is neither retained nor returned. Sequential Runs do not copy the tree or compute/apply a patch; they reopen the resulting managed root and observe prior ordinary-file mutations directly.

## Filesystem model

Allowed:

- directories;
- regular files;
- create, read, write, bounded overwrite, shrink, rename, and delete;
- canonical relative UTF-8 paths;
- bounded executable permission bit as file metadata only.

Rejected or unavailable:

- absolute paths, `..` traversal, non-canonical paths, NUL, backslash paths, invalid UTF-8, overlong paths/components, and macOS `..namedfork` forms;
- `.git` at any depth;
- symbolic links and hard links;
- FIFO/named pipes, Unix sockets, character/block devices, device nodes, and unknown file types;
- mount/bind-mount or cross-filesystem traversal;
- sparse growth through seek/write or growing truncate;
- extended attributes, ACLs, Linux file capabilities, setuid/setgid/sticky semantics, ownership transfer, and stable Host inode/device identity;
- `/proc`, `/sys`, `/dev`, cgroupfs, debugfs, Host home directories, or arbitrary Host mounts;
- file-descriptor passing and native handles.

Guest-visible `stat` projects device and inode to zero and normalizes link count. `/workspace` limits bound entry count, total bytes, per-file bytes, and depth. `/tmp` uses the same ordinary-file model with stricter defaults: 1,024 entries, 64 MiB total, 16 MiB per file, and depth 16. Unsupported stored objects fail acquisition; unsupported objects encountered while serving fail the operation.

The Manager base and generated workspace roots are private Host directories. The Host is trusted and must not mutate a leased root through an out-of-band path. Guest code never receives that path.

## Persistence semantics

Persistence is file-only and non-transactional:

- writes completed before a guest exception, timeout, or cancellation remain in the workspace;
- no automatic rollback, overlay commit, result snapshot, or patch is produced;
- `/workspace` lifetime is independent of each disposable reactor, but not independent of the Manager process in v1;
- `Manager.Close` removes all inactive managed roots;
- `/tmp` is per-instance scratch state: it is created only on checkout, removed after module close on success/error/timeout/cancellation, and never becomes part of workspace continuation.

Applications that need state transfer must write explicit files. Pickle or another opaque format is stored only as bytes; the Host does not deserialize it. Python globals, imported modules, monkey patches, random state, threads, atexit handlers, open files, Broker references, and WebAssembly memory never continue to the next Run.

## Source provisioning and executables

Git is only one possible Host-side provisioner. A trusted Host may materialize a repository working tree, dataset, object-store blob, generated template, document conversion, or API snapshot as a directory and copy it once with `CreateFromDirectory`; small generated values can use `InitialFile`. The reactor receives the managed files, not the source path, resolver, credentials, transport, or command.

This does not authorize general execution. Workspace v1 provides no shell, `exec`, arbitrary argv, Git, package manager, compiler, subprocess, socket, or Host executable. A fixed native helper may be an implementation detail of a separately reviewed Host provisioner or typed Broker operation; it is never exposed as guest-controlled command execution.

## Instance verification

`verification/workspacecapsule.Verify` runs the actual artifact through at least eleven disposable instances and emits a deterministic `workspace-capsule-verification/v2` report. The CLI entry point is:

```bash
go run ./cmd/apyrun-verify-workspace \
  -artifact /absolute/path/to/reactor.wasm \
  -strategy single-use-preinitialized \
  -stress-iterations 100 \
  -output workspace-verification.json
```

The verifier creates and removes its own source, Manager, workspace, scratch roots, and local-only barrier capability. It binds the report to the artifact SHA-256 and checks:

1. opaque workspace identity;
2. an actual fresh-instance engine strategy;
3. exclusive Runner lease;
4. detached one-time source copy;
5. ordinary-file continuation;
6. fresh Python globals;
7. fresh `/tmp` after success;
8. canonical reporting of an intentional Guest failure;
9. persistence of completed workspace writes from that failed Run;
10. fresh `/tmp` after failure;
11. fresh Python globals after failure;
12. Guest create/list behavior for ordinary files and directories;
13. Guest `stat` distinction between regular files and directories;
14. ordinary-file rename and truncate behavior;
15. ordinary-file and directory deletion;
16. symbolic-link and hard-link rejection;
17. workspace quota enforcement without committed over-limit bytes;
18. `.git` metadata rejection;
19. denial of ambient Host filesystem paths;
20. reset of a one-call Broker budget across two disposable instances;
21. configured timeout interruption of a non-terminating instance;
22. persistence of workspace writes completed before timeout;
23. fresh `/tmp` after timeout;
24. Host context cancellation after a deterministic Guest-to-Host barrier;
25. persistence of workspace writes completed before cancellation;
26. fresh `/tmp` after cancellation;
27. construction of one independent Broker per Run;
28. absence of verifier Host paths in Guest responses;
29. Runner/module/filesystem/lease cleanup;
30. Manager root cleanup.

The timeout and cancellation probes use separate workspace-bound Runners over the same opaque Ref, so each probe starts from a clean engine/pool while retaining only the ordinary workspace tree. The timeout Runner uses at least a five-second budget; the cancellation Runner uses at least ten seconds so fresh CPython initialization is outside the barrier race. Cancellation is not sleep-timed: after writing `/workspace` and `/tmp`, the Guest invokes a one-call, local-only `fetch_many` barrier through the normal typed Broker ABI; the Host cancels only after receiving that signal. No network request or external side effect occurs.

`-stress-iterations N` accepts `0..1000`. A nonzero value appends four portable checks and a `stress` summary: all requested Runs completed, the workspace counter advanced exactly once per instance, Python globals stayed fresh, and `/tmp` stayed fresh. On Darwin and Linux it also records open-FD before/after/delta and adds a fifth check requiring growth of at most two descriptors; unsupported platforms omit those fields and the check instead of reporting an untested PASS. Stress still uses the same exclusive workspace and the final cleanup/lease oracle.

WASI snapshot-preview1 exposes object type but not POSIX permission bits in filestat. Therefore the live verifier checks regular-file/directory type, while Host-side `0644`/`0755` canonicalization remains covered by `runtime/workspace` tests; the report does not invent a Guest-visible permission claim.

A `verified` report means all checks passed on that artifact and requested strategy. It is evidence for this bounded lifecycle contract, not a claim about untested artifacts, Host restart persistence, rollback, arbitrary native execution, or external side effects. The command exits `0` for `verified`, `1` for a completed report containing failed checks, and `2` for verifier/setup/protocol failure.

## Deliberately deferred

- persistent Manager catalog across Host restart;
- TTL, garbage-collection policy, and crash recovery;
- immutable snapshots, lineage, diff/patch export, rollback, fork, overlayfs, and reflink optimization;
- safe relative symlink support;
- source resolver registry;
- workspace selection in remote service protocols;
- parallel readers or transactional multi-writer semantics.

These can be added without changing the default single-use reactor contract. Long-lived interpreter sessions remain a separate product/API and are not a workspace optimization.
