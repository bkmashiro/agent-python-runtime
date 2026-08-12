# Bounded developer tools

Pysolate provides familiar developer operations as bounded Python semantics, not
as a shell or arbitrary executable surface.

## Workspace operations

The `pysolate.workspace` Guest package operates only below `/workspace` and
provides:

```python
from pysolate import workspace

workspace.read_text("README.md")
workspace.write_text("reports/result.json", "...\n")
workspace.list("src")
workspace.walk("src", max_files=500)
workspace.glob("*.py", path="src")
workspace.search("TODO", path="src", glob="*.py")
workspace.stat("src/app.py")
workspace.digest("src/app.py")
workspace.diff("before.py", "after.py")
workspace.mkdir("reports")
workspace.copy("a.txt", "reports/a.txt")
workspace.move("draft.txt", "reports/final.txt")
workspace.remove("obsolete.txt")
```

These replace the common semantics of `cat`, `grep`, `find`, `ls`, `mkdir`,
`cp`, `mv`, `rm`, `diff`, and `sha256sum`. They do not start subprocesses.
Every path is canonical, relative, excludes `.git`, and rejects symlinks. Text,
files walked, bytes read, matches, and diff output all have fixed upper bounds.
The outer Run timeout remains the backstop for expensive regular expressions.

The implementation uses ordinary Python filesystem APIs inside the Guest, so
its authority is exactly the rooted WASI `/workspace` mount. It does not consume
a typed Host-tool call.

## Read-only Git

`apyrun` can bind one Host-selected local repository:

```json
{
  "git_read": {
    "repository_id": "project-fixture",
    "repository_path": "/host-owned/private/path",
    "max_entries": 1000,
    "max_patch_bytes": 1048576,
    "max_blob_bytes": 1048576
  },
  "max_tool_calls": 8
}
```

`repository_path` is private operator configuration. It is not included in the
capability spec, Python projection, grant, receipt, or Guest result. The Guest
receives only:

```python
git.status()
git.diff()
git.log(limit)
git.show(revision, path)
git.list_refs()
git.resolve_revision(revision)
```

These operations use `go-git` in-process behind the existing Capability Broker.
They never execute the system `git` binary and never invoke Git hooks, external
diff/textconv, credential helpers, submodule commands, or a network transport.
All results are schema-validated and bounded. Blob and diff operations accept
only bounded UTF-8 content.

The initial read-only Git adapter and a mounted workspace are independently
Host-provisioned snapshots. They are coherent at Run admission only when the
operator creates both from the same source state. Git queries do not reflect
subsequent Guest workspace writes. A future transactional Git-write adapter must
bind repository metadata and workspace checkpoint identity explicitly rather
than pretending these independent views remain synchronized.

## Not provided

This surface does **not** add:

- shell execution or `subprocess`;
- arbitrary binaries;
- arbitrary Host paths;
- Git clone/fetch/pull/push;
- Git add/commit/checkout/merge;
- Git hooks, filters, credential helpers, or remote URLs.

Those boundaries require separate grants, budgets, receipts, and replay rules.
