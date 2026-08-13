# Multi-agent shared execution: next falsifiable prototype

Status: **research decision draft, 2026-08-13**. This is one bounded next step, not a commitment to build a global scheduler, effect lattice, general Python JIT, or cross-tenant cache.

## Decision

Build a **recorder-first shared-node multi-agent prototype**:

> Several logical agents invoke explicitly admitted, identical Python computations. On a concurrent miss, Pysolate executes at most one physical fresh Guest per invocation identity, returns that result to every waiting logical agent, and records enough evidence to prove both the logical trajectories and the reduced physical work.

This is the smallest next step that simultaneously:

- responds to the multi-agent research direction;
- tests whether Pysolate can reduce work rather than only sandbox it;
- reuses existing `runtime/subagent` branch isolation and `runtime/agentfunction` identity/cache/single-flight mechanisms;
- remains falsifiable before any massive multi-tenant design;
- does not turn Pysolate into another general remote computer.

## Why not start from the handoff's full A–F list

Several proposed mechanisms already exist in bounded form:

- `runtime/agentfunction.Invocation` binds function source, artifact/profile, import closure, canonical inputs, immutable roots, deterministic settings, output schema, project, privacy partition, and policy epoch into one identity;
- admission is already fail-closed as `cacheable` or `not_cacheable`;
- the guard models forbidden Host calls, undeclared reads, shared writes, clock, randomness, and dynamic imports;
- completed result retention and concurrent single-flight are independent;
- `runtime/subagent` already executes sibling-private child attempts and explicitly selects or discards roots.

The missing proof is not another enum or cache. It is a real composition in which multiple logical agents share one physical **Guest** computation, with measured benefit and failure boundaries. Today `agentfunction.Engine` coalesces a Host-supplied `ComputeFunc`, while `subagent.FreshRunnerExecutor` always creates one fresh Runner per child; the acceptance test's single-flight callback returns fixture bytes rather than executing CPython. The prototype must bridge the single-flight leader to an actual fresh Guest and keep every logical child identity visible. The current guard is a Host-instrumented mechanism; it is not yet proof that arbitrary Guest Python is pure.

## Recorder comes first

The current public Lab recording is a Runtime acceptance trace, not a full agent trajectory. A future experiment must have two linked evidence planes.

### Harness-owned trajectory

Record privately, with explicit retention and sensitivity labels:

- user/system/assistant/tool message identity, role, ordering, and body reference;
- model provider/model identity, request/response timestamps, token usage, outcome, and body references;
- agent creation, parent agent, branch/join/winner decisions;
- generated Python and the message/model call that produced it;
- tool-call request/result and final response.

Credentials and transport secrets are never retained as conversation bodies. Raw bodies remain private or use an explicitly public fixture; the public projection is allowlisted rather than relying only on string replacement.

### Pysolate-owned execution evidence

Record:

- logical invocation ID and physical execution ID separately;
- invocation/content identity and cache partition;
- leader, waiter, completed-hit, miss, rejection, and recomputation outcomes;
- Guest create/start/end/destroy timing and resource metrics;
- source identity plus stable AST node/span for each explicit shared-node invocation;
- typed Host/WASI effect attempts and admission violations;
- ordered filesystem mutation events (`create/write/rename/delete`) with bounded path and before/after identity when observation is enabled;
- workspace checkpoint identity before and after each execution-DAG node;
- path delta between those checkpoints;
- result identity and serialization/materialization cost.

All records join through `run_id`, `task_id`, `agent_id`, `span_id`, `invocation_id`, and `physical_execution_id`.

“Record everything” means full causal evidence for the experiment, not copying secrets or private arbitrary content into public JSON.

## Implementation slices

### Slice 0 — evidence spine, no scheduling change

Extend the canonical private trace before changing execution behavior:

- distinguish logical invocation from physical Guest execution;
- record Harness message/model/tool envelopes when they actually exist;
- persist stable source/AST span identity for explicit shared-node calls;
- capture workspace checkpoint roots and path deltas at execution-node boundaries;
- preserve the current independent-execution baseline.

