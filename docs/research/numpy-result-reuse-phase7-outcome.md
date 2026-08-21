# Bounded NumPy result reuse: Phase 7 outcome

## Current

Pysolate has one closed native package profile, `numpy-core`, and one bounded typed transport, `numpy_ndarray_c_v1`. The artifact is Pysolate-owned and source-locked; it supports only bounded little-endian numeric C-contiguous arrays. A verified concrete-Wazero producer execution grants authority for one exact result publication. The Host owns the immutable blob, and every logical consumer receives a private materialization in a fresh Guest.

Mechanism correctness and economic usefulness are separate gates. Phase 6 closed the mechanism at `1d788057d3c183dbdafb28030a95967863ba63cd` after an independent `FINAL: PASS blockers=0`. Phase 7 used harness commit `1a6596d2cd238e6c441b7ffa798ecb9b1c01c5e9`, tree `d98612fa162c9eded44e4d6cf82f52f471cc5cd4`, and artifact `sha256:2753cde560f3961a483df53aec334c8fdbb084934e5a62a56d436aea1ae557ad`.

## Measured

The frozen campaign completed all 240 records: 120 on macOS arm64 and 120 on Linux amd64, with 3 trials per treatment. It produced 80 treatment cells and 40 matched economic comparisons. Both platforms contributed 60 cold and 60 preprovisioned records; recompute and reuse each contributed 60 records per platform.

Every record passed result parity, fresh-Guest, no-authority-expansion and no-replay checks. Every reuse blob and all 216 consumer leases reached `consumed`. Linux preprovisioned trials used private COW for every physical Guest and recorded zero placement fallback. Every trial recorded peak resident memory.

No observed cell reached break-even:

| Platform | Profile | Cells | Median speedup ratio | Median reuse penalty | Best ratio |
|---|---|---:|---:|---:|---:|
| macOS arm64 | cold end-to-end | 10 | 0.460100 | 19.493 s | 0.713548 |
| macOS arm64 | preprovisioned | 10 | 0.520715 | 10.981 s | 0.795219 |
| Linux amd64 | cold end-to-end | 10 | 0.432827 | 29.415 s | 0.647821 |
| Linux amd64 | preprovisioned private COW | 10 | 0.430282 | 13.009 s | 0.748183 |

The closest observed cell was macOS preprovisioned `numpy_matrix_medium_gap45000_c4`: four consumers, 45,000 ms lead gap, 524,288-byte payload, ratio `0.7952187480012893`, and net saved time `-16.673223291 s`.

The sparse grid couples operation, payload, consumer count and lead gap. Independent compute, consumer-count and lead-gap thresholds are therefore recorded as `not_identified_from_coupled_sparse_grid`; no interpolation is claimed. Host load was sampled after the campaign rather than per trial, so residual platform differences are not attributed to load. Producer import, compute and encode remain one authority-preserving Guest interval and are not split into invented stages.

Canonical private artifacts:

- macOS JSONL: `sha256:01eb1a864760a1fbf732b20f3f31972dc5f0c6f9fb54484413f45894771df9f3`
- Linux JSONL: `sha256:cfc87552b05ab4122c7aad0fdb3ea4ad31e95ae6f003f7988734e57e39222374`
- local sealed report: `sha256:e807b23840d6b9183bcb72b157e40e46d49b9f1f04a0f68af06bf0d972eb6a3e`
- report identity: `sha256:fa6fa1a8b68df5eb0fc5070660609a9800062769789fcd5f9c0a107680184e1e`

The committed body-safe surface is `docs/evidence/numpy-result-reuse-phase7-v1.json`. `scripts/review-numpy-result-reuse-phase7.py` validates the summary and, when the private canonical artifacts are present, regenerates the report and requires byte-for-byte equality.

## Rejected

The campaign rejects enabling typed ndarray reuse as a performance default for the measured matrix. It also rejects starting a new fan-out or single-flight implementation: multi-consumer coordinates were measured, but all 40 observed comparisons were negative, so feature expansion is not economically justified.

No result extends to pandas, object dtype, non-contiguous arrays, arbitrary ndarray serialization, pickle, durable cache, cross-Run retention, pointer or heap transfer, shared memory, runtime package installation, or generic native plugins. Unknown native effects are not an instruction-level DBI or purity certificate.

## Deferred

A future experiment may test a materially different preregistered workload where producer compute dominates Guest provisioning and private materialization costs. Such work requires a new case matrix and cannot reinterpret this coupled sparse grid. The closed mechanism remains available as a research capability, but production enablement and durable retention remain deferred.
