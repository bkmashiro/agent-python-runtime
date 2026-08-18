# Observed DeepSeek stream opportunity

This companion measurement checks whether the authored seven-second source pause used by the deterministic mechanism campaign resembles live provider streams. It does **not** replace the campaign and does not claim an observed end-to-end Pysolate speedup.

## Method

On 2026-08-18, three pairs of the exact Brighton and Oxford candidate prompts used by the day-trip Harness were sent to `deepseek-v4-flash` with streaming enabled. Each pair used one reusable HTTP client and recorded monotonic response-header, SSE-envelope, reasoning, content, and source-statement-closure times. Six observations were accepted. One additional attempt failed projection before raw-on-failure persistence was installed and is counted as excluded.

Each pair is replayed as if both candidates started together. The replay uses the same observed source timestamps and checked-in deterministic tool latencies (`weather=300 ms`, `rail=600 ms`, `attractions=400 ms`) to compare:

1. **post-source sequential** — issue reads serially after source completion;
2. **post-source parallel** — issue all reads concurrently after source completion;
3. **prefix pre-dispatch** — issue each read when its complete, arguments-bound statement arrives.

These are counterfactual timestamp projections, not runtime wall-clock executions. They exclude semantic analysis, staging, exact claim, Guest, and workspace overhead. The result is therefore an `opportunity`, not a speedup.

## Observation

Across the six accepted candidate streams:

| Phase | Median |
|---|---:|
| First response byte | 0.338 s |
| First reasoning delta | 0.629 s |
| First answer-content delta | 7.706 s |
| Complete Python source | 8.465 s |
| First eligible call → source complete | 0.362 s |

The first cold observation completed DNS + TCP + TLS by 0.055 s. Reused requests avoided that setup. Most client-observed time before answer content was therefore neither local connection setup nor Python-source transfer: the provider streamed reasoning deltas before emitting the JSON answer. Once answer content began, the short sources completed quickly.

Across the three simultaneous timestamp replays, the opportunity relative to the strong post-source parallel baseline was:

```text
minimum  0.271799208 s
median   0.281865792 s
maximum  0.356362750 s
```

The median opportunity relative to post-source sequential calls was `0.981865792 s`. The smaller `0.281865792 s` median versus parallel calls isolates the distinctive cross-stream overlap from ordinary tool-call concurrency.

## Claim boundary

> These live streams exposed positive but short prefix windows. Against a strong post-source parallel-tool schedule, the median timing opportunity was about 0.282 s before Pysolate mechanism overhead.

The sample does not establish positive net runtime speedup or a population estimate across provider load, models, and program shapes. A runtime replay is still required to measure how much of the 0.282 s survives semantic, staging, claim, and Guest overhead.

Machine-readable evidence: [`observed-deepseek-stream-opportunity-v1.json`](observed-deepseek-stream-opportunity-v1.json).
