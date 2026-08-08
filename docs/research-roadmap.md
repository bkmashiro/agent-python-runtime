# Research roadmap

This page summarizes current research directions. Historical implementation checklists and agent-session handoff notes are intentionally not part of the public documentation set; Git history retains them when provenance is needed.

## Current

### COW performance and memory density

- measure request latency and memory cost by preparation profile;
- keep no-hook CPython-ready and NumPy-ready shards distinct;
- qualify scheduler admission and refill behavior under mixed workloads;
- test mutation and reset boundaries before considering served-slot reuse.

The implemented COW strategy remains single-use: a served slot is discarded.

The active bounded tranche is [Phase 6: NumPy-ready COW density and admission qualification](phase6-numpy-density.md). It extends the reviewed single-lifecycle NumPy result into profile-bound density, deterministic open-loop admission, and effective-policy evidence without changing the single-use boundary.

### Host-owned capabilities

- keep capability grants and credentials outside guest requests;
- extend deterministic adapters before introducing real providers;
- preserve host receipts and explicit effect classification;
- require separate approval policy for irreversible operations.

## Completed foundations

- CPython 3.14 `wasm32-wasip1` guest with a neutral ABI;
- wazero fresh and single-use prepared execution;
- Linux fixed-memory COW-ready slots;
- strict request, response, capability, and evidence schemas;
- source-locked guest builds with manifest, checksum, SBOM, and notices;
- deterministic agent-workflow and BFCL-derived local evaluation fixtures;
- scheduler experiments for refill, concurrency, memory pressure, and burst load;
- configurable pre-COW warmup with a NumPy-ready profile.
- optional metadata-only Agent trace plugin with Host-owned Runtime correlation;
- private SQLite playback integrity, read-only operator queries/statistics/JSONL export, and checkpoint fork-lineage handoff.

## Deferred

- external framework integration and comparative test drive;
- public release and package publication;
- arbitrary PyPI or SciPy support;
- served-instance reuse;
- durable Python sessions and migration;
- exact double-build artifact equality as a release gate;
- production provider credentials or private-network access;
- production deployment claims.

Deferred items require a separate design and evidence review; they are not implied by the current implementation.
