# Source-Bound Agent Programs: Compiler, Evidence, Lab and Paper Roadmap

> **For Hermes:** Use this document as the master roadmap. Split execution into the four linked megagoals below; do not attempt the entire roadmap as one autonomous run. Each megagoal must preserve the stop gates and update this roadmap with observed evidence before the next one starts.

**Goal:** Turn Pysolate's existing target-Guest semantic overlay, authority-bound execution mechanisms and Lab prototype into a source-bound agent-program system: Host-qualified compiler passes produce auditable boundary plans; recorder/evidence captures the resulting causal execution; Lab lets a human inspect code, inputs, outputs, physical reuse and workspace state; a final natural-workload campaign determines the paper claims.

**Architecture:** Keep exact target CPython as parser and executor. The Guest emits a bounded canonical semantic overlay; pure Host planner passes consume only verified overlay facts plus sealed Host contracts and frozen Run context; runtime consumers execute opaque qualified decisions through existing authority-bearing boundaries. A clean-break trajectory/evidence contract records source ranges, causal spans, physical/logical lineage and workspace checkpoints for a read-only human debugger. No pass receives authority, rewrites arbitrary Python, or becomes a second executor.

**Tech Stack:** Python `ast` in the target WASM Guest; Go semantic verification, legality, planning, runtime and content-addressed Lab storage; React/TypeScript Lab Web; optional bounded CPython `sys.monitoring` feasibility spike; Go/Python/TypeScript tests and real WASM Guest evidence.

---

## Status and claim vocabulary

**Roadmap status:** Proposed; architecture and megagoal decomposition accepted for initial planning, implementation not started.

**Pinned implementation baseline:** `911b33a314fcd66fda84fb7f28de4a40d60d102a` on `feat/programmatic-hot-approval` when this roadmap was authored.

Use these labels throughout implementation and paper artifacts:

- **Current:** verified in the pinned implementation or a later named commit.
- **Observed:** measured under a named source, Guest artifact, workload and command.
- **Proposed:** accepted design target without implementation evidence.
- **Deferred:** deliberately outside these four megagoals.
- **Rejected by evidence:** attempted direction whose current corpus/experiment did not justify an execution consumer.

Do not promote a proposal because it appears in this roadmap. Do not describe a skipped live Guest test as evidence.

This is the source of truth for future compiler/evidence/Lab/paper work. It builds on, but does not reopen, the completed slices in:

- [Unified effect-aware runtime megagoal](2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md)
- [Authority-bound programmatic tools and hot approval](2026-08-15-authority-bound-programmatic-tools-hot-approval-megagoal.md)
- [Authority-bound multi-Agent transparent campaign](2026-08-15-authority-bound-multi-agent-transparent-campaign-megagoal.md)
- [Observable workflow boundary optimization](2026-08-15-observable-workflow-boundary-optimization-megagoal.md)

Those documents remain implementation/evidence records. New future work should be added here or to one of this roadmap's four extracted megagoals rather than appended to a completed plan.

## Product and paper thesis

> **Pysolate lets agents program control flow without programming authority.**

An agent writes ordinary Python. Pysolate uses the exact target Guest AST to locate source-bound capability operations and conservative barriers. The Host—not the program—owns capability admission, grants, freshness, workspace, physical execution, sharing/cache decisions, approval and effect truth. The original source remains the executable authority; analysis and optimization plans are verified side tables.

The intended paper mechanism chain is:

```text
agent-written Python
  → target-Guest AST and source-indexed semantic overlay
  → Host verification against artifact/profile/import/Plan identities
  → deterministic default-off boundary passes
  → opaque qualified decisions
  → existing Broker/runtime consumers
  → source-bound causal evidence
  → human inspection in Lab
```

The current contribution candidates are:

1. **Authority-bound boundary compilation:** exact source occurrences are joined with sealed Host contracts without giving the analyzer or generated program authority.
2. **Authority-preserving physical execution:** direct and programmatic calls, pre-dispatch, exact sharing/reuse and hot approval preserve logical identities while physical execution remains Host-owned.
3. **Source-bound causal evidence:** every admitted or rejected boundary decision can be traced to code, authority context, physical producer/consumers, workspace state and terminal disposition.

Lab is an evidence and debugging artifact, not a claim that presentation itself proves correctness.

