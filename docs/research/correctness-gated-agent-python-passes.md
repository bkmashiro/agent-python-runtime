# Correctness-gated source-bound Agent Python passes

Status: **design contract for the next optimizer lane**

The body-free v1 study is frozen by
[`source-bound-pass-preregistration-v1.json`](../evidence/source-bound-pass-preregistration-v1.json).
It binds the four stage identities, four outcome classes, pipeline bounds, the
body-safe comparison columns, forbidden claim classes and 15
positive/adversarial controls without storing source, result or workspace bodies.

This note defines what Pysolate may call an optimization pass and how that pass
inherits the runtime's existing source, effect, authority, workspace and terminal
boundaries. The optimizer has both streaming-prefix overlays and complete-source
execution patches. It does not claim that the candidate passes below are implemented.

## Current safety boundary

Pysolate already separates three events that other agent runtimes often conflate:

1. physical preparation performed by the Host;
2. the logical effect reached by the original Python program;
3. publication of a result or workspace state.

This separation is why source-generation overlap does not mean executing an invalid
partial Python program.

### Complete source before formal Python execution

The formal execution path validates the complete source before Agent Python starts.
A malformed final suffix is rejected as invalid source. It does not execute an earlier
valid suite and cannot publish a result or workspace branch.

The current semantic pre-dispatch path is narrower. Before the final source is known,
the Host may issue one physical operation only when the exact capability contract
classifies it as a bounded speculative-safe read and the verified source prefix,
arguments, Plan, freshness, privacy and budget bindings all match. This operation:

- is not execution of the Agent Python prefix;
- carries no new Guest authority;
- is not yet a logical capability event;
- cannot publish Guest or workspace state;
- must be claimed once by the exact dynamic occurrence in the validated final program;
- is cancelled, late or orphaned and accounted if the final source is invalid,
  abandoned or does not reach that occurrence.

Writes and unknown effects do not cross this early boundary. They remain behind final
source validation, dynamic reach, Broker admission and any required approval.

### Derived source before transformed execution

The prepared pure-region path first validates the original source, then validates the
source-bound decision, capsule and execution patch. The derived AST is compiled and
bound to the final source, original and derived AST identities, analyzer, profile,
import closure, capability Plan, pass configuration and selected region before the
formal Guest executes it once.

A failed analysis, unsupported AST shape, binding mismatch, invalid derived AST or
unavailable materialization does not partially execute the transformed program. It
rejects the pass or fails before formal execution according to the typed boundary.

### Rollback is deliberately narrow

Pysolate does not promise generic rollback of the external world:

- pure Guest work can be destroyed;
- prepared/private memory can be destroyed;
- a private workspace branch can be discarded;
- speculative-safe read work may be orphaned and accounted without becoming a logical
  effect;
- an external write may be aborted only if its capability adapter provides a real
  transaction or compensation contract;
- after an authority-bearing external effect may have started, the Host must not replay
  the original program as if nothing happened. Ambiguity and reconciliation remain
  explicit terminal facts.

The optimizer therefore prevents unsafe effects from starting early rather than
pretending it can roll them back later.

## What counts as a pass

A Pysolate optimization pass must transform one or more bounded target-CPython ASTs,
or produce a source-bound overlay that changes how exact AST occurrences are prepared
or lowered. Complete-source execution patches are one pass kind, not the definition of
the whole optimizer. A pass is not a scheduler, cache-retention policy, model selector,
learned planner or backend placement rule.

Every registered pass needs:

```text
name + version
analyzer identity
pass configuration identity
original source + AST identity
required program/effect bindings
required Plan/profile/import/workspace/freshness bindings
consumer kind: overlay or execution patch
bounded output AST/overlay identity
```

The current registry has two concrete consumers:

| Pass | Consumer | Current role |
|---|---|---|
| `semantic_pre_dispatch` | `overlay_only` | Uses a verified complete-prefix AST to prepare one qualified Host observation; the unchanged final source may claim it only at the exact occurrence. |
| `prepared_pure_region` | `execution_patch` | Replaces one admitted scalar region with a one-shot materialization helper. |

