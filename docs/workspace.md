# Private workspaces

`runtime/workspace` gives disposable Guests a bounded filesystem at `/workspace` without exposing the source project or its Host path.

## Lifecycle

1. Create a private `0700` manager base.
2. Create a workspace from bounded files or copy an approved source directory once.
3. Acquire an exclusive writer `Lease` using an opaque `Ref`.
4. Take a pre-run `Snapshot` when change review is needed.
5. Execute with `RunWorkspace` using a fresh or workspace-prepared Runner.
6. Take a post-run snapshot and call `workspace.Diff`, or create a `Checkpoint`
   when another bounded attempt must adopt the exact local revision.
7. Export a bounded `ChangeSet` when changed contents need Host review.
8. Check its touched paths against the original Host tree, then release the lease and close the manager.

A lease can persist across multiple disposable Guests. Python memory and `/tmp` do not persist. `RunWorkspace` starts user code with `/workspace` as its current directory and first import root, so relative file access and imports from the private tree work naturally. Ordinary `Run` has no `/workspace` mount and does not receive this import path.

```go
before, err := lease.Snapshot()
if err != nil { return err }

out, runErr := runner.RunWorkspace(ctx, source, inputs, lease)

after, snapshotErr := lease.Snapshot()
if snapshotErr != nil { return snapshotErr }
changes := workspace.Diff(before, after)
```

A Python error does not roll back file writes. The private workspace remains inspectable until the Host releases and destroys it. Publication or apply-back to the original source is intentionally separate and is not implemented by `RunWorkspace`.

## Attempt handoff checkpoints

`Lease.Checkpoint` returns a versioned `{workspace, revision}` token. After the
current owner releases its lease, a harness can persist that token with its own
task state and use `Manager.AcquireCheckpoint` to transfer the same workspace
revision to a later attempt:

```go
checkpoint, err := lease.Checkpoint()
if err != nil { return err }
if err := lease.Release(); err != nil { return err }

next, err := manager.AcquireCheckpoint(checkpoint, "review-attempt")
if err != nil { return err }
defer next.Release()
```

Re-acquisition takes the exclusive lease first and then re-hashes the bounded
tree. A missing workspace returns `ErrWorkspaceNotFound`; changed bytes return
`ErrCheckpointMismatch`; malformed or unknown token versions return
`ErrInvalidCheckpoint`. Failed verification releases the temporary lease.

The token contains no files and is not a portable snapshot. The manager and
private bytes must still exist on the same Host. This keeps durable Tool history
separate from mutable filesystem storage while giving the harness an explicit,
checked boundary between attempts. Long-term backup, migration and publication
remain harness/storage responsibilities.

## Snapshots and diffs

A `Snapshot` contains a sorted file manifest with:

- relative path;
- byte size;
- SHA-256 content digest;
- executable bit.

Its revision is a canonical `sha256:` digest independent of the private backing path. File contents are not included. `Diff` returns deterministic added, modified and deleted entries with before/after metadata. Snapshot and file inspection fail with `ErrWorkspaceBusy` during an active Guest run.

Empty directories are not part of the current file revision. Workspace limits and ordinary-entry validation are rechecked before each snapshot.

## Reviewable change handoff

`Lease.ExportChanges` turns a trusted pre-run snapshot and the current workspace
into a versioned, deterministic `ChangeSet`. The bundle contains baseline and
result revisions, before/after metadata, deletion records, and bytes only for
added or modified regular files. The caller supplies a second aggregate byte
cap, so exporting a small review never has to allocate the full workspace
limit. `ChangeSet.Validate` rejects unsorted paths, malformed metadata,
incorrect content digests, invalid kinds, and payload accounting mismatches.

```go
changes, err := lease.ExportChanges(before, 1<<20) // at most 1 MiB of payload
if err != nil { return err }

report, err := workspace.CheckDirectoryConflicts(sourceRoot, changes, limits)
if err != nil { return err }
if !report.Clean {
    // Present report.Conflicts for review; do not publish stale edits.
}
```

Conflict checking is read-only and path-granular. An added path must still be
absent; a modified or deleted path must still match its baseline digest, size,
and executable bit. Missing files, occupied additions, changed contents/modes,
symlinks, hard links, special files, and filesystem-boundary crossings are
reported conservatively. Unrelated Host files may change without blocking the
handoff.

The package deliberately does not apply the bundle. Authorization, user
review, atomic replacement, backup policy, Git integration, and merge behavior
remain Host responsibilities rather than Guest authority.

## Security boundary

- The source directory is copied once and never mounted into the Guest.
- `.git` remains Host-owned.
- Symlinks, hard links, devices, non-canonical paths and filesystem-boundary crossings are rejected.
- File count, total bytes, per-file bytes and path depth are bounded by Host-selected limits.
- One lease permits at most one active Guest run.
- Tools and exported bundles do not receive the workspace backing path.
- Writable workspaces are excluded from durable replay.
- Checkpoints identify local state; they do not embed or durably replicate it.

The API does not claim transactional rollback, publication, merge or automatic conflict resolution.

## Checkpoint cost

The checked-in benchmark separates ordinary lease acquisition, checkpoint
creation, and revision-verified acquisition:

```sh
go test ./runtime/workspace -run '^$' \
  -bench '^BenchmarkCheckpointHandoff$' -benchtime=20x -count=3
```

An exploratory Apple M4/macOS arm64 run measured these medians:

- 32 files / 128 KiB: checkpoint `1.109 ms`, ordinary acquire `0.109 ms`,
  checked acquire `1.103 ms`;
- 128 files / 4 MiB: checkpoint `5.142 ms`, ordinary acquire `0.414 ms`,
  checked acquire `5.415 ms`.

These are local implementation measurements, not service SLOs. Both checkpoint
creation and checked acquisition hash file contents, so a complete handoff pays
roughly two bounded tree reads. The result is small enough for attempt
boundaries in these agent-shaped fixtures, but not for every Tool call or inner
loop. Re-run on the deployment filesystem before selecting retention or
migration policy.

## Executable acceptance

Run the real Host-tool and workspace workflow:

```sh
go run ./examples/workspace-edit -guest dist/pysolate.wasm
go run ./examples/agent-core-usecases -guest dist/pysolate.wasm
```

The example uses:

- a real local HTTP server behind `web.fetch_json(...)`;
- a real parameterized SQLite read behind `catalog.query_one(...)`;
- an idempotent Host-side write behind `audit.record(...)`;
- a fixture containing Python, JSON, Markdown and binary files.

The first Guest edits Python and JSON, writes a report, leaves the source fixture unchanged, exports the three changed files, and verifies that the touched source paths remain conflict-free. The common-usecase example additionally imports a local workspace module, reads TOML/CSV/JSONL, safely updates YAML, parses Python with `ast`, computes NumPy statistics, calls a namespaced Host market-data tool backed by a real local HTTP request, and writes normalized CSV/JSON/Markdown results. Commands print only Guest output, opaque revisions and bounded change data. They do not print Host backing paths or service credentials.