## Clean-break recorder and evidence decision

The next trajectory/recorder/evidence schema is a clean replacement, not a compatibility migration.

Allowed:

- delete checked-in v0 trajectory, experiment and scripted fixture data;
- replace trajectory schema/types/parsers/exporters and Lab ingestion outright;
- rename event types and reorganize storage around causal spans and artifacts;
- regenerate only the fixtures required by the new debugger and paper campaign;
- discard historical local/private captures when they do not conform to the new contract;
- remove compatibility adapters and dual-read paths.

Required:

- preserve current runtime authority and effect semantics; the clean break applies to observation/recorder/evidence data, not execution safety;
- keep private bodies opt-in/local-only and public projections body-safe;
- retain exact source/Guest/Host provenance for every new capture;
- bound body size, recursive decoding, tree depth, span count, workspace files and timeline events;
- distinguish unavailable/not-recorded data from empty values;
- preserve raw captured values behind the human projection; deterministic presenters may reorganize but must not invent interpretations.

There will be one current trajectory contract after Megagoal 2. Old fixture compatibility is not a deliverable.

## Is the compiler now flexible enough to add passes?

### Decision

**Yes for source-bound boundary planning; no for arbitrary Python optimization.**

The reusable infrastructure already exists:

- exact target-Guest `ast` parsing;
- canonical source spans, call-site IDs, function summaries, barriers and candidate regions;
- artifact/profile/import/Plan provenance binding;
- strict Host decoding and `VerifiedAnalysis`;
- shared fail-closed legality predicates and typed rejection reasons;
- immutable qualified proof objects such as `QualifiedCall`;
- run-scoped pre-dispatch and staged-observation consumer;
- logical-versus-physical execution evidence;
- default-off mechanism configuration and real Guest differential tests.

However, the code does not yet expose a generic pass-manager abstraction, and the overlay is deliberately not an executable IR. A new optimization is "add a pass" only when current facts, Host contracts and runtime primitives are sufficient.

### Three layers of a pass

```text
1. Fact extraction (Guest, no authority)
   native AST → bounded canonical facts + explicit unknowns

2. Planning/legality (Host, pure)
   VerifiedAnalysis + sealed Plan + frozen Run context
     → admitted/rejected candidates + opaque qualified decisions

3. Runtime consumer (Host, authority-bearing)
   qualified decision → existing Broker/runtime primitive + evidence
```

A pass may be added independently when it only consumes existing facts and an existing runtime primitive. If it needs new semantics, the pass slice must add all affected layers:

1. measured opportunity and explicit claim;
2. new canonical facts, if needed;
3. Host decoder/validation and schema identity;
4. sealed capability/effect contract, if needed;
5. shared legality predicate and stable rejection reasons;
6. deterministic planner ordering/conflict behavior;
7. existing or new bounded runtime consumer;
8. baseline-vs-enabled observable-trace parity;
9. real Guest positive and adversarial evidence;
10. source-bound recorder projection.

Adding only a Python AST visitor is not a safe optimization pass. Adding only a Host heuristic is not execution authority.

### Pass-manager target

Megagoal 1 should introduce a thin deterministic planner, not SSA or a second compiler backend. Its target shape is conceptually:

```text
VerifiedAnalysis
  + sealed capability Plan
  + frozen Run context
  + explicitly enabled PassSet
    → SourceBoundPlan {
        candidates,
        decisions,
        conflicts,
        rejection reasons,
        source ranges,
        required runtime consumers
      }
```

Required properties:

- immutable inputs and deterministic canonical output;
- stable pass name/version/config identity;
- declared fact and Host-contract requirements;
- explicit default-off enablement per pass;
- no capability/provider handles inside pass outputs;
- no direct dispatch from a pass;
- deterministic conflict resolution or fail-closed rejection;
- bounded candidates and decisions;
- source occurrence and dynamic occurrence kept distinct;
- planner/overlay digest is provenance, never by itself a cache key;
- unknown pass, fact, contract or conflict fails closed;
- pass rejection remains observable without changing baseline execution.

### Pass acceptance gate

A new optimization pass is admitted only when:

