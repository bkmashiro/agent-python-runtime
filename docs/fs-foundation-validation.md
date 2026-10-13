# FS foundation validation

Local validation on macOS arm64, Apple M4, Go 1.26.0. This changes Host execution/FS code and uses the existing agent-core Guest; it does not rebuild Python or claim Linux COW measurements.

## Checks

- `PYSOLATE_GUEST=... go test ./...`: passed, including root execution, durable recovery, Workspace service and examples.
- `go test -race ./runtime/versionfs ./runtime/fsjournal ./internal/versionmount`: passed.
- `PYSOLATE_GUEST=... go test -race . -run '^TestRunFiles' -count=1 -timeout=8m`: passed in 106.391 s.
- `go vet ./...`: passed without disabling analyzers.
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`: passed. This is a cross-build, not Linux runtime acceptance.
- `go list -deps .`: no SQLite or fsjournal dependency in the root production package.
- `git diff --check`: passed.

Real-Guest tests cover fresh and prepared read-only mounts, file reads and seeks, listing/stat, denied writes, old/new View separation, content capture only for read files, resource/version reverse lookup, and recording failure followed by a caught Python exception and a forbidden subsequent Host effect.

Regression checks found and fixed directory versions that did not advance when child metadata changed, acceptance of buffered events after Store.Close, and record counting outside the batch transaction. Empty content has a persistence regression test.

The first full test invocation found two existing tests using `dist/pysolate.wasm` directly rather than `PYSOLATE_GUEST`; an ignored local symlink to the existing artifact resolved their setup. The first race invocation exceeded its outer time budget. A narrower invocation exposed the new test's 30-second setup deadline expiring during race-instrumented Guest compilation. That test-only setup budget is now two minutes; production deadlines are unchanged. Filesystem handle recording uses standard `io/fs` interfaces, avoiding the syscall-style Seek signature that conflicts with Go vet's standard-method check.

## Small Host-only benchmarks

Commands:

```sh
go test ./runtime/versionfs -run '^$' -bench . -benchtime=100ms -count=1 -benchmem
go test ./runtime/fsjournal -run '^$' -bench '^BenchmarkRepeatedFileReads$' -benchtime=1x -count=3 -benchmem
```

One short versionfs sample:

| Operation | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| View | 12.77 | 24 | 1 |
| WriteFile, parent fanout 1 | 200.3 | 440 | 7 |
| WriteFile, parent fanout 16 | 508.8 | 1,168 | 9 |
| WriteFile, parent fanout 256 | 5,235 | 13,840 | 9 |
| WriteFile, parent fanout 1,024 | 19,171 | 54,840 | 11 |

This shows the cost of copying directory maps. It does not establish an end-to-end speedup.

The journal benchmark records 1,000 reads of one 64-KiB file version, including DB open/schema creation, first content load, batch flush and close. Every run verifies the loader is called once with capture enabled and never with capture disabled.

| Mode | Closed SQLite file | Three group times |
|---|---:|---|
| Metadata only | 106,496 bytes | 251.032, 5.955, 4.778 ms |
| Metadata + content | 172,032 bytes | 12.521, 11.988, 12.227 ms |

The first metadata sample includes process-cold SQLite/Wasm initialization and must not be compared as a steady-state sample. Capturing the single version adds exactly 65,536 bytes in this fixture. Repeating the payload in every event would contain 65,536,000 payload bytes before metadata or encoding. That reference is a byte count, not a measured competing database or compressed-archive baseline. The benchmark calls EnsureContent repeatedly to exercise backend deduplication; the Guest mount additionally caches captured identities for an attempt.

No model calls, paid runs, new workload campaign or VM execution were needed for this slice.
