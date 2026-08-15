# Observable workflow-boundary post-fix review

Date: 2026-08-15
Status: **Closed — no known unresolved Medium+ finding**

> Historical review: the paired evidence and body-free Lab surface reviewed here were later
> deliberately reset. Current Lab development data uses the private Agent trajectory
> contract documented in `docs/research/agent-trajectory-v0.md`.

## Independent review rounds

The bounded read-only review rounds did not produce a blanket pass before their time limit, but they did produce concrete adversarial findings. Each finding was reproduced, fixed and retained as a regression gate:

| Area | Finding | Closure |
|---|---|---|
| Track A cache | Layer crash staging used `.publish.XXXXXXXX`, while maintenance initially recognized only keyed temporary names. | `e3760fd` recognizes the producer's exact random form and bounded keyed final-cache residue; focused maintenance tests pass. |
| Track B provenance | A physical execution could list a non-producer consumer without one admitted reuse/coalescing decision. | `cae1660` requires exactly one sharing decision for every non-producer consumer. |
| Track B parallel scope | A declared-parallel decision could span different workflow IDs. | `d274b83` requires one Run and workflow identity plus distinct truly-overlapping physical intervals. |
| Track F equivalence | The first paired driver compared a predeclared digest rather than deriving the complete observable result. | `955b780` derives ordered measured tool results plus actual WASM result and fixed model fixture; `1eebbb9` strips invocation metadata and compares canonical Guest result JSON; divergent WASM output fails immediately. |
| Track G Lab verification | Recomputed outer seals could hide nested report/manifest mutations, unknown fields, cross-treatment links and orphan sharing. | `2bac356` verifies closed shapes, manifest/report/outer seals and provenance; later regression probes also cover attacker-controlled resealing. |
| Existing debugger regression | The new default Lab surface broke prior Playwright assumptions. | Existing debugger tests now select its explicit tab; all desktop/narrow tests pass. |

Two broad final review agents timed out after 900 seconds rather than returning a summary. Their completed probes are still preserved in their transcripts; the cross-workflow and Lab mutations above came from those probes. This document does not mislabel those timeouts as independent PASS results. Closure is based on the independent findings plus the post-fix executable regressions and full repository gates below.

## Post-fix gates

- `go test -race ./... -count=1`: pass.
- `go vet ./...`: pass.
- Guest Python suite: 95/95 pass.
- Workstation verifier suite: 7/7 pass.
- Lab unit tests: 14/14 pass.
- Lab production build: pass.
- Playwright: 10/10 desktop/narrow pass.
- Real Guest semantic gate against source `955b780`: pass.
- Sealed paired experiment: 14 tasks, zero divergence, 25 baseline versus 23 optimized physical reads.
- Final exact-source cache check: source `955b780`, layer hit/final miss followed by layer hit/final hit, identical artifact SHA-256, 3.44× elapsed ratio.

## Review boundary

No consumer was admitted for executable AST-region reuse or semantic placement. The paired Harness and Lab remain read-only research surfaces with no Runtime scheduling authority. CPU and peak RSS were not recorded by evidence v0, so no resource-efficiency claim is made beyond artifact/evidence/cache storage facts.
