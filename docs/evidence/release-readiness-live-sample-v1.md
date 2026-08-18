# Live release-readiness stream opportunity

This experiment tests a workload in which complete read-only calls appear before a long, naturally streamed program tail. It uses no authored pause.

## Workload

`deepseek-v4-flash` was asked to emit a complete Python release-readiness and incident-snapshot program. The program reads:

```python
metrics = ops.query_metrics(service="checkout", window="6h")
logs = ops.query_logs(service="checkout", severity="error", window="6h")
deployment = ops.latest_deployment(repository="shop/checkout")
config = ops.read_deployment(cluster="prod-eu", namespace="checkout")
```

It then generates pure-Python normalization, bucketing, correlation, drift checks, evidence construction, scoring, release-gate decisions, and a final report. The prompt requested 80–140 nonblank lines. The provider produced longer programs: the six accepted responses contain 246–432 physical lines (median 366). This prompt-directed workload is intentionally substantial, but its stream has no inserted sleep, statement schedule, or finalization pause.

The four checked-in deterministic read latencies are:

```text
metrics              2.2 s
logs                 3.1 s
deployment metadata  1.0 s
live configuration   1.4 s
```

## Capture scope

The recorder retains, without content redaction:

- complete system and user messages;
- every raw SSE envelope and monotonic arrival timestamp;
- provider response ID and model;
- complete reasoning and answer deltas;
- final JSON and Python source;
- token usage and finish reason;
- DNS, connection, TLS, request-write, and first-response-byte timing;
- every physical source-line closure;
- every tool eligibility and projected completion timestamp;
- actual timer replay order, completion, and projection drift.

Credentials are never serialized. Full raw streams live in the private thesis evidence repository; the public sample retains the complete accepted content, response IDs, statement timing, network timing, and replay evidence.

## Attempt accounting

One initial 6,000-token preflight produced no answer content before raw-on-failure retention was added. Twelve later attempts are retained byte-for-byte:

- five thinking-enabled attempts at 12,000 completion tokens: four exhausted the budget in reasoning with no answer content; one produced incomplete JSON before `finish_reason=length`;
- seven non-thinking attempts: six produced syntax-valid programs accepted by the workload contract; one produced an unterminated Python expression and was excluded from the accepted sample.

Failures are not silently dropped. Their hashes, token usage, finish reasons, content/reasoning sizes, and validation outcomes are in [`release-readiness-attempt-manifest-v1.json`](release-readiness-attempt-manifest-v1.json).

## Three schedules

Each accepted real source stream is replayed with three schedules:

1. **post-source sequential** — source completion, then four reads serially;
2. **post-source parallel** — source completion, then four reads concurrently;
3. **prefix pre-dispatch** — each read starts when its complete one-line statement arrives.

The third lane represents the cross-stream opportunity. The replay uses real timers and rotates lane order, but it does not execute the full Pysolate semantic, staging, exact-claim, Guest, or workspace stack. Results therefore remain opportunity bounds rather than net Runtime speedups.

## Accepted sample

Across six accepted streams:

```text
median first response byte              0.285105104 s
median first answer content             0.928927250 s
median source completion               21.047111646 s
median source physical lines           366
median first-eligible remaining window 19.687344187 s
```

Projected median readiness:

```text
post-source sequential  28.747111646 s
post-source parallel    24.147111646 s
prefix pre-dispatch     21.047111646 s
```

The median opportunity is therefore:

```text
vs post-source sequential  7.700000000 s  (26.786%)
vs post-source parallel    3.100000000 s  (12.838%)
```

Five of six streams fully hide the 3.1-second parallel barrier. One stream places the four reads around lines 190–193; its remaining window hides 2.792964709 seconds. The observed range against the strong parallel baseline is:

```text
2.792964709 s → 3.100000000 s
```

Actual timer replay tracks the timestamp projection closely:

```text
median sequential replay drift  8.756 ms
median parallel replay drift    4.224 ms
median prefix replay drift      2.154 ms
```

## Interpretation

This scenario exposes the mechanism much more clearly than the four-line travel program:

- the source tail is long because the requested program is substantial, not because the benchmark inserts a pause;
- complete read-only arguments appear before most of the stream in five runs;
- even a native parallel tool runner still pays a 3.1-second post-source barrier;
- prefix pre-dispatch can move that barrier entirely into source streaming in five runs.

The result does **not** establish a 12.838% Pysolate Runtime speedup. It establishes a median 3.1-second cross-stream opportunity before full Runtime overhead. A full semantic/Guest replay is still required to convert the opportunity into a net end-to-end claim.

Machine-readable accepted evidence: [`release-readiness-live-sample-v1.json`](release-readiness-live-sample-v1.json).
