# fsjournal

`runtime/fsjournal` is an optional, standalone SQLite store for bounded filesystem-operation observations. It intentionally does not share the `durable` schema or claim replay-grade synchronous durability.

## API

- `Open(path string) (*Store, error)` / `(*Store).Close() error`
- `(*Store).Begin(runID, viewRevision string, options Options) (*Session, error)`
- `(*Session).Append(Event) error` — the session assigns increasing `Seq` values.
- `(*Session).EnsureContent(resource string, version uint64, load func() ([]byte, error)) error`
- `(*Session).Finish(runError error) error`
- `(*Store).Events(runID string) (Run, []Event, error)` — a missing run returns `ErrNotFound`.
- `(*Store).Find(resource string, version *uint64) ([]Reference, error)` — nil version means all versions.
- `(*Store).Content(resource string, version uint64) ([]byte, error)` returns a copy of captured bytes; missing content returns `sql.ErrNoRows`.
- `(*Store).QueryPlanForFind() (string, error)` exposes the SQLite plan for the index test.

`Run.Complete` is recording completeness; `Run.RunError` separately reflects the Guest/program outcome. Therefore a Python error can coexist with a complete recording. `RecordingError` marks a failed/incomplete recording. `Event.Detail` must be valid JSON and is size-limited.

## Limits and persistence

Zero-valued `Options` use 100,000 events per run, 4 MiB of queued event metadata, 64 MiB of captured contents per session, batches of 128 events, and 16 KiB per detail value. Metadata accounting is conservative (UTF-8 byte lengths plus fixed row overhead); the queue bound applies to pending, not already committed, batches. Events exceeding a configured bound are rejected, the recording error becomes sticky, and `Finish` returns it without marking the run complete. Accepted, already committed batches remain queryable.

Append writes complete transactions at each batch boundary and Finish writes the final partial batch before marking complete. There is no promise that a just-appended event survives process or power loss before its batch commit; abrupt exit can retain only a committed prefix and leaves the run incomplete. A Close with active sessions does not finalize them. Close is idempotent; subsequent store operations return `ErrClosed`, while finishing a session after Close returns an error and leaves its persisted run incomplete.

The DB file is created with mode 0600 (and chmodded to 0600 on open); the parent directory is created with mode 0700 when needed. Treat the caller-provided path and its parent as caller-owned local storage; this package is not a defense against hostile shared-directory races. WAL sidecars are SQLite-managed. `synchronous(normal)` is deliberate: batching is a performance tradeoff, not a crash-durable-before-observation guarantee.

## Payload model

Events store metadata and a `(resource, version)` reference, not duplicate file bytes. With `CaptureContents: false`, `EnsureContent` returns without invoking its loader. When enabled, content is stored as a SQLite BLOB once per resource/version in `file_versions`; a later call checks for that key before loading. Version keys are identity-based: changed versions retain separate payload rows, with no SHA, cross-version deduplication, or custom pack format. Content capture is bounded and is not a full execution-replay guarantee. The resource/version/run/sequence index supports access lookup only; it does not infer dependency or causality.

## Verification and benchmark

Run `go test ./runtime/fsjournal`, `go test -race ./runtime/fsjournal`, `go vet ./runtime/fsjournal`; `go test -bench=BenchmarkBatchAppend -run=^$ ./runtime/fsjournal` reports throughput and approximate SQLite bytes/event against naively repeated 1 KiB detail payload bytes. This short benchmark is local and not an end-to-end Guest performance claim.
