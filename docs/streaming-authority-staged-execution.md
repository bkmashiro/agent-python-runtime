# Streaming authority-staged Agent execution

Status: **Historical mechanism evidence; literal eager preflight disabled by the 2026-08-14 successor contract.**
Date: 2026-08-13

> **2026-08-27 successor decision:** retained-prefix Guest execution and literal eager
> preflight are no longer product paths. They require the explicit
> `LegacyResearchExecution` gate for historical replay. Current source streaming performs
> analysis only; admitted Host work enters the unified split-phase table, then one
> sealed-source lowering precedes one synchronous Guest execution. The remaining text is
> historical mechanism evidence, not current admission policy.

## Research question

> Can Pysolate safely overlap model generation with real Python execution and
> qualified external reads, while guaranteeing that incomplete, invalid, or
> abandoned generated programs cannot publish filesystem changes or dispatch
> writes?

This hypothesis follows directly from an Agent-specific timing boundary:
traditional functions exist before invocation, whereas an Agent's Python program
is still being produced while the Runtime is otherwise idle.

## Historical minimal mechanism

The implemented proof now provides:

- an append-only Host state machine whose semantic verdicts and complete-suite
  ranges come from an injected exact-Guest compiler oracle;
- a trusted bootstrap streaming session in target Guest CPython using one private
  namespace, `CommandCompiler` future flags, a frozen import preamble, and
  complete-suite execution;
- Host-owned private workspace attempts with distinct identities, discard on
  invalid/error/cancel paths, and publication only after a successful final
  Guest response;
- Host-qualified `read_only + idempotent + speculative_safe` metadata, an
  independent speculation-call budget, literal-call eager preflight,
  occurrence-bound one-time consumption, orphan accounting, and relative timing;
- reach-gated execution for non-qualified reads, a streaming-specific Python
  projection that omits write capabilities, and Broker-level pre-seal write
  denial even if Agent code reaches a private bridge helper;
- a real-WASI north-star fixture for eager unreachable reads, unreachable
  reach-gated reads, no duplicate dispatch, private filesystem publication, and
  invalid-suffix discard.

This remains a narrow proof. Recursive streamed subagents, general external-write
commit/reconciliation, prepared-runtime/COW optimization, and performance-benefit
claims remain Proposed.

## Core model

```text
Model streams generated Python/subtasks
        │
        ├─ exact incremental admission
        ├─ authority-free Prepared World
        ├─ execute closed suites under actual Python control flow
        ├─ eager preflight of closed speculative-safe reads
        ├─ reach-gated execution for all other allowed calls
        ├─ private filesystem overlay
        ├─ streamed subagent fan-out
        └─ no externally visible writes/publication
                │
           complete source/plan
                │
        full target-Guest validation
                │
      seal source + authority + workspace
          ┌─────┴─────┐
        invalid      valid
          │             │
  discard Guest,     bind reached reads;
  overlay, outputs   publish selected FS delta;
  and staged data    cross qualified write barrier
```

The optimization is speculative: invalid or abandoned source may waste CPU and
qualified read requests. It must never leave a filesystem mutation in the
published workspace or dispatch a real write.

## Source-stream contract

Initial syntax is deliberately narrower than arbitrary Python:

- source is append-only between explicit begin/end markers;
- encoding, module docstring, `__future__` directives, and static import preamble
  are frozen before executable suites begin;
- imports after the preamble, dynamic import, `eval`, and `exec` are rejected;
- the target Guest CPython parser/compiler is authoritative;
- execution occurs per complete top-level statement or compound suite, not per
  physical line;
- decorators and a `def`, `class`, `if`, `try`, `with`, or other compound form
  wait until the complete suite is closed;
- all suites execute in one private module namespace;
- final end-of-stream compilation and admission cover the complete source;
- every executed suite has an exact source range and digest that must map to the
  final accepted module.

Later source cannot revoke an earlier sequential computation, but a later syntax
or admission failure invalidates the complete program and discards all
unpublished speculative state.

## Filesystem behavior

Ordinary Python filesystem calls execute against:

```text
immutable/pinned input root
+ private speculative write overlay
+ per-Guest /tmp
```

Before final seal:

- reads observe the admitted input view and prior private writes;
- writes, renames, and deletes affect only the overlay;
- no derived root is published to the parent workspace;
- stdout/result and file bodies remain protected staging state.

If final validation fails, the Guest, `/tmp`, and overlay are discarded. If it
succeeds, Host policy may select and publish the derived root. Publication is a
separate Host decision, not an effect of reaching end-of-stream.

## Tool classification

A binary read/write label is insufficient for streaming speculation.

### Eager-preflight read

A capability adapter may opt in only when all of the following are true:

- dispatch performs no external mutation;
- the operation is idempotent under its canonical request identity;
- operator policy marks it `speculative_safe`, permitting dispatch even when the
  final Python control flow may never reach the call;
- speculative requests are allowed by operator policy;
- request cost, quota, rate-limit, privacy, and provider-visible access logs are
  accepted as real consequences even if the program is later invalid;
- canonical arguments and a bounded result representation exist;
- the result may be frozen as a historical observation;
- freshness/expiry semantics are explicit;
- cancellation, timeout, and late response ownership are defined.

Examples may include bounded immutable-object reads or read-only fixture/search
adapters after qualification. The three properties are Host-owned and distinct:

```text
read_only
+ idempotent
+ speculative_safe
```

The first two are insufficient when an abandoned request can create an
unaccepted consequence. `speculative_safe` means that quota, billing, rate-limit,
provider access logs, privacy exposure, and wasted cache misses are all within a
separate operator-approved speculation budget.

Once a complete call expression has stable canonical arguments, the Host may
dispatch it without proving that final Python control flow will execute it. If
the final program never reaches that occurrence, the result is an orphan and the
dispatch is counted as speculation waste.

### Reach-gated call

Every allowed call that is not explicitly `speculative_safe` must wait until the
speculative Guest reaches that exact dynamic occurrence through real Python
control flow. Reaching the call establishes necessity for the execution prefix;
fully evaluated arguments establish request identity. This class may overlap the
remaining model stream from the point of reach, but it cannot be launched merely
because a syntactically closed call appears in an unexecuted branch or function.

Reach does not waive normal adapter policy. In particular, an external write
remains commit-gated as described below.

### Commit-gated write

A write may be parsed and an immutable intent may be staged, but the real provider
call cannot dispatch before complete-source validation, exact authority seal,
and any required approval. Writes that control subsequent Python flow therefore
cannot participate in initial streaming execution; execution pauses at the
barrier or the program is not admitted to streaming mode.

A provider-specific dry-run or preview is a separate qualified read operation,
not proof that the eventual write succeeded.

### Unknown or unsafe call

Unknown effects, unqualified live reads, credential-sensitive calls, expensive
or scarce calls, and calls without stable argument/result identity stop
speculation. They execute only after normal complete-source admission, or remain
denied.

## Historical dispatch rules: eager preflight versus confirmed reach

For an eager-preflight read, a closed call with canonical immediate arguments is
sufficient even when the call may be unreachable:

```python
if False:
    catalog.read("x")

def unused():
    return catalog.read("x")

ready and catalog.read("x")
```

All three calls may be preflighted only if `catalog.read` is Host-qualified as
`read_only + idempotent + speculative_safe`; unused results become orphaned
speculation. Without that qualification, none dispatches until the Guest really
reaches the corresponding occurrence.

Literal arguments are the easiest and likely common initial eager subset:

```python
catalog.read("object-17", limit=20)
```

A conservative first eager contract may admit only JSON-like literal arguments.
Subsequent versions may admit values derived solely from sealed structured input
or prior frozen observations, with provenance in the call key. Reach-gated calls
may use normal evaluated arguments once canonicalized.

## Speculative-read identity

A speculative read is one real dispatch, not a request that formal execution
repeats. Its identity binds at least:

```text
workflow/stream epoch
final source identity once sealed
executed suite/source-range identity
stable dynamic occurrence index
capability spec + handler + grant/policy identity
canonical argument bytes
parent observation/control-flow lineage
freshness epoch and expiry
privacy partition
```

Eager-preflight lifecycle:

```text
closed qualified call with canonical arguments
→ journal speculative-read operation
→ dispatch once
→ record immutable observation or terminal failure
→ later complete-source validation
    ├─ invalid/abandoned: mark observation orphaned; never expose it to another
    │  invocation merely because arguments match
    └─ valid source
        ├─ occurrence never reached: mark observation orphaned/wasted
        └─ exact occurrence reached: bind operation to final source/RunPlan and
           return the staged result without another dispatch
```

Reach-gated lifecycle starts at the exact dynamic occurrence and otherwise uses
the same single-dispatch, source binding, and fallback-playback rules.

When the actual speculative Guest already passed the occurrence, it simply
continues with the returned result. A fallback full re-execution must use strict
occurrence-bound playback and must not dispatch again. Mismatched occurrence,
arguments, source mapping, plan, freshness, or policy fails closed.

Global argument-only caching is a different optional mechanism and is not implied
by speculative dispatch.

## Immediate arguments: value and limit

Immediate arguments are valuable because they provide an exact request key as
soon as the call expression closes, before control-flow necessity is known. They
likely cover many generated calls such as:

```python
git.show("HEAD", "README.md")
catalog.search("copy-on-write", limit=10)
fixtures.get("benchmark-v1")
```

