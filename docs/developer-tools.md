# Developer operations without an ambient computer

Pysolate does not add a second filesystem SDK. Agent code uses ordinary Python
against the Guest-visible WASI mounts:

```python
from pathlib import Path
import difflib
import hashlib
import re
import shutil

text = Path("/workspace/README.md").read_text(encoding="utf-8")
files = sorted(Path("/workspace/src").rglob("*.py"))
Path("/tmp/report.txt").write_text("...\n", encoding="utf-8")
shutil.copyfile("/tmp/report.txt", "/workspace/report.txt")
```

`pathlib`, `open`, `re`, `difflib`, `hashlib`, and `shutil` are computation and
filesystem presentation. They grant no Host authority. CPython lowers ordinary
file operations through WASI into the Host-configured mounts.

## Filesystem boundary

- `/workspace` is Host-selected task state. It may be retained, snapshotted, or
  exported as a Capsule according to the Host disposition.
- `/tmp` is a separate per-Run scratch filesystem and is deleted when the Run
  ends.
- Both are separate instances of the same bounded Host `rootedFS`
  implementation, with separate roots, quotas, and accounting.
- No Host path, ambient filesystem, shell, subprocess, package installer, or
  socket is made available.

The mounted filesystem currently enforces the authority boundary. Individual
`Path.read_text()` or `Path.write_text()` calls are **not** typed Host capability
calls and do not receive per-call Broker receipts. Runtime evidence records
bounded initial/final workspace state, not every Python or WASI filesystem
operation.

The current mounted workspace uses direct-write semantics: bytes written before
a Guest failure may remain in that workspace depending on Host disposition. It
is not a transactional attempt overlay.

## Read-only Git inspection

Git repository semantics remain a Host-mediated typed capability because they
operate on a separately selected Host repository and metadata model:

```python
status = git.status()
commits = git.log(10)
readme = git.show("HEAD", "README.md")
refs = git.list_refs()
resolved = git.resolve_revision("HEAD")
```

The implementation uses `go-git`; it does not execute a system Git binary. The
Host policy binds an opaque repository identity, a private local path, and
entry/blob/patch bounds. Guest-visible calls cannot provide a Host path,
network target, credentials, hook, external diff, textconv, remote operation,
or write operation.

This is intentionally not full Git compatibility. Local mutations and remote
effects require separate future contracts.

## Workflow boundary

A current developer workflow is:

1. inspect a Host-selected repository through bounded read-only Git tools;
2. inspect and transform the mounted workspace with ordinary Python;
3. stage scratch data under `/tmp` when useful;
4. copy the selected durable result into `/workspace`;
5. inspect the Runtime's initial/final workspace manifests and derived diff.

The Git repository and mounted workspace are independently Host-bound. Current
Git calls do not automatically observe Guest workspace mutations.
