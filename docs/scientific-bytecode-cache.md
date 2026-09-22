# Optional scientific bytecode cache

## Decision

Keep the default artifact unchanged. NumPy-heavy workloads can opt into additional checked-hash Python bytecode at build time:

```sh
# On an already configured Linux Guest build host:
PYSOLATE_PRECOMPILE_SCIENTIFIC=1 \
PYSOLATE_DIST_DIR="$PWD/dist/scientific" make guest
```

This is an `agent-core` artifact variant, not a new runtime or a new set of permissions. It compiles existing Python sources with the host CPython from the same 3.14 build as the Guest. It does not import NumPy during preparation, retain an executed Guest, or add packages. The ordinary build retains its previous precompilation list. Unsupported flag values fail rather than silently selecting a profile.

Select the result explicitly, for example on Linux:

```sh
go run ./cmd/pysolate-server \
  -guest dist/scientific/pysolate.wasm -cow-data-image
```

`dist/scientific/pysolate.wasm` and its matching manifest are local build outputs, not tracked releases. Keep the original artifact for older durable Runs and bundles: their pinned digest must match exactly.

## Evidence and scope

A function-level import profile identified repeated source compilation as the main NumPy import cost. Precompiling only NumPy reduced the measured small NumPy request from about 7.03 s to 3.47 s. A follow-up compile hook identified remaining standard-library work, chiefly `typing`, `pickle`, `threading`, `pathlib` and their dependencies. The final selection adds these observed import paths without preloading their module state.

Final alternating A/B: three independent processes per artifact, each using COW data-image preparation, one excluded warm-up per case and three measured requests per case. All result oracles passed. Medians below are medians of process medians; ratios are medians of paired ratios. This was a small Linux x86_64 Slurm experiment with `GOMAXPROCS=2`, requesting two CPUs and 2 GiB, not a production concurrency study.

| Workload | Original | Scientific cache | Paired speedup |
| --- | --- | --- | --- |
| CSV/JSON aggregation | 36.23 ms | 7.62 ms | 4.75× |
| NumPy small array sum | 7.04 s | 446 ms | 15.74× |
| NumPy 32×32 matrix operation | 7.03 s | 447 ms | 15.70× |

The gain is import latency, not accelerated numerical computation. The initial decomposition measured the array calculation below 0.2 ms and the matrix calculation below 1 ms. Runner setup is outside request timing; Guest creation and imports are inside it. Request count is 54 measured plus 18 warm-ups across six processes, not 54 independent replications.

## Cost and why this remains optional

- Original artifact: **34,671,228 bytes**, SHA-256 `2c63ce407eae626c89fcd1dd918c713b1f8b75a35a6cf6ca6861466168d0faf8`.
- Scientific variant: **40,734,865 bytes**, SHA-256 `714c326d592cd30fb68bd82c8fa479a35237e704bad7e0606a3800bb059eeb50`.
- Increase: **6,063,637 bytes**, about **17.5%**. Final selection produced 435 cached modules / 7,570,751 bytecode bytes, versus 166 / 1,533,672 originally.
- A separate no-import control used three alternating process pairs, 100 measured short requests per mode after five warm-ups. Ordinary COW median p50 increased **16.36 → 17.47 ms**, about **6.8%**. With data-image enabled, process-median p50s were around 1.76/1.80 ms, with visible variation. There is no supported universal short-script gain.

Therefore no default runtime or artifact is replaced. Select the larger variant when avoided import work outweighs its distribution and instantiation cost. The existing COW data-image memory tradeoffs also still apply.

## Verification and reproduction

- Real Guest qualification passed for both candidates.
- Final scientific artifact passed real Linux durable timeout/recovery and offline CLI tests. Ordinary HTTP budgets, workspace cleanup and early-read permission tests passed with the first candidate; the final candidate additionally passed the affected local service suite.
- Default artifact passed `make check`. The final scientific artifact passed root runtime, durable, offline CLI and service tests. Read-only CLI export, no-overwrite permissions, deleting the DB before replay and large-integer/HTML-safe JSON comparison have regression coverage.
- One earlier Linux lifecycle test used an arbitrary 100 ms budget for its follow-up cold request and returned 408. Its corrected test keeps the original 10 ms cancellation, adds a positive control and gives the follow-up a two-second budget. No runtime deadline or cleanup requirement was relaxed.

Raw final [requests](performance-data/import-cache-292739/runs.jsonl), [summary](performance-data/import-cache-292739/summary.json), [Linux validation](performance-data/import-cache-292739/verification.txt) and [short-script controls](performance-data/import-cache-292739/controls.txt) are retained. The adjacent `probe.go.txt` and `control-probe.go.txt` can be copied into the root package as temporary tests, cross-compiled, and run against explicit `PYSOLATE_GUEST` paths. Initial [phase decomposition](performance-data/import-study-292723/summary.json) is separate from the final comparison. The selected build-time cache list is in `tools/precompile-stdlib.py`.
