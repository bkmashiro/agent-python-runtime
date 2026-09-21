# Live-I/O pilot data

These JSON Lines are the raw five-process pilot samples discussed in
`docs/semantic-scheduling-study.md`.

- Source baseline before the implementation commit: `6268f28c6bf08d40df358e42608b75cfd6e3621e`
- Guest artifact SHA-256: `d75b6f9cadc9fd0d04b111da6cff4823bd8b592e088941a6ba1d6208d8aa3729`
- Host: macOS arm64 (`Darwin 25.4.0`)
- Go: `go1.26.0 darwin/arm64`
- Preparation: portable copy
- Fixture: two Runs, one running slot, two resident slots, two external-tool slots, 100 ms synthetic Host delay
- Sample count: five separate processes per policy

Each process ran one of:

```sh
go run ./cmd/pysolate-queue-bench \
  -guest dist/pysolate.wasm -mode executor \
  -tasks 2 -active 1 -resident 2 -tool-active 2 \
  -heap 0 -hold 100ms -cow=false -external-io=false

go run ./cmd/pysolate-queue-bench \
  -guest dist/pysolate.wasm -mode executor \
  -tasks 2 -active 1 -resident 2 -tool-active 2 \
  -heap 0 -hold 100ms -cow=false -external-io=true
```

`setup_ns` includes process-local Runner preparation and is not part of the
live-wait comparison. RSS fields are null because this harness reads Linux
`/proc`; no memory result is inferred from these macOS samples.