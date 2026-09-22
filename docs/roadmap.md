# Pysolate: product clarity and measured runtime improvements

Approved goal: make existing execution capabilities easy to explain and use; finish the opt-in COW optimization, then improve at most two measured hotspots. Preserve isolation, recovery semantics and defaults. No engine fork, heavy dependencies, new platform, UI dashboard or speculative scheduler.

## Current pointer

**P1 — product story and core demo clarity.** P0 is complete. P2 implementation may proceed independently, but integration/claims remain gated on real validation.

## Outcomes

- [x] P0: COW data-image opt-in, admission checks, Linux isolation/replay/workspace validation, paired performance results and explicit memory tradeoff. See [cow-data-image.md](cow-data-image.md). Runtime API median gains: 6.14× short Python, 7.45× immediate Tool, 4.67× durable completion. Default unchanged; process memory still warrants investigation.
- [ ] P1: README leads with practical Python and clear safety, warm execution, early-read and deterministic replay benefits. Keep workspace and replay examples separate. Three to five core demo entries; claims link to evidence.
- [ ] P2a: explicit data-image flag across ordinary/workspace/durable service constructors and CLIs; invalid combinations reject. Linux HTTP acceptance and limited service A/B.
- [ ] P2b: read-only `/v1/durable/runs/{id}/history`, reusing pagination. Exclude payloads and operation keys, preserve not-found/error handling and service trust boundary.
- [ ] P3: profile optimized path, prioritize unexplained PSS retention then per-instance allocations. Keep at most two improvements with real end-to-end benefit; zero or one is acceptable. Revert neutral experiments.
- [ ] P4: align docs and final numbers, run affected real paths and relevant broad gate, signed commits/push with remote verification, clean worktree, stop.

## Execution constraints

One owner per mutable path. Each milestone needs runnable evidence, not a subagent completion claim. Distinguish setup from requests, API from HTTP, sampled PSS from sealed-file allocation, tool waiting from Python CPU time. Preserve intent-before-effect and outcome-before-return. No unsafe retries, cross-request Guest reuse or implicit optimization fallback.

Unexpected fixed-memory cost is not a reason to hide numbers or force GC on each request. Re-profile before choosing the next optimization. If a fork, public contract change beyond the agreed additive flags/history route, payment or major architecture choice becomes necessary, stop for a decision. Otherwise continue across milestones without requesting approval for routine choices.

The original accepted specification is the local `.hermes/plans/2026-09-23_003550-pysolate-next-megagoal.md`; this document is the sole live execution pointer. Do not create a parallel progress ledger.
