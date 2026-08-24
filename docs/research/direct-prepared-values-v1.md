# Direct Prepared Values v1

## Decision

Retain the direct prepared-value lane as Experimental and default-off. Reject the earlier
analyzer-driven fixed NumPy source pass as the performance story.

## What changed

The old treatment analyzed the whole Python source, reread and digested the fixture, built a
new ValueSlot runner inside its timer, and closed that runner before its timer stopped. The
baseline constructed and closed its runner outside the timer. It also rebuilt the producer for
every single consumer, so it did not measure reuse. Its `4.695 s -> 4.991 s` result remains a
valid record of that rejected harness, not a result for prepared immutable reuse.

The direct lane is explicit:

```text
Host prepares immutable object once
  -> one sealed ValueSlot template
  -> each formal fresh Guest receives table.Fresh()
  -> trusted prelude materializes one scalar or private byte copy
  -> Agent program reads prepared_value
```

```go
config.Mechanisms.ValueSlots = true
prelude, err := valueslot.PythonPrelude("slot-numpy-sum-v1")
payload, err := runner.Run(ctx, request, prelude)
```

`ValueSlots` no longer depends on `SemanticAnalysis`. This lane creates zero analyzer Guests
and performs no source transform. The prior analyzer-driven source-pass entry point can still
select both mechanisms when that historical route is tested.

## Semantics

This is an explicit prepared-value API, not a claim that arbitrary source is transparently
rewritten:

- the baseline program reads the canonical `8,388,736`-byte `.npy` file, loads it with NumPy
  and computes `int(dataset.sum())`;
- the treatment program explicitly reads `prepared_value`;
- both return the same integer, `549755289600`;
- the Host template owns immutable physical backing;
- every Run receives a fresh claim table;
- canonical JSON scalar delivery copies `12` bytes;
- immutable bytes become a private `bytearray`, so one Guest's mutation cannot affect another;
- one formal fresh Guest is instantiated per Run.

There is no automatic cache, arbitrary NumPy proof, cross-partition sharing, source analyzer or
generic Python dataflow graph.

## Matched exact-Guest result

The timer starts immediately before `Runner.Run` and stops on return. Both runners are
constructed before measurement and closed afterward. The order alternates within each
five-pair cohort. Three cohorts produced 15 matched pairs:

| Metric | Baseline | Direct prepared value |
|---|---:|---:|
| Median complete Run | `5.017 s` | `2.355 s` |
| Median saved | | `2.662 s` |
| Relative improvement | | **53.06%** |
| Guest-side data path | `8,388,736`-byte workspace read | `12`-byte slot copy |
| Analyzer invocations | `0` | `0` |

All 15 treatment trials were faster. The exact fixed-fixture producer took a median `2.821 ms`
when its input bytes were already available to the Host. Charging that producer once to one
median treatment gives `2.358 s`, still `53.01%` below the baseline, but this is not labeled a
cold storage-to-result measurement because fixture acquisition is outside both timers.

The treatment reused one immutable template across five fresh Runs per cohort. Every Run
recorded one claim, zero discarded slots and `12` copied bytes. A separate exact-Guest test ran
three repetitions of two fresh private-byte consumers; mutation in the first did not reach the
second.

## Evidence

Machine-readable evidence is in
[`direct-prepared-values-e2e-v1.json`](../evidence/direct-prepared-values-e2e-v1.json).
The historical negative source-pass measurement remains in
[`host-scheduled-reuse-e2e-v1.json`](../evidence/host-scheduled-reuse-e2e-v1.json).