- a natural or public bounded corpus shows repeated opportunity;
- it supports a named paper or product claim;
- legality can be stated against observable behavior;
- it does not require ambient authority or replay;
- it has a bounded consumer and rollback/default-off path;
- the enabled run matches baseline result/exception, ordered effects, workspace disposition and logical-call semantics;
- physical work and latency changes are reported separately;
- rejected/unclassifiable cases remain in the denominator.

## Optimization family map

| Family | Current readiness | Likely value | Required work | Roadmap decision |
|---|---|---:|---|---|
| Source occurrence/provenance projection | Existing spans and IDs; runtime linkage incomplete | Very high for Lab/paper | Dynamic occurrence binding and recorder contract | Megagoal 1–2 |
| Multiple independent exact read pre-dispatch | Single exact call consumer exists | Potentially high if natural overlap exists | Multi-candidate planning, conflicts, budgets, bounded controller set | Candidate after Megagoal 4 census |
| Exact single-flight/shared producer at a source boundary | Whole-Run sharing exists; call-level `CanCoalesce` fails closed | High for multi-Agent story if observed | Explicit coalescing contract, computation identity, runtime join | Candidate after census |
| Durable completed-result cache at a source boundary | Runtime has retained whole-Run results; call-level cache contract absent | Medium/high but freshness-sensitive | Cache contract, publication/expiry, privacy partition, provenance | Do not pre-commit; census-gated |
| Read batching | No typed batching contract | Medium, workload dependent | Capability-specific batch equivalence and result mapping | Separate pass only after measured opportunity |
| Branch-aware pre-dispatch/hoisting | Overlay lacks sufficient branch/exception proof | Unclear | Control/exception facts and unused-speculation semantics | Deferred until corpus justifies it |
| Approval-bound source annotation | Hot approval exists; exact source linkage incomplete | High observability, not primarily optimization | Source occurrence binding and causal spans | Megagoal 1–3 |
| Ordinary pure-Python peepholes | Native CPython already executes them | Low paper value | Would duplicate native compiler concerns | Reject |
| Arbitrary region materialization/reuse | 19-program/69-region census found zero materializable overlap | Low under current evidence | Executable IR/heap state and new corpus | Rejected by current evidence |
| Semantic backend placement replacement | Prior experiment did not justify it | Low under current evidence | New representative workload and placement contract | Rejected by current evidence |

This table is a research reserve, not an implementation queue. Megagoal 4 may promote at most one or two pass families based on measured natural opportunity.

## New observation contract: required conceptual records

Megagoal 2 may rename these concepts, but the contract must represent them explicitly rather than burying them in untyped JSON text.

### Session and causal spans

- session/trace identity and provenance;
- actor identity and kind: model, agent, tool, Pysolate Guest, Broker, workspace, subagent;
- causal parent and explicit relation kind;
- stable start/end or point-in-time semantics;
- status and terminal disposition;
- typed child ordering independent of timestamps.

### Source artifacts and runtime occurrences

- source document ID, language, display path and exact digest;
- body reference with public/private availability policy;
- static source span and static call-site/region ID;
- dynamic occurrence ID;
- link from tool/PTC/ABI/Broker/receipt events to one occurrence;
- claim level: `program_range`, `source_bound`, `executed_line`, or `not_recorded`;
- optional line/instruction evidence must remain separate from AST/static linkage.

### Inputs, outputs and bodies

- typed model request/output and tool arguments/results;
- raw body reference;
- bounded deterministic nested-JSON decoding metadata;
- token/latency/cache metrics only when directly recorded or mechanically derived from named fields;
- no LLM-generated summary or semantic interpretation in the canonical projection.

### Runtime optimization lineage

- logical request ID and source occurrence;
- decision kind: ordinary, pre-dispatched, retained hit, single-flight follower, physical producer, rejected;
- physical execution ID;
- producer logical request and all consumers;
- computation/freshness/privacy/authority identity references;
- physical executions saved only as an explicitly defined derived metric;
- rejection reasons and unclaimed/orphaned/late dispositions.

### Approval lifecycle

- approval request/decision IDs;
- proposal, decision, lease expiry and dispatch-commit times;
- source-bound capability occurrence;
- approve/reject/expire/cancel outcome;
- handler outcome and receipt binding;
- no replay claim only when the same live execution evidence is present.

### Workspace checkpoints