The current acceptance fixture has no real LLM call—its checked-in corpus identifies the model as `development-fixture`—so its conversation lane should remain empty rather than fabricate messages. A later Hermes/ARIES run supplies real message/model evidence.

### Slice 1 — one real shared physical Guest node

Compose `agentfunction` single-flight with an actual fresh Guest computation for 8 logical child agents. The shared node returns only an immutable serialized value; any child-specific filesystem materialization happens afterward inside that child's private workspace. First prove the baseline and coalesced treatments, identity, isolation, failure behavior, and trace consistency. Do not add completed retention, prepared/COW, or a scheduler yet.

### Slice 2 — controlled matrix

Only after Slice 1 passes, add 32 agents, controlled identity overlap, completed private retention, resource measurements, and the stop-condition analysis below.

## Source and filesystem scope

The first prototype should not promise arbitrary statement tracing or a time-travel filesystem.

- Parse the explicitly generated shared-node call on the Host and assign a stable AST node ID and source span. This is sufficient to link a shared physical execution to the code region that requested it.
- Do not initially instrument every Python statement with `sys.settrace` or an AST rewrite; measure that separately if JIT work later needs dynamic node execution counts.
- Snapshot the workspace at execution-DAG node boundaries, not every syscall. Between checkpoints, retain an ordered mutation journal when the backend can supply one. Persist checkpoint root identity plus a path delta; a time-point explorer is enabled only when base + ordered deltas are complete. Periodic full manifests can bound replay cost; content blobs remain private and content-addressed.

The current public dataset contains only whole-program source ranges and one base-to-child-final delta per child. It cannot reconstruct intermediate states.

## Experiment

### Population

Start small enough to debug:

- 8 and 32 logical child agents;
- 1, 2, and 4 distinct admitted invocation identities;
- controlled duplicate ratios of 0%, 25%, 50%, and 75%;
- fixed public Python functions over immutable, public inputs;
- one negative function per run that attempts a forbidden effect and must fail closed without producing a reusable result.

Do not start at 10,000 logical agents. Scale only after the counters and causal trace agree.

### Treatments

1. **Independent** — every logical agent receives a fresh physical Guest execution.
2. **Coalesced** — concurrent identical invocations share one in-flight physical execution; no completed retention.
3. **Retained** — coalescing plus project-private completed-result reuse.

Prepared/COW execution remains off in the first comparison. It can be a later orthogonal treatment after logical-to-physical reduction is established.

### Primary metric

```text
physical Guest executions / logical agent invocations
```

Supporting metrics:

- avoided physical executions;
- leaders, waiters, hits, misses, and purity rejections;
- end-to-end P50/P95/P99 logical latency;
- physical Guest runtime, create/destroy count, CPU time, peak RSS, and materialized bytes;
- queueing and waiter latency;
- serialization and cache lookup overhead;
- result equality and workspace isolation;
- recorder overhead.

### Stop conditions

Narrow or stop if:

- logical agents rarely request identical admitted computations;
- admission requires unnatural generated code;
- lookup, serialization, or materialization dominates the avoided computation;
- a failed/forbidden execution can populate or poison the shared result;
- identity or partition boundaries prevent a convincing noninterference argument;
- reductions in physical work do not improve latency, density, or CPU.

## What follows only if this works

1. Replay the mechanism on a small number of real multi-agent trajectories and measure natural overlap.
2. Add a tiny scheduling choice set—`SERIAL`, `PARALLEL`, `HEDGE`—using observed latency distributions, rather than general Python auto-parallelization.
3. Add prepared/COW as an orthogonal physical-density treatment.
4. Consider a richer effect classification only when a concrete scheduler or admission rule needs it.
5. Consider cross-project or cross-tenant reuse only after privacy/equality-side-channel analysis; current reuse remains project-private.

## Relationship to the reference systems

### Agent JIT Compilation

