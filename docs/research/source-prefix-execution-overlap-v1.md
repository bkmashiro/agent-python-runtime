# Source-Prefix Execution Overlap v1

Status: **authored mechanism evidence; not natural benchmark uplift**

## Question

Can the existing exact-Guest streaming path overlap an early, reached Host-mediated read with the remaining timed source production without changing the result, physical dispatch count, authority Plan, or workspace disposition?

This experiment deliberately does not construct a dynamic DAG, infer dependencies, preflight unreachable calls, or enable semantic pre-dispatch. Python executes complete suites sequentially in one Guest. The only treatment difference is when the same frozen chunks are released.

## Preregistration and identity

The measurement was run only after these files were committed:

- `docs/evidence/source-prefix-overlap-contract-v1.json`
- `docs/evidence/source-prefix-overlap-oracle-v1.json`
- `docs/evidence/source-prefix-overlap-lane-config-v1.json`

The contract SHA-256 is `sha256:dab34bfa2a6ea8dce909c375c0b963569cfc67f988fa1adae56de561b1b009ff`. It fixes three repetitions, a 1,500 ms fixture read, source offsets 0/700/1,400 ms, a three-chunk/64 KiB queue, the expected result digest, the independent oracle digest, and the lane-config digest. The CLI hashes and strictly decodes all three files before creating a Guest.

Both artifact and harness were bound to signed source commit `501daef99796c1af7cd7bab1e0ab712a199820b9`. The exact Guest artifact SHA-256 was `sha256:a443042fb080d22f8e352aca0d0c8a5c87a7801e8afcc603e174d75fbe11c69b`.

## Matched treatments

Both lanes use the same exact Guest artifact, `RunStream` path, source chunks, sealed one-call capability Plan, fixture handler, private workspace attempt, output oracle, timeout profile, and final `result = stream_final` wrapper. Each lane first completes `_stream_begin`; the mechanism clock starts afterwards so Guest startup/initialization is not represented as decoding overlap.

- `generate_then_execute`: wait until the frozen 1,400 ms generation-complete frontier, then release all three buffered chunks.
- `stream_while_generating`: release each chunk at its frozen 0/700/1,400 ms offset. The first suite reaches a 1,500 ms Host read while the independent bounded producer continues releasing the remaining chunks.

The capability is an authored `external_read` fixture. It has no `PreDispatchContract`, `ReadOnly`, or `Idempotent` optimization metadata. Dispatch therefore occurs only when unchanged Python actually reaches the call. The Plan contains no write capability.

## Result

| Metric | Generate then execute | Stream while generating |
|---|---:|---:|
| Median mechanism-window wall time | 2,942.505 ms | 1,521.205 ms |
| Median speedup | 1.000× | **1.934×** |
| Median wall-time reduction | — | **48.30%** |
| Logical calls per lane | 1 | 1 |
| Physical dispatches per lane | 1 | 1 |
| Guest starts per lane | 1 | 1 |
| Fallbacks | 0 | 0 |
| Workspace disposition | published | published |
| Independent result oracle | PASS | PASS |

Matched pair savings were 1,396.688 ms, 1,421.300 ms, and 1,426.913 ms.

In baseline rows, the read began at approximately 1,408–1,412 ms, after the generation-complete frontier at approximately 1,402 ms. In streaming rows, it began at approximately 3.5–3.7 ms while generation continued until approximately 1,402 ms. Every row produced the fixed result SHA-256 `sha256:086ec5b85986ac0824ab4a1332c136028cb45c7d349e1b072dea9475493518e6`, exactly one logical call, exactly one physical dispatch, no fallback, and a published private workspace.

The body-safe evidence is `docs/evidence/source-prefix-overlap-v1.json`, SHA-256 `sha256:2777ab40b1a0a8919638a13f1b4ae0c6018e3f9456591fb1a943e910a406d1bd`. Raw execution evidence remains in a private 0700/0600 evidence root.

## Claim boundary

This result supports one narrow causal claim:

> On the preregistered authored mechanism fixture, sequential exact-Guest source-prefix execution overlaps one reached 1,500 ms Host read with a 1,400 ms remaining source-production window, reducing the matched median mechanism-window wall time while preserving the fixed result, logical/physical call count, sealed runtime identities, and workspace disposition.

It does not show natural workload prevalence, provider/model uplift, dynamic DAG scheduling, parallel tool calls, automatic safety from HTTP verbs or read-only labels, production tail latency, or universal Pysolate speedup. Guest initialization is intentionally outside this mechanism window and must not be presented as end-to-end model-request latency.
