# Paper pass absorption v1

Status: **first compatible kernel implemented**

This audit rechecks the paper candidates against the implemented compile-time
`SourcePatchPlugin` seam. A candidate is admitted here only when it can transform the
complete final source in an authority-free exact Guest and execute through the unchanged
original `RunRequest`. A paper's scheduler, cache, planner or approximate operator is
not inherited with its optimization kernel.

## Decision

| Primary work | Candidate kernel | Current decision | Reason |
|---|---|---|---|
| [stratum](https://arxiv.org/abs/2603.03589) | exact constant folding | **Implemented narrowly as `pure_scalar_fold`** | Complete-source, total scalar rewrite; needs no Broker, workspace or multi-program state. |
| stratum | repeated pure computation | **Already represented narrowly by `pure_scalar_cse`** | Adjacent total bool/int64 expressions fit the same source-patch contract. |
| stratum | predicate/projection pushdown | Deferred | Requires an adapter-owned range/line capability and exact exception/encoding law. |
| stratum | vectorized or batched map | Deferred | Requires per-item logical outcomes and a physical batch contract. |
| [LLMCompiler](https://arxiv.org/abs/2312.04511) | dependency-safe parallel calls | Deferred | Requires source-order exception selection, cancellation and late/orphan accounting. |
| [APPL](https://arxiv.org/abs/2406.13161) | future/deferred call lifting | Deferred | APPL's asynchronous language semantics cannot be imposed on ordinary sequential Python without a typed concurrent-call owner. |
| [LLM-Tool Compiler](https://arxiv.org/abs/2405.17438) | fused tool calls | Deferred | One physical batch cannot yet preserve the original per-occurrence budget, receipt and failure order. |
| [AWO / Meta-tools](https://arxiv.org/abs/2601.22037) | static straight-line composite tool | Deferred | The useful subset still needs a composite Host dispatch with separate logical occurrences; history-derived policy replacement is not an equivalent source pass. |
| [AAFLOW](https://arxiv.org/abs/2605.02162) | map-to-batch lowering | Deferred | The AST rewrite is simple, but the batch operator's order, item errors, privacy and receipts are not represented by the pure patch seam. |

Only stratum contributes kernels that fit the current stage without a new execution
subsystem. The implementation claim is therefore about two narrow exact rewrites, not
about reproducing stratum or supporting all paper optimizations.

## Implemented slice: `pure_scalar_fold`

The pass accepts only a closed top-level scalar program. Every statement must be a
single-name assignment whose value is a known `bool`/signed-int64 expression, followed
by `result` as a scalar or bounded literal/list/tuple/dict/compare assembly. It replaces
a total `+`, `-` or `*` expression with the same literal while preserving source byte
length and line layout. For example:

```python
seed = 7
folded = seed * seed + 3
```

is rewritten to a padded equivalent of `folded = 52`.

Any import, call, attribute, control flow, unsupported assignment, division, heap value,
int64 overflow or compiled-code introspection makes the whole pass `not_applicable`.
Final execution uses the existing source/AST-bound patch contract and a fresh Guest.

## Why the other kernels stay deferred

The generic source-patch selector removed one historical blocker: a complete-source
rewrite can now be selected before formal execution. It did not create the downstream
semantics required by effectful passes. Projection still needs a versioned capability
law; batching needs per-item logical outcomes; parallelization needs source-order
failure and orphan ownership; meta-tool fusion needs a composite dispatch; multi-pipeline
stratum sharing needs multi-program identity.

Adding those mechanisms merely to make a paper row appear implemented would widen the
Runtime more than the pass itself. They should be reconsidered only with a concrete
workload and the smallest typed Host contract that preserves the original program's
logical effects.

## Claim boundary

The implemented result may support:

- a second independent whole-program plugin runs through the common seam;
- the exact scalar constant-fold subset preserves checked pass-off/pass-on behavior;
- stratum's constant-fold kernel can be represented without adopting its DataOp DSL.

It does not support claims about cross-pipeline sharing, predicate pushdown,
vectorization, batching, parallel tool calls, natural-workload prevalence or general
speedup. The matched exact-Guest fixture was negative in two repeated runs; the retained
run measured a `4.49%` slowdown. Raw samples and artifact binding are in
[`pure-scalar-fold-paper-pass-v1.json`](../evidence/pure-scalar-fold-paper-pass-v1.json).
