# Bounded developer tools

Pysolate provides familiar developer operations as bounded Python semantics, not
as a shell or arbitrary executable surface.

## Generic filesystem operations

The `pysolate.fs` Guest package operates on the two filesystems visible to the
Guest:

```text
/workspace   task state; snapshotted and optionally continued across Runs
/tmp         per-Run scratch; deleted at the end of the Run
```

Both mounts use the same rooted filesystem implementation, but they are separate
instances with separate Host directories, accounting limits, and lifecycles.
Every API path must explicitly name one mount:

```python
from pysolate import fs

fs.read_text("/workspace/README.md")
fs.write_text("/tmp/result.json", "...\n")
fs.list("/workspace/src")
fs.walk("/workspace/src", max_files=500)
fs.glob("*.py", path="/workspace/src")
fs.search("TODO", path="/workspace/src", glob="*.py")
fs.stat("/workspace/src/app.py")
fs.digest("/workspace/src/app.py")
fs.diff("/workspace/before.py", "/tmp/after.py")
fs.mkdir("/tmp/output", parents=True)
fs.copy("/workspace/input.json", "/tmp/output/input.json")
fs.move("/tmp/output/input.json", "/tmp/output/final.json")
fs.remove("/tmp/output/final.json")
```

These replace the common semantics of `cat`, `grep`, `find`, `ls`, `mkdir`,
`cp`, `mv`, `rm`, `diff`, and `sha256sum`. They do not start subprocesses.
Paths are canonical absolute Guest paths, are restricted to the two visible
mounts, exclude `.git`, and reject symlinks. Text, files walked, bytes read,
matches, and diff output all have fixed upper bounds. Copy may cross mounts;
move must remain within one mount because cross-mount rename is not atomic.

`workspace` is reserved for the durable-state lifecycle—identity, snapshots,
Capsules, continuation, and replay—not ordinary filesystem commands. Currently
both mounts are provisioned when the Host configures a mounted workspace; `/tmp`
is not yet independently mounted for Runs without one.

## Read-only local Git

The operator can bind one Host-selected local repository through:

```json
{
  "git_read": {
    "repository_id": "project",
    "repository_path": "/host-selected/private/path",
    "max_entries": 1000,
    "max_patch_bytes": 1048576,
    "max_blob_bytes": 1048576
  }
}
```

`repository_path` is Host-private. Specs, grants, Guest source, results and
receipts carry only the opaque `repository_id`.

The generated typed Python namespace exposes:

```python
git.status()
git.diff()
git.log(20)
git.show("HEAD", "README.md")
git.list_refs()
git.resolve_revision("HEAD")
```

The adapter uses `go-git` in-process. It does not invoke the Host `git` binary
and does not expose remotes, network transports, hooks, external diff/textconv,
credential helpers, submodule traversal or mutation. Entries, patch bytes and
blob bytes are bounded. Worktree diff rejects symlinks and non-ordinary files.
Calls pass through the ordinary capability Broker and produce receipts.

## Snapshot coherence

The mounted `/workspace` and `git_read` repository are independently
Host-selected inputs. A source directory is copied into `/workspace`; it is not
a live bind mount. Therefore Git inspection describes the repository snapshot
selected by the Host, while later Guest writes change only `/workspace`.

A coherent developer flow in the current release is:

1. inspect the selected repository with read-only Git calls;
2. inspect and modify `/workspace` with `pysolate.fs` or ordinary Python file APIs;
3. inspect the Runtime's initial/final workspace manifests and derived diff.

A future repository-to-workspace binding may relate one Git baseline identity to
one imported workspace snapshot. The current API does not claim that relation.

## Deliberately absent

This surface does **not** add:

- shell execution or `subprocess`;
- arbitrary binaries;
- arbitrary Host paths;
- Git mutation, commit, checkout or merge;
- Git clone, fetch, pull or push;
- credentials or network authority.