The registry is intentionally not yet a general pass manager. A third real
transformation should validate the shared shape before the project generalizes it.

## Stage model

Streaming does not require Pysolate to choose between “compiler pass” and “runtime
optimization.” It introduces a second compiler stage:

```text
append-only source stream
  -> complete-prefix AST snapshots
  -> prefix overlay / safe physical preparation
  -> final source seal and complete AST
  -> execution-patch passes
  -> exact target-Guest compile
  -> one formal execution that claims admitted overlays
```

An arbitrary chunk or syntactically incomplete suite is not an AST snapshot and cannot
admit a pass. The prefix analyzer sees only source that the exact target parser accepts
as the visible prefix. A prefix overlay may start qualified physical work, but it does
not execute the prefix as the Agent program or commit a logical effect. The complete AST
remains mandatory before formal execution and before any derived execution patch.

This yields four pass forms:

| Pass form | Input | Output | Example |
|---|---|---|---|
| prefix overlay | one verified complete-prefix AST plus Host contracts | occurrence-bound preparation decision | `semantic_pre_dispatch` |
| hybrid preparation pass | prefix AST prepares pure/private work; final AST revalidates it | final execution patch or discard | future streaming pure-region/array preparation |
| whole-program execution patch | validated complete AST | bounded derived AST | `prepared_pure_region` |
| multi-program execution patch | several validated complete ASTs | shared pure prefix plus private residual ASTs | future `cohort_common_prefix` |

The current prefix readiness filter, bounded analyzer session and prepared/COW analyzer
capacity reduce compiler-analysis overhead. They do not change or overlay Agent AST
occurrences, so they are compiler-service optimizations rather than passes in the paper
count.

## Correctness obligations

### Full-source and AST validity

- The original complete source must parse and validate before transformed formal
  execution.
- The transformed AST must stay within explicit node, depth and byte bounds.
- The exact target Guest must compile the derived AST before it can execute.
- Unsupported or unclassifiable Python rejects the pass; absence of a candidate never
  means safety.

### Value, control and exception behavior

A pass must preserve the declared observable subset:

- result or exception class and bounded payload;
- short-circuit and zero-iteration behavior;
- source-order exception visibility;
- Python aliasing and mutable-object identity where observable;
- stdout and workspace disposition where included in the pass contract.

Pysolate cannot prove arbitrary Python equivalence. Each pass must define a narrow
admitted subset for which these obligations are machine-checkable and reject everything
else.

### Logical effects

The transformed program must preserve:

- logical occurrence identity;
- capability, arguments and logical resource identity;
- predecessor/order constraints;
- logical budget, receipt and approval behavior;
- freshness, privacy and workspace root;
- cancellation, ambiguity and reconciliation semantics.

A pass may reduce physical work while retaining the original logical events. Physical
and logical counts must remain separate in evidence.

### Authority

AST syntax and analyzer output identify opportunities but do not create authority.
Every derived capability call must be admitted by the original sealed Plan. The pass
must not widen a resource, freshness domain, privacy partition, workspace root or
invocation identity.

### Failure and fallback

The safe fallback depends on what has physically started:

- pure/private preparation may be discarded;
- qualified speculative-safe read work may be cancelled or orphaned and explicitly
  accounted before ordinary execution continues;
- an execution patch that has not begun formal execution may be rejected in favor of
  unchanged source;
- no fallback may replay an authority-bearing external effect that may already have
  started;
- unknown start state is ambiguity, not permission to retry.

## Pass classes worth implementing

### Paper-derived optimization kernels

| Optimization kernel | Candidate pass | Initial admissible subset |
|---|---|---|
| repeated computation/read sharing | `effectful_cse` | pure values or exact frozen-root reads with detached materialization |
| predicate/projection pushdown | `capability_projection_pushdown` | adapter-declared exact rewrite laws, beginning with bounded repository reads |
| map/tool fusion | `capability_batch_fusion` | ordered bounded batches of pure or frozen-root reads with per-item outcomes |
| independent-call parallelism | `independent_capability_parallel` | dependency-free pure work or frozen-root reads; no external writes |
| straight-line meta-tool fusion | `straight_line_meta_tool_fusion` | static deterministic sequences only; no learned reasoning removal |

