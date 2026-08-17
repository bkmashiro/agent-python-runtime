# Source-Prefix Execution Overlap v1

Status: **authored mechanism evidence; not natural benchmark uplift**

## Question

Can exact-Guest, reach-gated streaming execution overlap one early Host-mediated read with the remaining timed Python source production without changing the result, dispatch count, authority Plan, or workspace state?

This experiment does **not** build or parse a dynamic DAG. It executes complete source prefixes in normal Python order.

## Frozen design

The measurement uses the preregistered files:

- `docs/evidence/source-prefix-overlap-contract-v1.json`
- `docs/evidence/source-prefix-overlap-oracle-v1.json`
- `docs/evidence/source-prefix-overlap-lane-config-v1.json`

The harness hard-codes their exact byte digests. The case contains three timed chunks at 0, 700, and 1400 ms. The first chunk reaches one 1500 ms Host-mediated read. Both lanes use the same exact source, Guest artifact, capability Plan, handler, private-workspace lifecycle, result oracle, and three-pair alternating order.

The only treatment difference is source release:

- `generate_then_execute`: hold all source until generation completes;
- `stream_while_generating`: release each chunk at its frozen offset.

The mechanism clock starts after `_stream_begin`, so Guest initialization is deliberately excluded from both lanes. This isolates source-production/tool overlap rather than claiming whole-request provider latency.

## Provenance remediation

An earlier local attempt used a self-consistent but caller-supplied harness/artifact identity and did not record portable workspace hashes. Independent review rejected it; it is **superseded and not used for this claim**.

The accepted measurement is explicitly labeled `provenance-remediation-v2` and closes those gaps:

- fixed preregistration byte digests in the CLI;
- fixed Guest artifact source commit and artifact SHA-256;
- harness source commit read from clean Go embedded VCS build information, not a command-line value;
- Host portable workspace SHA-256 recorded before and after every lane;
- validator requires the frozen 1500 ms tool duration, identical workspace identity, matched calls, and exact attempt identity.

## Result

Accepted public evidence: `docs/evidence/source-prefix-overlap-v1.json`

```text
matched pairs                 3
baseline median               2950.047 ms
streaming median              1533.923 ms
median speedup                1.923x
median wall-time reduction    48.00%
logical calls per lane        1
physical dispatches per lane  1
fallbacks                     0
workspace before == after     6/6
oracle                        PASS (6/6)
```

In baseline rows, tool dispatch began only after generation completed. In streaming rows, dispatch began at approximately 3.6–4.2 ms while source production continued until approximately 1401–1402 ms. This directly demonstrates overlap in the authored mechanism case.

## Supported claim

> In a preregistered authored workload with an early 1.5-second Host-mediated read and a 1.4-second source-generation tail, reach-gated source-prefix execution reduced median measured mechanism-window wall time from 2.950 seconds to 1.534 seconds (1.923×) across three matched pairs while preserving the canonical result, one logical/physical dispatch, the sealed Plan identities, and portable workspace state.

## Claim boundary

This evidence does **not** establish:

- natural benchmark or production-agent speedup;
- provider end-to-end latency;
- dynamic DAG extraction or dependency scheduling;
- parallel Python statements;
- safe speculative external writes;
- automatic pre-dispatch based only on `read_only` or `idempotent` metadata.

It establishes one bounded mechanism result: existing sequential source-prefix execution can overlap safe reached work with later source production without changing the measured result or workspace state.
