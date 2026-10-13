# Versioned input files and optional recording

`RunFiles` mounts a Host-owned immutable file view at `/workspace`. The Host can publish a new version while existing Guests keep reading their original view. Recording is optional and independent of tool record/replay.

Use the existing [private Workspace](workspace.md) when Python must edit files. This API is a smaller read-only input filesystem, with explicit Host updates. It does not mirror a disk directory or detect edits made outside its Store.

## Publish and execute

Embedding excerpt, with an existing Runner and context:

```go
store, err := versionfs.New(versionfs.DefaultLimits())
if err != nil { return err }
if err := store.WriteFile("policy.json", []byte(`{"budget":300}`)); err != nil {
    return err
}
view := store.View()
out, err := runner.RunFiles(ctx, `
import json
with open('/workspace/policy.json') as f:
    result = json.load(f)['budget']
`, nil, view)
```

Imports: `github.com/bkmashiro/agent-python-runtime/runtime/versionfs` and the root package for Runner. `New` and workspace-prepared constructors support this mount. A plain prepared image has a different preopen layout and is rejected. Each execution still creates and closes its own Guest.

Host operations are `WriteFile`, `Mkdir`, and `Remove`. Parents must exist. Directories must be empty before removal. There are no symlinks, hard links, writable Guest handles, permission mutation or file watchers. Changing the available Python packages still uses the artifact build workflow; this mount does not replace the Guest's bundled stdlib VFS.

## Identity without rescanning files

A Store generates one random namespace at construction. New resources receive IDs from that namespace and a monotonic publication counter. Overwriting a file preserves its resource ID and advances its version. Deleting and recreating the same path creates a new resource ID.

`view.Identity(path)` returns `{ResourceID, Version}`. `view.Revision()` identifies the Store's publication generation. Neither is a content digest. Equal bytes published again can have different versions. Ancestor directory versions advance on descendant changes, including changes visible through directory-entry metadata. This conservative rule can report more changes than a future dependency validator needs.

A View retains the immutable root in constant time. A mutation copies the affected ancestor directory maps and shares other nodes and bytes. Update cost depends on those directories' fanout. Input bytes are copied on publication, and `ReadFile` returns a copy, so callers cannot mutate an existing view through a retained slice.

Default limits are 4,096 files/directories, 256 MiB of file contents, 64 MiB per file and depth 32. These are engineering defaults for the **current tree**. Retained older Views keep their referenced data alive and need a Host retention policy. They are not covered by a global history-memory quota.

## Optional FS records

```go
journal, err := fsjournal.Open("private/fs.sqlite")
if err != nil { return err }
defer journal.Close()
recorder, err := journal.Begin("run-42", view.Revision(), fsjournal.Options{
    CaptureContents: true,
})
if err != nil { return err }
out, err := runner.RunFiles(ctx, source, inputs, view,
    pysolate.FileRunOptions{Recorder: recorder})
run, events, readErr := journal.Events("run-42")
```

`runtime/fsrecord` contains only event types and the Sink interface. `runtime/fsjournal` supplies the SQLite implementation. Importing the root runtime does not require importing this recording backend. A recorder belongs to one attempt and one exact View; `RunFiles` finalizes it after Guest handles close.

Records contain operation, path, resource/version when known, returned byte offset/count, error and bounded operation details. They describe Host filesystem adapter calls. Python buffering can read more bytes than the program subsequently uses. WASI may fetch more directory entries than fit in its response buffer. Operations rejected before reaching the adapter and the bundled stdlib's internal VFS are outside this log. These records do not establish semantic dependency or causal influence.

With content capture disabled, the content loader is never called. When enabled, the first read of an immutable file version stores its whole payload once under `(ResourceID, Version)`; repeated reads refer to the same row. Metadata-only operations do not capture file contents. This avoids per-event payload copies and per-read hashing. Different versions are not deduplicated by content.

```go
id, err := view.Identity("policy.json")
if err != nil { return err }
references, err := journal.Find(id.ResourceID, &id.Version)
```

The SQL index on resource, version, run and sequence supports this reverse lookup. It answers which logged operations referenced a version. There is no automatic invalidation or model notification.

## Failure and persistence boundaries

Events commit in batches. `Finish` flushes the last batch before marking the recording complete. A crash can lose the pending tail; the persisted prefix remains incomplete. This log does not provide durable-before-observation replay.

Program outcome and recording completeness are separate. A Python exception can have a complete log. Recording overflow or storage failure is sticky: the attempt is cancelled, subsequent Host tool dispatch is refused, and a Python `try/except` cannot turn it into successful output. Already completed effects are not undone. This does not make tools retry-safe.

The caller owns the database and its parent directory. Captured inputs may contain private data. Retention and deletion of journal databases remain Host responsibilities.

## Existing APIs

Deterministic clocks/random state now live in an execution environment independent of Journal. `RunRecorded`, recorded prepared constructors and durable recovery retain their previous contracts. New FS records do not enable combinations previously rejected by tool replay.

The HTTP Workspace run endpoint still returns SHA-based before/after snapshots by default. Set `"changes": false` to skip these snapshots and their full-file hashing when only the result is needed. Existing bounded-workspace validation remains active. Explicit snapshot endpoints are unchanged.

[Validation and small Host-only benchmarks](fs-foundation-validation.md) document the executed checks and costs.

## Reserved for later

Stable file identities, immutable views, per-operation records and resource/version lookup supply inputs for later read-set collection. This change does not implement dependency validation, time/freshness policies, conditional commits, automatic result repair, tool-observation tracking or counterfactual execution. It also does not remove existing artifact-integrity hashes or invent a persistent journal DAG.
