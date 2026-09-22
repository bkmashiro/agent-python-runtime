# Pysolate: product clarity and measured runtime improvements

Approved goal: make existing execution capabilities easy to explain and use; finish the opt-in COW optimization, then improve at most two measured hotspots. Preserve isolation, recovery semantics and defaults. No engine fork, heavy dependencies, new platform, UI dashboard or speculative scheduler.

## Current pointer

**P1 — product story and core demo clarity.** P0 is complete. P2 implementation may proceed independently, but integration/claims remain gated on real validation.

## Outcomes

- [x] P0: COW data-image opt-in, admission checks, Linux isolation/replay/workspace validation, paired performance results and explicit memory tradeoff. See [cow-data-image.md](cow-data-image.md). Runtime API median gains: 6.14× short Python, 7.45× immediate Tool, 4.67× durable completion. Default unchanged; process memory still warrants investigation.
- [x] P1: README now leads with practical Python and four clear capability groups plus measured evidence. Five existing core demos include safe early reads; workspace and replay remain separate. Real `run-core.sh` passed.
- [ ] P2a: explicit data-image flag across ordinary/workspace/durable service constructors and CLIs; invalid combinations reject. Linux HTTP acceptance and limited service A/B.
- [ ] P2b: read-only `/v1/durable/runs/{id}/history`, reusing pagination. Exclude payloads and operation keys, preserve not-found/error handling and service trust boundary.
- [x] P3: profiled optimized Python/Tool/durable execution and diagnosed elevated PSS as reclaimable Go heap, not an observed mapping leak. No additional runtime change justified: per-request GC rejected; instance-bound engine-object sharing deferred. See the follow-up in `cow-data-image.md`.
- [ ] P4: align docs and final numbers, run affected real paths and relevant broad gate, signed commits/push with remote verification, clean worktree, stop.

## Execution constraints

One owner per mutable path. Each milestone needs runnable evidence, not a subagent completion claim. Distinguish setup from requests, API from HTTP, sampled PSS from sealed-file allocation, tool waiting from Python CPU time. Preserve intent-before-effect and outcome-before-return. No unsafe retries, cross-request Guest reuse or implicit optimization fallback.

Unexpected fixed-memory cost is not a reason to hide numbers or force GC on each request. Re-profile before choosing the next optimization. If a fork, public contract change beyond the agreed additive flags/history route, payment or major architecture choice becomes necessary, stop for a decision. Otherwise continue across milestones without requesting approval for routine choices.

The original accepted specification is the local `.hermes/plans/2026-09-23_003550-pysolate-next-megagoal.md`; this document is the sole live execution pointer. Do not create a parallel progress ledger.
