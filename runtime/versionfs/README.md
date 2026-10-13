# versionfs

`runtime/versionfs` is a Host-owned, in-memory immutable input filesystem. Host changes are explicit (`WriteFile`, `Mkdir`, `Remove`); each `Store.View()` is a read-only `io/fs` snapshot. It does not watch disk paths, persist state, provide a writable Guest overlay, or perform dependency/freshness analysis.

```go
store, err := versionfs.New(versionfs.DefaultLimits())
if err != nil { return err }
if err := store.Mkdir("src"); err != nil { return err }
if err := store.WriteFile("src/main.py", []byte("print('hello')\n")); err != nil { return err }
view := store.View() // O(1) root capture
info, err := view.Stat("src/main.py")
identity, _ := view.Identity("src/main.py") // same snapshot as Stat/Open
_ = info.Sys().(versionfs.Identity) // FileInfo.Sys carries identity
_, _, _ = view, identity, err
```

`Identity.ResourceID` is an opaque random identifier scoped uniquely in practice across stores. Replacing a file retains that ID and increments `Version`; remove then create allocates a new ID. Directories also have identities; their versions increment whenever direct visible children are added or removed. Replacing file contents does not change its parent directory version. `Revision()` is an opaque store ID plus monotonically increasing publication generation, never a content hash. Generations/versions fail rather than wrap.

Every publication copies caller-provided bytes; `ReadFile` returns a separate copy. Immutable nodes and payloads are shared between snapshots. Open files hold their original immutable node, and ReaderAt/Seek/Read never expose internal slices. View reads take no Store lock. Host writes are serialized and publish only after validation, quota checks, identity allocation, and path-copy construction succeed.

Paths follow `fs.ValidPath`, additionally rejecting backslashes, NUL, invalid UTF-8, and paths deeper than `MaxDepth`. `.` is the read-only root. Files and directories both count toward `MaxFiles`; regular-file bytes count toward `MaxBytes` and `MaxFileBytes`. Limits apply to each current tree, not the aggregate memory of old Views still retained by callers. Directory Stat reports size 0, mode `ModeDir|0555`; file mode is `0444`; modification time is zero. Directory listing order is lexical. `ReadDir(count > 0)` returns EOF only when no entries remain; `count <= 0` returns all remaining entries with nil error.

Mutations path-copy each ancestor directory and its immediate child map. Cost therefore includes copying the fanout of affected directories; this implementation does not claim arbitrary-fanout O(depth) updates. Intended for bounded task inputs, not unbounded archives. No database, persistence, whole-tree hashing, or dependency logic is included.
