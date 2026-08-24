# Value-slot and data-local reduction contract v1

Status: **Exact-Guest verified; analyzer-driven data pass rejected, direct prepared-value successor retained Experimental/off**

> **Performance successor:** the explicit analyzer-free lane is specified in
> [Direct Prepared Values v1](direct-prepared-values-v1.md). This file retains the historical
> source-pass contract and its negative measurement.

This contract covers the first bounded immutable-value lane. It is intentionally
not a general object cache, Python IR, NumPy optimizer, or DataOp runtime.

## Value-slot contract

A `ValueSlot` is Host metadata for one derived-source occurrence. Its identity
binds:

- source occurrence;
- producer implementation identity;
- immutable input revision identity;
- value kind and maximum bytes;
- privacy partition;
- claim policy and maximum claims.

The first implementation supports only:

1. canonical JSON `bool` or signed-int64 values, materialized inline;
2. immutable byte strings up to 1 MiB, materialized as a detached private copy.

Python cannot name the materializer in ordinary source. A static source pass may
use the hidden helper only when its exact pass name and version are accepted by
the Guest. The direct successor instead gives explicitly authored consumer code a
trusted `prepared_value` binding before that code runs; it still cannot invoke the
Host materializer itself. The Host validates the slot shape before execution.
Unknown slots, duplicate claims, kind/strategy mismatches, over-limit payloads,
non-canonical scalars, stale input revisions, and privacy-partition mismatches
fail closed.

Every fresh consumer Run owns a fresh `Table`. Multiple tables may refer to one
`PreparedObject` only inside the same privacy partition. Claims always return a
new Go byte slice and the Guest reconstructs a new Python scalar or `bytes`
object. `Table.Close` releases the physical object's consumer reference;
`CanEvict` becomes true only after every consumer table has closed. No module,
global, iterator, handle, or mutable Python heap crosses a Run boundary.
The Runner stores an immutable table template and creates/closes a fresh claim table for
each Run. Runner construction failure, close-before-run and ordinary Runner close also
release the transferred template ownership.

## First data-local adapter

The only first-pass adapter matches this exact four-line source shape:

```python
import io
import numpy as np
dataset = np.load(io.BytesIO(open("/workspace/input.npy", "rb").read()), allow_pickle=False)
result = int(dataset.sum())
```

The adapter law is narrower than general NumPy semantics:

- the canonical checked-in NumPy int64 fixture is admitted only by its exact whole-file
  digest and fixed Host producer;
- `io.BytesIO`, binary `open`, `allow_pickle=False`, path, aliases, assignment names,
  and call shape are fixed;
- the reduction result is a canonical signed-int64 Python `int`;
- the slot input identity must equal the current workspace snapshot digest for
  `input.npy` at execution;
- any source variation, producer error, digest drift, or unsupported shape uses
  the original program before external effects begin.

Selection and execution are deliberately split:

1. select and validate the static patch;
2. only then invoke the selected-runner factory;
3. read the immutable workspace input and require the one frozen file digest;
4. let the named producer construct the fixed scalar and package-owned proof;
5. construct the Runner-owned immutable template;
6. clone a fresh Run-private value-slot table and execute the selected patch.

This ordering avoids preparing a value for an ineligible source. Source
validation for this pass verifies the original import declaration without
importing NumPy, then imports only modules retained by the derived program. The
all-off baseline continues through ordinary source validation and imports NumPy
normally.

Disabled/not-applicable selection, patch mismatch and producer failure construct only an
ordinary runner. A workspace digest drift discovered after selected construction executes
the unchanged source before Guest effects and reports the patch as not applied. Failures
after selected Guest execution begins are never replayed. Fallback retains the patch or
producer rejection in `PassError` separately from the ordinary Run result.

## Accounting

The workspace read, decode, reduction, object publication, copies, and table
lifetime are physical work. The original source occurrence remains the logical
observation represented by `result`; physical readiness never records a
capability effect. The implementation exposes value-slot evidence separately
from effect receipts.

## Historical source-pass cost gate

The original campaign intended to include workspace read, fixture validation, slot
construction, fresh Guest startup, materialization and response validation. It did not produce
a fair reuse result: the treatment rebuilt its producer for every consumer, kept source
analysis in the Run path, and timed selected-runner construction and close while the baseline
timed neither. Five repetitions recorded a `4,695,289,958 ns` baseline median and
`4,990,682,666 ns` treatment median, or **6.29% slower**. Those measurements remain frozen as
the cost of the rejected analyzer-driven harness; they are not evidence that prepared values
are intrinsically slower.

The direct successor uses one immutable template across fresh Runs, zero analyzer invocations
and matched `Runner.Run` timer boundaries. Across 15 exact-Guest pairs, it measured a
`5,017,197,083 ns` baseline median and `2,354,885,541 ns` treatment median, or
**53.06% faster**. The Host delivered `12` bytes per Run. Exact successor measurements are in
[`direct-prepared-values-e2e-v1.json`](../evidence/direct-prepared-values-e2e-v1.json); the
historical source-pass measurements remain in
[`host-scheduled-reuse-e2e-v1.json`](../evidence/host-scheduled-reuse-e2e-v1.json).

## Cross-Run reuse disposition

`PreparedObject` may back more than one fresh Run-private `ValueSlot` table. Each
consumer still receives detached Python bytes, keeps its own slot claims and
terminal Run state, and releases its own consumer reference. Producer identity,
input identity, privacy partition, backing identity, and consumer count must all
match before the physical object can be reused or evicted.

This is a semantic mechanism proof, not a production cache decision. The existing
NumPy reuse campaign already contains 240 records and 40 matched economic cells;
every cell was negative and no break-even was observed. Therefore this roadmap
does not add a durable cache, cache lookup policy, or automatic cross-tenant
reuse. The shared-object lane remains default-off and is rejected for production
promotion unless a materially different workload is separately preregistered.

## Composition disposition

No admitted workload in this roadmap simultaneously contains the fixed
`sources.read` overlap pattern and the fixed NumPy reduction pattern. Enabling
both mechanisms in configuration would therefore add bookkeeping without a
measured joint path. The runtime keeps Host scheduling and value materialization
as independent mechanisms and does not add a generic pass manager, ordering
graph, or combined policy layer. Composition is a no-go until a real workload
requires both and independently retained evidence predicts a benefit.
