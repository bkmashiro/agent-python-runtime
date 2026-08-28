# Unified split-phase execution evidence v1

Status: **Correctness passed; current cold exact-Guest economics are negative.**

The machine-readable record is [`unified-split-phase-v1.json`](../evidence/unified-split-phase-v1.json).

## What passed

The post-retirement CPython/WASI Guest was rebuilt from the current implementation and identified as:

```text
sha256:267d0810e0a9805bd58b100a92b63e72e7f05763a0bacce087b32c21e1d70202
```

The exact-Guest suite passed source-time preissue and runtime reuse, `A -> Python -> B(x)`, independent-call overlap, branch and loop activation, unsupported-source fallback, and failure/discard behavior. Logical Broker calls and receipts remained separate from physical starts. Local full and race suites also passed.

## Economics result

The bounded matched fixture uses two independent 150 ms immutable reads. The treatment includes one cold exact analyzer/lowering Guest before the final synchronous Guest. Across five runs:

```text
baseline median   3.755490702 s
unified median    9.441244409 s
delta             5.685753707 s
change            +151.40%
```

The unified path preserved physical overlap, but analysis and lowering cost dominated the available overlap. This fixture therefore does not support a latency-improvement claim. It supports the narrower conclusion that the unified lifecycle is correct and that the evaluated cold implementation did not improve end-to-end latency.

The result is not combined with source-generation Study A, prepared-value, COW or multi-program measurements. Retained-prefix local Python overlap is deliberately removed and is not remeasured here.
