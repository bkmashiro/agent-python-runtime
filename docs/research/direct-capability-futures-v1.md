# Direct Capability Futures v1

## Decision

Retain the direct Future lane as Experimental and default-off. Reject the earlier
analyzer-driven source-rewrite route.

The earlier negative result did not measure intrinsic Future overhead. It placed a fresh
exact CPython/WASI analyzer before every formal Guest run:

```text
old treatment = cold analyzer Guest + formal Guest + overlapped calls
```

That route paid about `5.081 s` of added work to hide at most `150 ms` from two `150 ms`
calls. It is not the paper-style Future design.

The retained route is:

```text
sealed Plan
  -> precomputed Future Python projection
  -> one formal fresh Guest
  -> tool call returns a Future immediately
  -> Host starts the physical call
  -> Python use or final result encoding materializes it
```

It runs **zero analyzer Guests**. If prefix analysis is also selected for an unrelated
streaming experiment, that experiment keeps its one existing private COW analyzer; the
Future lane does not create another.

```go
config.Mechanisms.SplitPhaseCalls = true
payload, err := runner.Run(ctx, request, plan.FuturePythonPrelude())
```

## Semantics

`Plan.FuturePythonPrelude()` projects every live, non-approval Python capability as a
Future. This is deliberately Future semantics rather than a claim of transparent ordinary
Python equivalence:

- the physical operation starts when Python dynamically reaches the call;
- independent calls can run concurrently;
- proxy use materializes the value;
- final response encoding recursively materializes Futures reachable from `result`;
- remaining Futures are drained so ignored writes and their errors are not lost;
- handler errors may therefore appear later than the original call expression;
- proxy type/identity observation is not promised to match the eventual value;
- playback and approval-gated calls remain on their existing non-Future paths.

There is no source AST pass, full-source analyzer, prefix analyzer, COW analyzer or derived
patch in this lane. Fresh Guest execution is unchanged: one formal Guest per Run.

## Exact-Guest result

The matched fixture contains two independent `150 ms` calls. Five exact-Guest trials at
Host commit `35cd17b315c45a6002554efd7621011ff691ac61` produced:

| Treatment | Median complete-Run time |
|---|---:|
| synchronous baseline | `2.473559667 s` |
| direct capability Futures | `2.363813292 s` |
| saved | `109.746 ms` |

The Future lane was faster in all five trials. Median speedup was `4.44%`, and it captured
`73.16%` of the theoretical `150 ms` overlap window after complete Guest and Host overhead.
An exact-Guest ignored-write fixture also passed three repetitions: one physical execution,
one final logical claim and no discarded write.

Machine-readable evidence:
[`direct-capability-futures-e2e-v1.json`](../evidence/direct-capability-futures-e2e-v1.json).

## Claim boundary

This result supports the paper story:

> Direct Futures have low overhead when they are implemented at the existing capability
> boundary and do not cold-start a second interpreter for source analysis.

It does not establish transparent equivalence for arbitrary Python values, default-on
suitability, or positive economics for every tool latency and workload shape.