These kernels appear separately in stratum, LLMCompiler, APPL, LLM-Tool Compiler,
AWO/Meta-tools and AAFLOW. Pysolate should claim only the kernel represented by the
pass, not the complete paper system.

### Pysolate-native passes

| Pass | Purpose |
|---|---|
| `canonical_input_specialization` | Substitute exact immutable inputs, fold branches and expose unreachable code. |
| `unreachable_import_elimination` | Remove only imports in branches proven unreachable after specialization, then recompute import closure. |
| `repository_projection_pushdown` | Lower exact read/split/slice/search patterns to bounded Host capabilities. |
| `literal_array_hoisting` | Replace admitted immutable literal/array construction with bounded prepared materialization. |
| `pure_function_memoization` | Lower admitted direct pure calls to content-addressed one-shot materialization. |
| `loop_invariant_observation` | Preserve zero-iteration and first-exception timing while avoiding repeated frozen-root reads. |
| `streaming_pure_region_prepare` | Prepare an authority-free pure region after its prefix closes, then admit or discard it against the complete final AST. |
| `streaming_literal_array_prepare` | Begin bounded literal/array preparation from a closed prefix and consume it only through a final-source-bound patch. |
| `sibling_observation_cse` | Rewrite several sibling ASTs to consume one immutable physical observation under separate logical identities. |
| `cohort_common_prefix` | Factor a shared pure prefix from several ASTs and lower residual programs to private Prepared Family consumers. |

The final two are multi-AST passes. Prepared Family, private COW, single-flight and
workspace branches are lowering substrates, not passes by themselves.

## Passes that remain out of scope

The following mechanisms may improve an agent system but are not AST passes for this
lane:

- LLM KV/prefix cache retention and eviction;
- GPU/CPU batching and request scheduling;
- model or reasoning-budget selection;
- learned workflow or skill induction;
- planner-generated DAGs that replace the source program;
- generic retry, merge, compensation or effect replay;
- backend placement without an AST transformation;
- approximate or lower-fidelity operator substitution.

A paper that combines one eligible transformation with these mechanisms is related only
through the eligible kernel.

## Initial implementation order

The minimum coherent sequence is:

1. preserve the two existing pass registrations and freeze this contract;
2. implement `effectful_cse` for pure scalar expressions and exact frozen-root reads;
3. implement repository-specific projection pushdown with one versioned exact rewrite
   law;
4. implement ordered read-only batch fusion;
5. add independent read parallelization only after CSE/fusion exception and orphan
   semantics are executable;
6. add prepared literal/array hoisting and then a bounded cohort common-prefix spike;
7. generalize the pass manager only after at least three independent transformations
   exercise the same interface.

Each pass needs pass-off/pass-on differential fixtures, adversarial invalid-source and
earlier-exception cases, exact logical-effect equivalence, physical-work accounting,
negative authority/freshness/workspace cases and an independent post-fix review before
promotion.

## Research claim

The useful claim is not that every agent-system optimization is a compiler pass. It is:

> Several point systems optimize repeated observations, independent calls, batched
> operations and reusable computation. Pysolate expresses the semantics-preserving
> kernels as guarded source-bound passes over ordinary Agent Python, spanning prefix
> overlays and complete-source transformations under one effect, authority, freshness,
> workspace, failure and fallback contract.

The distinguishing problem is not discovering parallelism or reuse. It is deciding when
that physical optimization is allowed to occur without changing the original program's
logical effects.

## Related work entry points

- [LLMCompiler](https://arxiv.org/abs/2312.04511)
- [LLM-Tool Compiler](https://arxiv.org/abs/2405.17438)
- [APPL](https://arxiv.org/abs/2406.13161)
- [Optimizing Agentic Workflows using Meta-tools](https://arxiv.org/abs/2601.22037)
- [stratum](https://arxiv.org/abs/2603.03589)
- [AAFLOW](https://arxiv.org/abs/2605.02162)

The separate `pysolate-explained` repository keeps short per-paper analyses and makes
explicit which part is an eligible pass, a lowering substrate, behavior-changing work or
unrelated serving policy.