- checkpoint and base-checkpoint identity;
- file manifest with path, kind, mode where portable, size and content digest;
- add/modify/delete relations;
- optional before/after body references under private-body policy;
- initial, intermediate and terminal checkpoint roles;
- explicit `not_captured` rather than inferred empty workspace.

## Human-first Lab contract

The default Lab view must answer human development questions before exposing evidence IDs.

### Left causal tree

```text
Session
└─ Turn
   ├─ Model request/output
   ├─ Tool call
   │  ├─ Generated code
   │  ├─ Pysolate Run
   │  │  ├─ source-bound capability occurrence
   │  │  ├─ reuse/cache/approval decision
   │  │  ├─ physical execution or producer link
   │  │  └─ receipt/result
   │  └─ Workspace changes
   └─ Step completion
```

Lifecycle start/end atoms become group status and duration, not default peer rows. Low-information transport/evidence atoms are collapsed but remain reachable.

### Inspector

Default tabs:

```text
Overview | Input | Output | Code | Timeline | Workspace | Evidence | Raw
```

Type-specific deterministic presenters choose the fields and links. IDs and digests live primarily under Evidence. Every presenter has a Raw escape hatch and test fixtures proving no field is fabricated.

### Timeline

- actor swimlanes;
- real intervals only when start/end semantics are recorded;
- point events otherwise;
- derived intervals visibly labeled;
- selection synchronized with tree, code and workspace;
- concurrency, approval wait and physical/logical reuse visible without reading raw JSON.

### Nested JSON

Decode only complete valid JSON values, recursively and with bounds. Preserve raw strings and label decoded nested strings structurally. Do not substring-guess, execute, repair or semantically summarize malformed JSON.

### Source view

- selecting a Run highlights its `program_range`;
- selecting a typed boundary occurrence highlights the exact `source_bound` span;
- selecting an execution-line event highlights only `executed_line` evidence;
- multiple records may link to the same source range;
- source view lists all related events, physical producer and workspace changes;
- static AST spans must never be labeled as executed-line evidence.

### Workspace view

- checkpoint selector and file tree;
- added/modified/deleted markers;
- before/after diff when both bodies exist;
- generated code and output files viewable under body policy;
- direct links between tool/source events and workspace changes;
- public body omissions explicit and non-erroring.

## Megagoal decomposition

The roadmap is deliberately split into **four megagoals**. Each produces a useful artifact and has a stop gate. A `/goal` run must execute only one megagoal unless the user explicitly joins them after reviewing the gate evidence.

---

## Megagoal 1 — Source-Bound Compiler Pass and Provenance Foundation

**Status:** Complete at the Megagoal 1 stop gate on 2026-08-15. Final independent review: PASS at `4a7f74b5a78ab3a68fbeebcd13ad20ff445fcde2`. Megagoal 2 has not started.

**Extracted execution plan:** [2026-08-15-source-bound-compiler-pass-provenance-megagoal.md](2026-08-15-source-bound-compiler-pass-provenance-megagoal.md)

**Purpose:** Make the existing semantic infrastructure explicitly pass-oriented and bind static source occurrences to dynamic boundary occurrences without creating an executable IR.

**Primary areas:**

- `guest/bootstrap/agent_runtime/semantic.py`
- `guest/tests/test_semantic_analysis.py`
- `runtime/semantic/contract.go`
- `runtime/semantic/verified.go`
- `runtime/semantic/legality.go`
- new thin planner/pass files under `runtime/semantic/`
- Broker/receipt/trajectory projection seams only as needed for occurrence binding
- real Guest semantic/PTC integration tests

**Tracks:**

- [x] Freeze vNext source document, static occurrence, dynamic occurrence and claim-level vocabulary.
- [x] Refactor extraction into bounded fact producers where this reduces coupling; preserve exact target AST and canonical output.
- [x] Add deterministic pass descriptor/config/result contract and pure planner orchestration.
- [x] Wrap the current semantic pre-dispatch qualification as the first pass without changing runtime behavior.
- [x] Emit admitted and rejected source-bound decisions with stable reasons.
- [x] Bind PTC/direct Broker calls to exact dynamic occurrences where supported.
- [x] Spike `sys.monitoring` or equivalent exact target-Guest instruction/line positions; measure overhead and portability, and keep it optional or reject it.
- [x] Add differential real Guest tests proving disabled parity, source-span accuracy and fail-closed unknown/conflict handling.