The transferable lesson is constrained optimization: semantic tools with pre/postconditions, many candidate plans checked through CFG traversal, and a deliberately tiny scheduling space evaluated from offline latency distributions [2]. It does not establish general automatic parallelization of arbitrary Python. Pysolate should initially borrow the small, measurable scheduling space—not its web/DOM-specific planner cache.

### CaMeL

The transferable lesson is that control-flow isolation alone is insufficient: the provenance of data entering an effect matters. CaMeL obtains value-level provenance through a custom restricted-Python interpreter [1]. Pysolate executes real CPython, so the bounded analogue is provenance on structured Harness messages, immutable observations, shared-node inputs, and typed Host-call arguments—not arbitrary Python object taint.

### Learning to Share

Learning to Share directly targets redundant work across parallel agent teams, but shares textual intermediate steps through task-local global memory: natural-language summaries are keys, raw agent outputs are values, and a learned controller chooses what to admit [5]. This is semantically closer to shared memory/context than to Pysolate's proposed execution identity and effect-enforced physical work sharing.

### Workload-Aware Caching for Multi-Agent Systems

This concurrent work already caches exact intermediate task results in multi-agent DAGs and contributes an eviction score over dependency count, recomputation cost, and agent frequency [4]. Its cache keys are planner-emitted task description/subject identifiers; its evaluation targets retained LLM/OCR/visual task outputs under finite capacity. Therefore **multi-agent DAG node caching or lower latency from cache hits is not a sufficient Pysolate novelty claim**.

The narrower Pysolate question remains distinct only if the prototype proves execution semantics the caching paper does not study: content-/environment-bound invocation identity, runtime-enforced rejection of effects, in-flight coalescing into one physical CPython Guest, private workspace isolation, and a logical-to-physical execution trace. Eviction policy should not be the first contribution.

### ARIES

At pinned source `b8e6df3dd912d7c9fbee3cb1fe66b119e468d5a3`, ARIES separates Benchmark, AgentHarness, ToolSandbox, ToolBridge, model, and monitoring [3]. Its Hermes harness already exports message-level `sessions.jsonl` telemetry. However, `ToolSandbox` is a full task environment with `Exec`, `Upload`, and `Download`, used by benchmark preparation and evaluation. A direct Pysolate-as-ToolSandbox adapter would force Pysolate to emulate a general computer.

Use ARIES later as the experiment/harness layer while retaining its task sandbox and invoking Pysolate as the generated-Python execution backend/tool. Join ARIES trajectory telemetry and Pysolate execution evidence by shared run/task/agent/span identities.

## Non-goals for the next prototype

- general automatic extraction of pure regions from arbitrary Python;
- a five-level effect lattice across every tool;
- global scheduler or 100k-user execution fabric;
- cross-tenant result sharing;
- full CaMeL-style value taint;
- arbitrary Python statement/bytecode tracing;
- replacing Docker/SSH in all ARIES benchmarks;
- claiming prepared CPython density before a matched benchmark;
- claiming shared physical execution as novel before a broader dataflow/serverless/work-sharing related-work review.

## Sources

1. Google DeepMind et al., [Defeating Prompt Injections by Design](https://arxiv.org/abs/2503.18813) (CaMeL), arXiv:2503.18813v2.
2. Caleb Winston et al., [Agent JIT Compilation for Latency-Optimizing Web Agent Planning and Scheduling](https://arxiv.org/abs/2605.21470), arXiv:2605.21470.
3. HyScale Lab, [ARIES pinned source](https://github.com/hyscale-lab/ARIES/tree/b8e6df3dd912d7c9fbee3cb1fe66b119e468d5a3), commit `b8e6df3dd912d7c9fbee3cb1fe66b119e468d5a3`.
4. Anas Mohamed et al., [Workload-Aware Caching for Multi-Agent Systems](https://arxiv.org/abs/2607.20495), arXiv:2607.20495v1.
5. Joseph Fioresi et al., [Learning to Share: Selective Memory for Efficient Parallel Agentic Systems](https://arxiv.org/abs/2602.05965), arXiv:2602.05965v2.
