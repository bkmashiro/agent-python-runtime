# Value-slot and data-local reduction contract v1

Status: **Exact-Guest verified; semantic slot retained Experimental/off, data pass rejected on cost**

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
the Guest. The Host validates the pass-specific slot shape before execution.
Unknown slots, duplicate claims, kind/strategy mismatches, over-limit payloads,
non-canonical scalars, stale input revisions, and privacy-partition mismatches
fail closed.

Every fresh consumer Run owns a fresh `Table`. Multiple tables may refer to one
`PreparedObject` only inside the same privacy partition. Claims always return a
new Go byte slice and the Guest reconstructs a new Python scalar or `bytes`
object. `Table.Close` releases the physical object's consumer reference;
`CanEvict` becomes true only after every consumer table has closed. No module,
global, iterator, handle, or mutable Python heap crosses a Run boundary.

## First data-local adapter

The only first-pass adapter matches this exact four-line source shape:

```python
import io
import numpy as np
dataset = np.load(io.BytesIO(open("/workspace/input.npy", "rb").read()), allow_pickle=False)
result = int(dataset.sum())
```

The adapter law is narrower than general NumPy semantics:

- the canonical checked-in NumPy int64 fixture format is decoded by the Host;
- `io.BytesIO`, binary `open`, `allow_pickle=False`, path, aliases, assignment names,
  and call shape are fixed;
- the reduction result is a canonical signed-int64 Python `int`;
- the slot input identity must equal the current workspace snapshot digest for
  `input.npy` at execution;
- any source variation, producer error, digest drift, or unsupported shape uses
  the original program before external effects begin.

Selection and execution are deliberately split:

1. select and validate the static patch;
2. if selected, read/decode/reduce the immutable workspace input on the Host;
3. create the Run-private value-slot table;
4. construct a value-slot-enabled fresh Guest;
5. execute the already selected patch.

This ordering avoids preparing a value for an ineligible source. Source
validation for this pass verifies the original import declaration without
importing NumPy, then imports only modules retained by the derived program. The
all-off baseline continues through ordinary source validation and imports NumPy
normally.

## Accounting

The workspace read, decode, reduction, object publication, copies, and table
lifetime are physical work. The original source occurrence remains the logical
observation represented by `result`; physical readiness never records a
capability effect. The implementation exposes value-slot evidence separately
from effect receipts.

## Cost gate

The matched campaign includes, per treatment repetition:

- workspace read;
- immutable fixture validation and reduction;
- value-slot construction;
- fresh Guest startup;
- hidden materialization;
- full response validation.

The baseline performs the same fresh-Guest run with ordinary NumPy import,
`np.load`, and `sum`. Five fresh repetitions are required. The adapter remains
research-only unless the treatment median is faster and all results match. A
positive result applies only to this exact immutable int64 reduction; it is not
evidence for arbitrary NumPy, table pushdown, or a generic shared-object cache.

The exact `numpy-core` Guest built from `1bcec51f1c1770c09680fdcf761270ae8296b9ee`
passed private-byte materialization, fresh-consumer isolation, shared physical backing and
the fixed NumPy sum equivalence controls. Five matched repetitions read an `8,388,736`-byte
fixture and copied only `12` result bytes to each Guest, but measured a `4,709,831,166 ns`
baseline median and `5,072,518,333 ns` treatment median. Complete treatment cost was
**7.70% slower**; Guest peak memory was unavailable and is not inferred.

The fixed data-local pass is therefore a truthful no-go and stays default-off. The small
ValueSlot seam remains Experimental because it independently supports bounded scalar/byte
materialization, not because the NumPy pass won. Exact measurements are frozen in
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