**Definition of Done:**

- current pre-dispatch behavior is represented by the thin pass API;
- pass enablement is independent and default-off;
- source-bound capability events can identify exact source document/span and dynamic occurrence in at least one real programmatic Guest execution;
- no pass can dispatch directly or mint authority;
- optional executed-line tracing is either qualified with measured overhead or explicitly rejected with evidence;
- no arbitrary source rewriting, SSA, region executor or heap capture is introduced.

**Stop gate:** Review the pass API and real source-link evidence before changing the recorder contract. If exact dynamic source binding requires target-Guest behavior that cannot be qualified, keep `program_range` plus static `source_bound` evidence and do not claim executed lines.

---

## Megagoal 2 — Clean-Slate Recorder, Evidence and Workspace Contract

**Purpose:** Replace v0 flat events and scripted fixture assumptions with one typed causal trajectory contract suitable for live Harness/Pysolate capture and human inspection.

**Primary areas:**

- `research/trajectory/`
- `research/labstore/`
- Runtime/CLI/Harness recorder attachment points
- trajectory/evidence exporters
- `apps/lab-web/src/trajectoryData.ts` or its replacement
- `apps/lab-web/public/lab-data/`
- recorder and body-policy docs/tests

**Tracks:**

- [ ] Delete v0 compatibility requirements and freeze one new schema.
- [ ] Implement causal spans, actors, relation kinds, typed start/end semantics and bounded ordering.
- [ ] Add source documents/ranges/dynamic occurrence records from Megagoal 1.
- [ ] Add typed model/tool/PTC/Broker/approval/runtime records rather than relying on opaque body text.
- [ ] Add logical/physical producer-consumer lineage and deterministic links.
- [ ] Add content-addressed workspace checkpoints, manifests and optional private bodies.
- [ ] Add bounded raw-body storage plus recursive JSON presentation metadata.
- [ ] Attach the recorder to one real execution path; scripted fixtures alone do not satisfy completion.
- [ ] Generate one private full fixture and one public body-safe projection from the same typed trace.
- [ ] Delete stale checked-in v0 trajectory/experiment fixtures and parsers.

**Definition of Done:**

- one current trajectory schema exists with no dual-read adapter;
- one real run records model/tool/runtime/source/workspace relationships end to end;
- public projection contains no private bodies, paths or credentials;
- private local fixture can inspect captured generated code and output files;
- every interval, link and derived metric has explicit source semantics;
- malformed/oversized/deep records fail closed.

**Stop gate:** Validate the data contract and real capture before redesigning Lab. If source/workspace bodies are unavailable, Lab must display explicit absence rather than inventing or reconstructing them.

---

## Megagoal 3 — Human-First Source-Bound Lab Debugger

**Purpose:** Rebuild Lab around causal tasks and developer questions instead of flat event JSON.

**Primary areas:**

- `apps/lab-web/src/App.tsx` or replacement shell
- type-specific presenter modules
- causal-tree, timeline, source and workspace components
- Lab CSS/design system
- fixtures and browser/visual tests

**Tracks:**

- [ ] Replace the flat event list with Session → Turn → Step → Tool/Run causal tree grouping.
- [ ] Collapse start/end and low-information atoms into group summaries.
- [ ] Add Overview/Input/Output/Code/Timeline/Workspace/Evidence/Raw inspector tabs.
- [ ] Add bounded recursive JSON decoding with raw preservation.
- [ ] Add clickable causal, producer/consumer, approval and workspace relations.
- [ ] Add actor swimlane timeline with honest point/interval semantics.
- [ ] Add source viewer with program-range, source-bound and optional executed-line highlighting.
- [ ] Add workspace checkpoint explorer and before/after diff.
- [ ] Add Pysolate-specific cards for pre-dispatch, hit/miss, single-flight, producer/consumer, approval and terminal dispositions.
- [ ] Move IDs/digests into Evidence while preserving copy/link affordances.
- [ ] Verify responsive desktop layout, keyboard navigation, empty/missing-body states and large bounded traces.
- [ ] Perform real browser visual QA against the real private fixture and public projection.

