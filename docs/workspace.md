# Private workspaces

`runtime/workspace` gives disposable Guests a bounded filesystem at `/workspace` without exposing the source project or its Host path.

## Lifecycle

1. Create a private `0700` manager base.
2. Create a workspace from bounded files or copy an approved source directory once.
3. Acquire an exclusive writer `Lease` using an opaque `Ref`.
4. Take a pre-run `Snapshot` when change review is needed.
5. Execute with `RunWorkspace` using a fresh or workspace-prepared Runner.
6. Take a post-run snapshot and call `workspace.Diff`.
7. Read selected files through `Lease.Files`, then release the lease and close the manager.

A lease can persist across multiple disposable Guests. Python memory and `/tmp` do not persist. Ordinary `Run` has no `/workspace` mount.

```go
before, err := lease.Snapshot()
if err != nil { return err }

out, runErr := runner.RunWorkspace(ctx, source, inputs, lease)

after, snapshotErr := lease.Snapshot()
if snapshotErr != nil { return snapshotErr }
changes := workspace.Diff(before, after)
```

A Python error does not roll back file writes. The private workspace remains inspectable until the Host releases and destroys it. Publication or apply-back to the original source is intentionally separate and is not implemented by `RunWorkspace`.

## Snapshots and diffs

A `Snapshot` contains a sorted file manifest with:

- relative path;
- byte size;
- SHA-256 content digest;
- executable bit.

Its revision is a canonical `sha256:` digest independent of the private backing path. File contents are not included. `Diff` returns deterministic added, modified and deleted entries with before/after metadata. Snapshot and file inspection fail with `ErrWorkspaceBusy` during an active Guest run.

Empty directories are not part of the current file revision. Workspace limits and ordinary-entry validation are rechecked before each snapshot.

## Security boundary

- The source directory is copied once and never mounted into the Guest.
- `.git` remains Host-owned.
- Symlinks, hard links, devices, non-canonical paths and filesystem-boundary crossings are rejected.
- File count, total bytes, per-file bytes and path depth are bounded by Host-selected limits.
- One lease permits at most one active Guest run.
- Tools do not receive the workspace backing path.
- Writable workspaces are excluded from durable replay.

The API does not claim transactional rollback, merge or automatic conflict resolution.

## Executable acceptance

Run the real Host-tool and workspace workflow:

```sh
go run ./examples/workspace-edit -guest dist/pysolate.wasm
```

The example uses:

- a real local HTTP server behind `web.fetch_json(...)`;
- a real parameterized SQLite read behind `catalog.query_one(...)`;
- an idempotent Host-side write behind `audit.record(...)`;
- a fixture containing Python, JSON, Markdown and binary files.

The Guest edits Python and JSON, writes a report, and leaves the source fixture unchanged. The command prints only Guest output, opaque revisions and bounded change metadata. It does not print the Host backing path or SQLite DSN.