Potential overlap is:

```text
model continues generating source
∥ qualified read is in flight
```

The critical-path saving is bounded by the overlap between remaining model
generation time and read latency. It is largest for early, slow reads followed by
substantial generated source.

Literalness does not establish safety, freshness, or necessity. Only a
Host-qualified `speculative_safe` read may ignore necessity. Even an abandoned
GET may incur money, quota, provider logs, access notifications, or privacy
exposure. Those costs require a separate speculative-read budget and evidence.

## Interaction with later control flow

A read result may immediately control Python execution:

```python
metadata = catalog.read("object-17")
if metadata["kind"] == "dataset":
    ...
```

This is supported naturally: the speculative Guest waits for the real read,
continues down the actual branch, and consumes later closed suites as they arrive.
The model may continue producing source concurrently; it does not receive the
result unless the Harness deliberately defines a separate model/tool interaction.

Past observations remain historical facts of the sealed execution. They are not
silently refreshed during fallback re-execution. An explicit refresh creates a
new observation and downstream lineage.

## Authority staging

"Authority-free" preparation and "qualified speculative read" are distinct
phases:

1. a shared Prepared World contains no tenant data, Broker, workspace, or grant;
2. a private speculative child receives a narrow read-only speculation plan,
   input root, `/tmp`, overlay, distinct eager-preflight/reach-gated budgets, and
   result sink;
3. no write/commit/approval authority exists in that child;
4. complete source admits and seals the final normal RunPlan;
5. only then may later qualified writes be considered.

The speculative read plan is real limited authority. It must be narrower than or
equal to the final admitted authority and independently auditable; it is not
ambient access inherited from the prepared image.

## Streamed subagent fan-out

A parent stream may emit a complete structured child descriptor before the full
parent plan ends. The Harness may then:

- create a private child from an authority-free prepared baseline;
- branch the parent's immutable filesystem root;
- start the child's model generation and streaming-safe local execution;
- grant only bounded speculative reads;
- stage the child's result and filesystem delta.

Until the parent plan seals, the child cannot publish filesystem state or dispatch
writes. Parent invalidation/cancellation discards or explicitly retains protected
orphan evidence according to policy. Recursive children follow the same rule.

This pipelines parent generation, child generation, Python execution, and
qualified reads instead of serializing every layer.

## Orthogonality

Streaming execution must have a complete off-state:

```text
streaming_execution=off
→ buffer complete source
→ ordinary admission
→ ordinary fresh Run
```

Independent switches should cover:

```text
incremental_validation
streaming_local_execution
speculative_reads
streamed_subagent_fanout
prepared_runtime
memory_cow
workspace_attempt
```

Incremental validation may run without execution. Local streaming may run with
all tools denied. Speculative reads may be disabled while filesystem speculation
continues. Prepared Runtime and memory COW only change performance.

## First proof

Use a versioned streaming program whose early closed prefix performs local
filesystem analysis and reaches one qualified delayed fixture read with literal
arguments while the remaining source continues to stream.

Treatments:

1. complete-source ordinary fresh baseline;
2. incremental validation only;
3. streaming local execution with tools denied;
4. streaming local execution plus qualified speculative read;
5. treatment 4 with final invalid suffix;
6. treatment 4 with changed arguments/source occurrence;
7. streamed two-child fan-out with parent valid/invalid variants;
8. prepared Runtime and memory COW independently off/on where supported.

Required results:

- valid streaming execution matches ordinary complete-source semantics;
- eager qualified reads may dispatch at closed literal call sites without
  reachability, while non-qualified calls wait for actual reach;
- each dispatched read occurs once and overlaps source generation;
- formal continuation or fallback never dispatches it twice;
- invalid suffix publishes no filesystem/output state and no writes;
- invalid suffix truthfully records the spent speculative read, cost, and orphan;
- unreachable eager-qualified literal calls may dispatch and are counted as
  orphaned waste; unreachable non-qualified calls do not dispatch;
- write calls never dispatch before the final barrier;
- cross-source, cross-occurrence, stale, and cross-partition staged-result reuse is
  rejected;
- disabling each mechanism restores its declared baseline.

Metrics:

```text
time to first closed suite
remaining generation time
speculatable local-prefix time
first reached tool-call position
qualified read latency and overlapped fraction
time from source end to final result
valid/invalid/abandoned rate
wasted speculative CPU and read count/cost
private/shared memory and overlay bytes
fan-out critical path and completion time
```

Stop or narrow if generated programs rarely contain useful early closed suites,
qualified reads usually occur after source completion, overlap is small, invalid
source waste is high, adapter policy excludes most useful calls, or incremental
module semantics require an unnatural Python subset.