**Definition of Done:**

A developer can select generated Python, see which exact source boundary produced a tool/runtime chain, follow a cache/shared-producer link, inspect approval timing, view recorded workspace code/diffs and return to raw evidence without reading an unstructured ID wall. Type-specific summaries are deterministic projections, not generated interpretations.

**Stop gate:** User visual/product review. Do not proceed to paper screenshots or optimizer claims until tree grouping, source synchronization, workspace inspection and nested JSON are accepted on a real trace.

---

## Megagoal 4 — Natural Workload Census, Pass Selection and Paper Evidence

**Purpose:** Use real Agent-generated programs and the new debugger/evidence substrate to choose at most one or two additional boundary passes and freeze defensible paper claims.

**Primary areas:**

- private local workload collection and scrubbed public fixtures
- research corpus/census tools
- selected pass implementation under `runtime/semantic/`
- real Guest/Harness differential experiments
- `docs/research/`, `docs/evidence/` and paper figures/tables

**Tracks:**

- [ ] Define a bounded real program corpus with provenance classes, oracle classes and privacy policy.
- [ ] Capture direct/programmatic/both usage, source-bound call sites, barriers, opportunities, rejection reasons and overlap windows.
- [ ] Measure task success, model turns, tokens, logical calls, physical executions, critical path and authority/freshness near misses.
- [ ] Keep rejected/unclassifiable programs in denominators.
- [ ] Apply the pass acceptance gate to the optimization family map.
- [ ] Promote at most one or two candidates; otherwise publish the negative result and stop.
- [ ] Implement selected passes through the Megagoal 1 API with independent switches and real Guest differential tests.
- [ ] Run paired baseline/enabled and adversarial authority/freshness/privacy/cancellation cohorts.
- [ ] Produce one source-bound Lab case study showing code → decision → physical lineage → workspace result.
- [ ] Freeze claim/evidence matrix, limitations, negative results and exact source/Guest/environment identities.

**Definition of Done:**

- the paper story is supported by natural workload evidence rather than only scripted fixtures;
- any new pass has measured opportunity and observable-equivalence evidence;
- unsupported optimization families remain rejected/deferred rather than partially implemented;
- figures and Lab artifacts are reproducible from named local/public evidence;
- no claim exceeds the recorded evidence.

**Stop gate:** Paper scope review. Do not enter cold continuation, cross-process snapshot, mmap allocation, production scheduling, general region execution or broad external-write orchestration without a new roadmap and separate evidence gate.

## Cross-megagoal verification policy

Every code megagoal must use TDD and finish with risk-proportionate gates. At minimum:

```text
go test -race ./... -count=1
go vet ./...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
cd apps/lab-web && npm test -- --run && npm run build
git diff --check
```

Run real Guest tests with a named exact artifact and record skips honestly. Lab UI work additionally requires browser screenshots/visual inspection and fixture-based interaction tests. Independent review should target protocol boundaries, source/runtime identity, recorder body safety and presenter non-invention rather than re-running every broad gate mechanically.

After each coherent slice:

1. update the active megagoal and this master roadmap;
2. record Current/Observed/Rejected status accurately;
3. run targeted then megagoal gates;
4. create a signed commit and push when the slice is verified;
5. continue within the same megagoal until its stop gate or a real blocker;
6. do not silently cross into the next megagoal.

## Explicit non-goals for all four megagoals

- arbitrary Python source rewriting as executable authority;
- SSA/general-purpose executable IR;
- heap snapshot or arbitrary CPython continuation serialization;
- cold pageout, mmap allocator or cross-process restore;
- replay after started/may-have-started/ambiguous effects;
- ambient filesystem/network/shell authority expansion;
- production scheduler or unlimited durable workflow;
- Cloudflare-style request replay;
- autonomous permission expansion;
- LLM-authored interpretation inside canonical Lab evidence;
- compatibility support for obsolete recorder/evidence fixtures.

## First execution handoff

The next autonomous goal should implement **Megagoal 1 only**. Before implementation it should extract a dedicated plan from this roadmap with exact tasks and files, then preserve the current runtime behavior while introducing the thin pass abstraction and one real source-bound PTC/pre-dispatch evidence path. It must stop for review before deleting or replacing recorder data in Megagoal 2.
