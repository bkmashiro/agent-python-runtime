# Effect-aware runtime related-work truth matrix

Status: **Current source-pinned research contract**

Verified at: 2026-08-14T13:45:30Z

This document limits the related-work claims that may be used by the Unified Effect-Aware Runtime project. A source pin proves only the cited public behavior; absence from a public source is not evidence about an unpublished implementation.

## Pinned primary sources

| System | Pin | Primary evidence used here |
|---|---|---|
| AsyncFC | [arXiv:2605.15077v1](https://arxiv.org/abs/2605.15077v1) | “Dependency Specification with Labeling”, “Conflict Analysis”, and “Annotation Robustness” |
| Agent JIT | [arXiv:2605.21470v2](https://arxiv.org/abs/2605.21470v2) | abstract and Sections 3.1–3.3 on invariant-enforcing tools, CFG validation/cost, and scheduling |
| PASTE | [arXiv:2603.18897v3](https://arxiv.org/abs/2603.18897v3) | non-interference design and speculative tool scheduler |
| Workload-Aware Caching | [arXiv:2607.20495v1](https://arxiv.org/abs/2607.20495v1) | cache model and eviction-policy sections |
| CaMeL | [arXiv:2503.18813v2](https://arxiv.org/abs/2503.18813v2), [source `f083b6b`](https://github.com/google-research/camel-prompt-injection/tree/f083b6b396399d3b3c7f2ddaf613a5945eaf32d8) | paper control/data-flow claim and custom interpreter source |
| A1 | [source `604e0e2`](https://github.com/stanford-mast/a1/tree/604e0e21e539ed359827501dabf5477ee3493408) | README, `src/a1/cfg_builder.py`, and `src/a1/codecost.py` |
| ARIES | [source `b8e6df3`](https://github.com/hyscale-lab/ARIES/tree/b8e6df3dd912d7c9fbee3cb1fe66b119e468d5a3) | README architecture and current-implementation tables |
| Cloudflare Code Mode | [2025-09-26 public design](https://blog.cloudflare.com/code-mode/) | MCP-to-TypeScript API and sandboxed generated-code design |
| Cloudflare Computer | [2026-08-03 public preview](https://blog.cloudflare.com/cloudflare-computer/) | shared filesystem and isolate/container/browser execution abstraction |
| DeltaBox | [arXiv:2605.22781v2](https://arxiv.org/abs/2605.22781v2) | incremental filesystem/process checkpoint/rollback |
| Crab | [arXiv:2604.28138v1](https://arxiv.org/abs/2604.28138v1) | OS-visible effect classification for checkpoint granularity |
| SpecBox | [arXiv:2607.23933v2](https://arxiv.org/abs/2607.23933v2) | intent-driven prewarming, stochastic prefetch, and semantic result cache |

## Claim-by-claim matrix

### AsyncFC

**Verified:** AsyncFC is an execution-layer future-based function-calling runtime. It uses hierarchical resource read/write labels, tracks conflicts in a State Tree, and dispatches a call after blocking label futures resolve. Missing annotations conservatively become root read/write sets and serialize tool execution. Labels may be developer-authored, rule-derived, or generated offline.

**Pysolate distinction that remains supportable:** AsyncFC’s primary semantic object is a function call plus resource labels. Pysolate is investigating whether exact target-Guest program structure can propagate Host-owned contracts through control/data flow and support several consumers beyond call dispatch.

**Prohibited:**

- “AsyncFC greedily parallelizes unsafe calls.”
- “AsyncFC has no conservative fallback.”
- “Pysolate is novel merely because tools declare reads and writes.”

### Agent JIT

**Verified:** Agent JIT compiles task descriptions into executable plans, validates tool sequences using precondition/postcondition state invariants, builds CFGs for validation and cost estimation, and searches scheduling/parallelization strategies using learned latency distributions.

**Pysolate distinction that remains supportable:** Agent JIT primarily validates, ranks, and schedules generated web-agent plans. Pysolate’s proposed question is whether one effect/dependency representation of the selected Python program can conservatively qualify scheduling, exact execution identity, and placement.

**Prohibited:**

- “Agent JIT provides no applicability or correctness checks.”
- “Agent JIT does not inspect a CFG.”
- “Any Pysolate concurrency is automatically stronger than Agent JIT scheduling.”

### PASTE

**Verified:** PASTE predicts concrete future invocations from recurring patterns, executes admitted candidates while generation continues, hides speculative results until a canonical authoritative invocation matches, and requires side-effect-free policy or a non-mutating safe variant for side-effecting tools.

**Pysolate distinction that remains supportable:** PASTE predicts a future call that is not yet authoritative. Pysolate’s proposed hoisting lane starts from a call already present in the accepted executable program and requires legality before moving issue time.

**Prohibited:**

- “PASTE blindly executes side effects.”
- “Hoisting and predictive speculation have the same correctness contract.”

### Workload-Aware Caching for Multi-Agent Systems

**Verified:** the evaluated cache stores intermediate task results and keys entries by a tuple of task description and subject identifier emitted by the planning agent. Its main contribution is workload-aware eviction using recomputation cost, DAG dependency count, and invocation frequency.

**Pysolate distinction that remains supportable:** Pysolate’s proposed identity is an exact executable-region identity bound to canonical live-ins and runtime/effect context, not a natural-language task key.

**Prohibited:**

- “Existing multi-agent caching has no identity.”
- “Executable-region identity is semantic program equivalence.”

### CaMeL

**Verified:** CaMeL extracts control and data flow from trusted queries and enforces security/capability policies. Its pinned implementation parses Python with `ast.parse` in `src/camel/interpreter/interpreter.py` and tracks dependencies in interpreter values.

**Pysolate distinction that remains supportable:** CaMeL demonstrates that generated Python can be non-opaque for security. Pysolate investigates target-Guest semantic facts plus Host-owned effect contracts as a runtime-optimization interface.

**Prohibited:**

- “No previous system analyzes generated Python.”
- “AST/dataflow access is itself Pysolate’s novelty.”
- “CaMeL is only prompt filtering.”

### A1

**Verified:** A1 compiles an Agent AOT or JIT, generates and verifies candidates, and ranks them. At the pinned commit, `src/a1/codecost.py` parses Python AST, builds a CFG with `CFGBuilder`, traverses blocks, and estimates tool-call cost with loop/comprehension depth.

**Pysolate distinction that remains supportable:** A1 optimizes generation, verification, costing, and selection of agent code. Pysolate’s proposed focus is semantics-preserving execution transformations and placement of the accepted program under Host-owned runtime authority.

**Prohibited:**

- “A1 does not inspect generated code.”
- “A1 has no AST or CFG.”
- “A1 and Agent JIT are the same system.”

### ARIES

**Verified:** ARIES is an experimentation framework whose Runner composes `Benchmark`, `AgentHarness`, `ToolSandbox`, and `ToolBridge`. It preserves task semantics across configurations, records end-to-end trajectories, and currently supports explicit harness, benchmark, Docker sandbox, bridge, and model-service implementations.

**Pysolate relationship:** ARIES is an evaluation/integration target, not evidence that the Pysolate semantic optimizer already works. Pysolate may fit behind its sandbox/bridge boundary only after a bounded adapter proves lifecycle equivalence.

**Prohibited:**

- “ARIES is a competing whole-program optimizer.”
- “An ARIES production trace is a generated-Python corpus.”

### Cloudflare Code Mode and Computer

**Verified:** Code Mode converts MCP schemas to a TypeScript API and executes agent-generated code in a sandboxed environment with connectivity/authorization handled outside generated code. Computer publicly describes a durable shared filesystem and multiple execution environments, including isolates and containers; its examples allow the model or configured tools to select a backend.

**Pysolate distinction that remains supportable:** Pysolate proposes deriving conservative optimization and pre-execution placement facts from the executable program and Host-owned contracts. Public Cloudflare material does not establish whether Cloudflare internally does or does not perform comparable whole-program analysis.

**Prohibited:**

- “Cloudflare has only one backend.”
- “Cloudflare cannot select execution environments.”
- “Cloudflare performs no semantic optimization internally.”
- “Pysolate is Cloudflare Code Mode implemented in Python/WASM.”

### DeltaBox, Crab, and SpecBox

**Verified:** DeltaBox optimizes incremental sandbox checkpoint/rollback; Crab observes OS-visible effects to avoid unnecessary checkpoints; SpecBox predicts sandbox demand for prewarming/prefetch and includes semantic caching.

**Pysolate relationship:** these systems strengthen the case that prepared state, COW, checkpoints, and sandbox scheduling are supporting mechanisms rather than sufficient top-level novelty for Pysolate.

**Prohibited:**

- “No nearby system reuses sandbox state.”
- “Observed OS effects and static program effects are interchangeable.”

## Allowed concise positioning

> Existing systems optimize tool-call schedules, generated plan selection, predicted future calls, task/DAG caches, security dataflow, or sandbox state. Pysolate investigates whether a qualified subset of the exact executable agent program can act as a shared semantic interface for conservative scheduling, exact reuse, and pre-execution placement under Host-owned authority.

This is a research hypothesis, not a Current implementation claim.
