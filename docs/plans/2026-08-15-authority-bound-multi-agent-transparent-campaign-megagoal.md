# Authority-Bound Multi-Agent Execution and Transparent Campaign Autonomous Mega-Goal

> **For Hermes:** This is a prepared `/goal` handoff, not an instruction to execute during
> the session that authored it. When Yuzhe explicitly starts it with `/goal`, read this
> file fully, inspect the live repository, and continue across independently verified
> slices. A green test, signed commit, or completed track is a checkpoint—not a stopping
> condition. Stop only at the decision and safety gates below.

**Status:** Active — architecture gate resolved; Track E real adapter next
**Date:** 2026-08-15
**Owner:** Yuzhe
**Repository:** `~/projects/agent-python-runtime`
**Prepared baseline:** `78d55b622eae24a64f13f92a6b187d1d9929d61a`
**Predecessor:** [`2026-08-15-observable-workflow-boundary-optimization-megagoal.md`](2026-08-15-observable-workflow-boundary-optimization-megagoal.md), complete and historical

## Mission

Strengthen Pysolate's core authority-lifecycle claim for multi-agent execution, then
explain that claim through one exact, transparent, paper-oriented campaign in which
**20 bounded Python programs arrive at declared times** and every admission, queue,
sharing, physical execution, wait/resume, workspace, rejection and terminal decision is
visible and causally explained.

The target claim is deliberately narrower than “an Agent OS” or “a faster scheduler”:

> Logically independent Agent programs may share a qualified physical producer or exact
> verifier result without sharing Guest continuation, capability authority, private
> workspace ownership or effect truth; suspended and delegated work remains bound to
> Host-owned authority throughout its lifecycle.

The campaign must make this statement understandable without a live model or a live
Agent Harness. It is a Pysolate test corpus and Runtime experiment. The Lab is a read-only
storytelling and inspection surface, not an execution controller.

## Paper story

The main narrative is not a leaderboard. It is a transparent execution walkthrough:

```text
20 exact Python programs
+ fixed release offsets
+ explicit dependencies, Plan/grant and workspace identities
+ bounded physical execution slots
                  |
                  v
arrival -> admission -> queue -> dispatch -> physical execution
        -> wait/release/resume -> workspace branch/seal/select/discard
        -> exact sharing or reason-coded rejection -> terminal disposition
```

A reader must be able to answer:

1. Which program arrived at each time?
2. Why did it execute, wait, share, reject or resume?
3. Which logical programs mapped to which physical executions?
4. Which authority and workspace identities remained distinct?
5. What changed between all-off baseline and the qualified treatment?
6. Which facts are measured, which are fixture inputs, and which claims are forbidden?

No model latency, hidden reasoning, counterfactual duration or scheduler decision may be
invented. Actual monotonic timestamps come from execution; release offsets and policy are
manifest inputs.

## Value function

Prefer, in order:

1. authority-lifecycle correctness and adversarial evidence;
2. one understandable multi-agent mechanism claim;
3. a small exact corpus with a strong visual narrative;
4. composition or refactoring of existing mechanisms;
5. measured logical-to-physical execution evidence;
6. practical paper/Lab inspection;
7. performance only when matched evidence supports it.

A negative result is useful. If qualified sharing does not reduce physical work, or an
authority invariant requires a broad rewrite, preserve the evidence and stop at the
relevant decision gate rather than manufacturing a feature or speedup.

## KISS and code-size contract

Pysolate must not grow one package per idea.

1. Extend or refactor existing `capability`, `subagent`, `workflow`, `agentfunction`,
   `workspace`, `observe`, `trajectory` and `workflowbench` seams first.
2. Do not add a new top-level Runtime package unless it owns an independent lifecycle and
   authority invariant that existing packages cannot express.
3. Extend `research/workflowbench` for the 20-program campaign rather than creating a
   second benchmark framework or production scheduler.
4. Extend the existing Lab and trajectory/evidence path rather than adding a second
   viewer or debugger schema without a proved incompatibility.
5. Delete or consolidate superseded fixture/generator paths when the replacement is
   verified. Do not retain compatibility solely for data deliberately reset in this
   research branch.
6. Prefer adding a field, validator, typed disposition or composition test over adding an
   abstraction layer.
7. Before every implementation track, inventory nearby code and record what will be
   reused, refactored, removed and added. If additions materially exceed the governing
   seam, stop and present the smaller design.

## Non-negotiable boundaries

- The Host owns Plan, grant, authority, effect truth, operation identity, scheduling
  policy and terminal disposition. Guest reports and Agent requests carry no authority.
- Every Run gets fresh identity-bound authority. No child, resume, cache follower,
  verifier consumer or trajectory fork reuses an old Broker, grant handle, Guest,
  workspace owner or physical-execution authority.
- Authority may remain equal or attenuate across delegation; it may never widen.
- No grant union across logical consumers. A shared producer must be authority-free under
  its exact admitted contract.
- No Guest/WASM memory, Python frame, FD, `/tmp`, socket, credential or continuation is
  shared as a consequence of logical result sharing.
- Ordinary Guest filesystem access remains Python stdlib through WASI. Do not add a
  `pysolate.fs` facade.
- Current workspace support is portable immutable lineage, not virtual Git. Do not add
  Git objects, refs, index, checkout, rebase or general merge.
- General three-way merge and multi-wait workflow scheduling remain deferred unless an
  exact campaign blocker proves they are required; such a blocker is a decision gate,
  not permission to implement them.
- Remote-read egress classification, if needed, is an extension of existing semantic and
  capability rules, not a new subsystem.
- Real external writes, credentials, messaging, publishing, payment and provider-side
  effects remain out of scope. No live effect-intent adapter is part of this goal.
- No live LLM/provider/Harness is required. Model fixtures must not be added merely to
  make the campaign look Agent-like.
- Lab is read-only and non-authoritative. It never schedules, retries, authorizes,
  reconciles, selects a workspace or drives replay.
- Unknown, ambiguous, late or orphaned work never becomes successful authority evidence.
- Fallback or backend reselection is permitted only when the Host proves both workspace
  and effects `not_started`; no post-effect replay or migration.
- Public/checked-in evidence is credential-free and source/body bounded. Private bodies
  remain ignored local artifacts.

## Current implementation baseline

At the prepared baseline:

- `runtime/subagent.Orchestrator` provides bounded fresh child execution, private
  workspace branches, explicit selection, cancellation and cleanup. Its descriptor binds
  `ChildPlanSHA256`, but the orchestration seam does not yet prove Plan attenuation or
  aggregate authority-budget conservation.
- `runtime/workflow` provides explicit single-wait fresh re-evaluation, observation
  refresh and transitive invalidation without Guest continuation. Suspended `State` is
  graph/root-bound but does not yet carry a complete Host authority envelope.
- `runtime/agentfunction` provides exact authority-free whole-function admission,
  retained reuse and retention-independent single-flight with logical-to-physical
  producer identity.
- `runtime/workspace` provides private mutable branches, immutable sealed roots, parent
  lineage, Capsule transfer/rebind, explicit selection and expected-base conflicts. Forks
  currently materialize directory copies; there is no virtual Git or general merge.
- `runtime/observe` records logical request to physical producer/consumer provenance.
- `research/workflowbench` contains the current 14-task balanced-order, CPU-aware real
  Guest experiment and is the preferred campaign substrate.
- `research/trajectory` and `apps/lab-web` provide an append-only private trajectory,
  exact model context/raw chunks where supplied, Runtime/workspace joins and a read-only
  multi-session inspector. Current real-Guest model events are explicitly scripted.
- The previous real-Guest workflow treatment reduced physical executions `25 -> 23` in
  one 14-task run but did not demonstrate CPU or wall-time improvement. That bounded
  negative/neutral result remains valid and must not be rewritten as a speedup.

## Desired future state

### Authority

- A child starts only with a Host-verified attenuation relation to its parent Plan/grant
  and a conserved reservation of bounded calls/resources.
- Suspended workflow state binds the authority epoch needed to decide which nodes remain
  reusable. Resume creates a fresh Run; authority-dependent nodes revalidate and only
  affected descendants invalidate.
- Multiple logical consumers may use one qualified physical producer result while each
  later runs with fresh, independent authority and workspace ownership.

### Execution campaign

- Exactly 20 checked-in, small, comprehensible Python programs form one versioned corpus.
- A canonical manifest declares their release offsets, dependencies, source/artifact,
  profile, input, authority/privacy/workspace identities, expected sharing eligibility,
  semantic oracle and prohibited inference.
- The same manifest runs under an all-off baseline and one qualified treatment with the
  same release order and physical-slot bound.
- A deterministic experiment driver releases work; it is not described or exported as a
  production scheduler.
- Real execution supplies queue/start/end/wait/resume timestamps, CPU accounting,
  physical execution identities, workspace roots and terminal outcomes.

### Lab and paper storytelling

- The existing Lab loads a “20-program Pysolate campaign” session without pretending it
  came from a model.
- One overview shows all releases and treatment identity; one time-scaled lane view shows
  logical and physical execution; one detail view explains every decision in plain,
  event-derived language.
- The same canonical evidence can deterministically produce:
  1. a compact 20-case contract table;
  2. an arrival-to-terminal timeline suitable for a paper figure;
  3. a logical-to-physical authority-bifurcation diagram or table.
- Paper figures use measured timestamps, readable direct labels and explicit claim
  boundaries. The Lab may retain its developer UI theme; paper SVGs remain plain,
  publication-oriented artifacts generated from canonical evidence.

## Track 0 frozen implementation map and preregistration

Live inspection at `df2d9e13b844d0fa39d9c0a7149d030ef758a8d0` confirms that the goal
can be expressed through existing packages. No forbidden virtual Git, live Harness,
effect plane, general workflow engine or production scheduler is required.

| Seam | Reuse | Refactor | Remove | Add |
|---|---|---|---|---|
| delegation attenuation | canonical `capability.Plan.Specs`, `Grants`, `MaxCalls`, `subagent.Config/Stage`, terminal cleanup | expose a Host-side exact equal-or-narrower Plan relation and bind decoded parent/child Plans at admission | no package; retire digest-only authority admission in updated call sites | typed rejection/reason and one bounded reservation ledger inside `subagent.Orchestrator` |
| authority-bound resume | `workflow.State`, graph identity, observation policy/freshness and transitive invalidation | bind a compact Host-authored authority envelope to `State` and node identity where authority is consumed | no continuation/checkpoint path | envelope validation, changed-authority invalidation and fresh-Run evidence |
| authority bifurcation | `agentfunction` single-flight/retention, `workspace.Root`, `subagent`, `observe` logical/physical identities | compose producer and consumers through immutable values/roots rather than add another executor | any duplicate fixture-only composition superseded by the campaign | focused composition/evidence tests only; no top-level Runtime package |
| 20-program campaign | replace/extend `research/workflowbench` v1 manifest, execution, evidence, trajectory projection and existing Lab | evolve the current 14-task fixture into the versioned 20-program contract and transparent release driver | delete superseded 14-task-only fixture/generator branches after equivalent evidence is regenerated | fixture sources, typed campaign decisions, bounded release/slot fields and paper projection |

The authority comparison remains exact and conservative: a child capability is accepted
only when its canonical `Spec` and grant-policy identity equal the parent's binding, the
child set is a subset, and its call reservation fits the Host-owned delegation budget.
There is no effect-class partial order, schema subsumption or grant interpretation.

The initial orchestration budget is deliberately separate from Broker call accounting:
the parent supplies one finite delegation ceiling, each admitted child atomically reserves
its sealed Plan `MaxCalls`, and terminal cleanup returns only the unused reservation that
the Host can prove. If real execution cannot report consumed calls without a second ledger,
the first bounded implementation conservatively consumes the full reservation. It must not
pretend that child Broker calls decrement an unrelated parent Broker counter.

Preregistered candidate claims:

1. exact child Plan subset plus bounded reservation can be enforced before child Guest
   startup without changing valid private-branch behavior;
2. fresh workflow resume can retain authority-free compute while rejecting or
   revalidating authority-dependent state after envelope change;
3. one exact authority-free physical producer or verifier may serve multiple logical
   consumers while grants, fresh Runs and workspaces remain disjoint;
4. one exact 20-program campaign can make every release, decision, physical execution and
   terminal state reconstructable from sealed evidence.

Preregistered invalid inferences remain those in [Metrics and claim discipline](#metrics-and-claim-discipline).
Primary measured outcomes are pre-start authority rejections, reservation conservation,
fresh Run/Guest identity, logical-to-physical counts, exact-root verifier counts, queue and
execution intervals, CPU/wall accounting, cleanup and semantic/workspace divergences. The
campaign treatment order is balanced from the frozen seed; the paper walkthrough is the
first fully valid balanced campaign, never a post-hoc fastest or prettiest run.

## Corpus contract: exactly 20 Python programs

The initial corpus is fixed by role, not by invented performance values. Each row is an
actual Python source with a deterministic output/workspace oracle.

### Family A — shared producer, authority-divergent consumers (`P01`–`P04`)

Four programs require one identical admitted authority-free producer result, then consume
it under distinct child Plan/workspace identities. The treatment may share only the
producer. Consumers must use fresh Runs and must not union grants.

### Family B — exact sharing and near-match rejection (`P05`–`P09`)

- one exact duplicate pair that qualifies for single-flight or retained reuse;
- one source/input near-match that must not share;
- one privacy-partition near-match that must not share;
- one authority/freshness near-match that must not share.

All five rows remain independently inspectable and each rejection has one primary
reason-code oracle.

### Family C — workspace identity and verifier reuse (`P10`–`P12`)

Two programs reach byte-identical immutable workspace roots through different logical
paths; one reaches a byte-distinct root. Exact root plus verifier/artifact/profile identity
may share one physical verification. No workspace merge, semantic-root equivalence or
trajectory-equivalence inference is allowed.

### Family D — authority-bound wait/resume (`P13`–`P16`)

Cover:

- same authority epoch resume;
- changed observation freshness;
- changed/revoked grant or Plan;
- expired/invalid authority envelope.

Only authority-free nodes may survive relevant authority changes. Invalid resume must
fail before a Guest receives stale authority.

### Family E — delegated child authority and terminal cleanup (`P17`–`P20`)

Cover:

- valid attenuated child Plan;
- capability/effect widening attempt;
- aggregate child reservation exceeding the parent budget;
- cancellation/late child work that must lose publication authority and clean its private
  branch.

Rejected cases remain first-class campaign rows. “No physical execution” is an expected,
visible outcome, not missing data.

## Canonical campaign vocabulary

The campaign evidence must make these states explicit where applicable:

```text
released
admission_started
admitted | rejected
queued
physical_started
producer_joined | retained_result_used | sharing_rejected
waiting
physical_released
resume_revalidated | resume_rejected
workspace_forked | workspace_sealed | workspace_selected | workspace_discarded
cancel_requested | late | orphaned
completed | failed | cancelled | rejected
```

Names may be adjusted to reuse an existing typed vocabulary, but the distinctions must
not be collapsed into free-form messages. Every event records a reason code and stable
logical/physical/Run/workspace/authority joins where relevant.

## Autonomous execution queue

### Track 0 — Live reset, KISS implementation map and claim preregistration

- [x] Inspect live Git state, recent commits, mechanism matrix, threat model, completed
  predecessor roadmap, current tests and evidence; trust live files over this baseline.
- [x] Write a concise implementation map for the four governing seams: delegation
  attenuation, authority-bound resume, authority bifurcation and 20-program campaign.
- [x] Identify code to reuse/refactor/remove/add. Reject duplicate registries, schedulers,
  evidence stores and viewers before implementation.
- [x] Freeze candidate claims, invalid inferences, metrics, corpus role allocation and
  treatment order before measurements.
- [x] Correct stale Current/Proposed/Deferred documentation as needed.

**Gate:** no implementation starts until the map proves the goal can be expressed mainly
through existing packages. If it requires a virtual Git, general scheduler, live Harness,
new effect plane or broad capability rewrite, stop for Yuzhe.

### Track A — Delegation attenuation and authority-budget conservation

- [x] Add RED tests showing child capability/effect widening and aggregate budget
  overcommit are currently not proven at the `subagent` orchestration seam.
- [x] Reuse the canonical decoded `capability.Plan` and grant identities; do not create a
  subagent-only capability model.
- [x] Make child admission prove equal-or-narrower capability/effect scope and atomically
  reserve bounded calls/resources from the parent.
- [x] Return only explicitly unconsumed reservations after terminal child cleanup. The
  conservative v0 report has no trusted unused count, so it returns zero.
- [x] Prove parent abort, child failure, duplicate child and cancellation cannot leak a
  reservation, branch or grant.
- [ ] Integrate one real Guest valid and adversarial child case in the canonical campaign;
  the current unit slice proves executor non-start before the later real-Guest fixture.

**Gate:** every widening/overcommit fails before child Guest startup; valid attenuation
preserves current fresh-child and private-workspace behavior.

**Architecture decision gate:** if the canonical Plan/grant types cannot express a
comparable attenuation relation without a broad authority-model redesign, land the RED
census/design evidence and stop before Track B.

### Track B — Authority-bound fresh workflow resume

- [x] Add RED tests proving suspended state must be bound to Plan/grant/privacy/authority
  epoch and cannot resume under stale or widened authority.
- [x] Add the smallest Host-authored authority envelope or digest to existing workflow
  state; do not retain Broker/Guest handles.
- [x] Reuse current dependency invalidation so authority-free compute remains reusable
  while authority-dependent observation and descendants revalidate.
- [x] Cover same epoch, freshness change, Plan/grant change, revocation/expiry, tamper,
  eviction and all-off resume behavior.
- [x] Prove every resume creates fresh Run and physical execution identity.
- [x] Integrate real Guest evidence for one valid and one rejected resume.

**Gate:** stale authority is never consumed; unrelated authority-free nodes are not
needlessly invalidated; no continuation state crosses the wait.

**Architecture decision gate:** if authority changes require a general multi-wait engine
or implicit continuation restore, record no-go and stop before Track C.

### Track C — Authority bifurcation and exact verifier sharing

- [x] Build the smallest composition test in which one admitted physical producer serves
  multiple logical consumers with different child Plan and workspace identities.
- [x] Prove consumers get fresh Runs, independent grants and independent private branches.
- [x] Demonstrate cancellation/failure of one consumer cannot cancel, publish for or
  widen siblings.
- [x] Bind exact verification reuse to immutable workspace root plus verifier source,
  artifact/toolchain, profile, environment contract, privacy and policy identities.
- [x] Share verification only for exact root identity; byte-distinct roots run separate
  physical verifiers.
- [x] Route all logical-to-physical relations through existing observation contracts.
- [x] Measure physical producer/verifier counts and overhead without claiming a speedup.

**Gate:** a shared physical producer never carries consumer authority; every consumer and
workspace remains separately attributable.

**No-go condition:** if useful sharing requires Python continuation, mutable Guest state,
grant union, semantic workspace matching or virtual Git, preserve the negative evidence
and continue to the campaign with only independently qualified mechanisms.

### Track D — Versioned 20-program campaign contract

- [x] Extend `research/workflowbench` models rather than creating another benchmark
  framework.
- [x] Materialize exactly 20 Python sources matching Families A–E.
- [x] Define canonical manifest validation for release offset, source/input/profile,
  Plan/grant/privacy/workspace identity, dependencies, oracle, expected admission and
  expected sharing/rejection class.
- [x] Reject duplicate IDs, non-monotonic or out-of-range releases, cycles, unknown
  dependencies, body/identity mismatch and missing oracle/prohibited-claim fields.
- [x] Generate fixtures deterministically and verify two generations byte-for-byte.
- [x] Add a readable corpus table documenting what each program tests and what it cannot
  prove.

**Gate:** exactly 20 comprehensible programs; every row has a deterministic semantic
oracle and at least one authority/lifecycle reason for inclusion. Do not inflate the
corpus to improve headline counts.

### Track E — Transparent bounded campaign execution

- [x] Implement or refactor an experiment-only release driver inside
  `research/workflowbench`; do not add a production Runtime scheduler.
- [x] Use fixed release offsets, fixed physical-slot limit, FIFO tie-breaking and explicit
  no-preemption unless an existing qualified mechanism states otherwise.
- [x] Run the same manifest under all-off baseline and one qualified treatment in balanced
  order.
- [ ] Record actual monotonic release/admission/queue/start/wait/resume/end intervals,
  process CPU, logical/physical counts, reason-coded decisions and terminal cleanup. The
  driver records release/admission/queue/start/end and exposes an event seam; real adapter
  wait/resume/workspace events remain pending.
- [x] Ensure zero-duration/rejected/no-physical rows remain visible.
- [ ] Add cancellation, failure, stale authority, cache corruption and mismatched-identity
  adversarial treatments without changing the 20 canonical source rows.
- [x] Validate evidence independently against the manifest and semantic/workspace oracle.

**Gate:** every program and physical execution can be reconstructed from the sealed
record; baseline/treatment outputs and allowed effects are equivalent; no event is
inferred from UI layout.

### Track F — Real Guest campaign and repetition policy

- [ ] Freeze exact source commit, Guest artifact/profile/import closure, host/resource
  bounds, manifest, seed and treatment order.
- [ ] Build/verify the real Guest from a clean source identity using the repository-owned
  workstation process. Do not build heavy artifacts on a login node.
- [ ] Run a bounded preregistered repetition set sufficient to detect unstable semantics
  and report dispersion; do not choose the prettiest timeline after measurement.
- [ ] Select the paper walkthrough by a predeclared rule, such as the first fully valid
  balanced campaign, while aggregate measurements include every valid repetition.
- [ ] Preserve private canonical evidence locally and produce a narrowly reviewed,
  credential-free Lab/paper projection.
- [ ] Record no-go/default-off outcomes when physical work, CPU, wall time or complexity
  do not improve.

**Gate:** real Guest equivalence, authority, cleanup and evidence-integrity gates pass.
One campaign may explain mechanism flow; it cannot establish representative performance.

### Track G — Lab campaign storytelling

- [ ] Extend the existing Lab data/loader path with the smallest truthful campaign
  projection; do not create a second viewer.
- [ ] Add a campaign overview with exactly 20 programs, release offsets, treatment,
  source family and terminal outcomes.
- [ ] Add a time-scaled multi-lane view for release, queue, physical execution, wait,
  resume and terminal events. Logical requests must remain visible when served by one
  producer.
- [ ] Show physical-slot occupancy and queue order without implying an unimplemented
  scheduler.
- [ ] For every sharing/rejection/resume decision, generate a deterministic explanation
  from typed event fields: what happened, at what time, which identities matched or
  differed, and which authority rule applied.
- [ ] Link program -> logical request -> physical execution -> authority/Plan -> workspace
  root -> terminal disposition in both directions.
- [ ] Support baseline/treatment comparison without placing incompatible cohorts on one
  ranking axis.
- [ ] Preserve raw event/manifest inspection, source filters, search, exact private/public
  boundaries and narrow-viewport usability.
- [ ] Visually inspect overview, congested intervals, rejected rows, shared producers,
  workspace branches and mobile/narrow layouts.

**Gate:** a reader can narrate the complete campaign from the Lab without reading source
code; all text is state-derived, all times are measured or explicitly declared release
offsets, and the UI has no execution authority.

### Track H — Paper-oriented artifacts and truthful closeout

- [ ] Generate one compact 20-case contract table from canonical manifest/evidence.
- [ ] Generate one publication-oriented arrival-to-terminal SVG from the preregistered
  walkthrough with direct labels, readable time units and explicit baseline/treatment
  scope.
- [ ] Generate one logical-to-physical authority-bifurcation figure or table only if the
  campaign produces the required evidence; otherwise report the no-go instead of drawing
  the target architecture as an observed result.
- [ ] Keep mechanism diagrams separate from measured figures and bind source symbols in a
  small manifest.
- [ ] Verify deterministic generation, SVG structure, paper-width readability and actual
  data domains; do not check in temporary raster QA unless requested.
- [ ] Write a short paper-facing narrative: setup, exact 20-case flow, one walkthrough,
  aggregate result, limitations and invalid inferences.
- [ ] Update architecture, threat model, mechanism matrix, research history and Lab docs
  with Current/Observed/Proposed/Deferred labels.
- [ ] Perform independent post-fix review of authority, lifecycle, evidence integrity,
  privacy and claim wording.
- [ ] Run full local gates, real Guest gate, credential/private-path scan, signed commits,
  push, verify signatures, `HEAD == @{u}` and clean worktree.

**Final claim gate:** report only exact qualified mechanisms and measured campaign facts.
Do not claim a general Agent scheduler, virtual Git, arbitrary-Python reuse, universal
multi-agent speedup, live-Harness behavior, exactly-once effects or production readiness.

## Campaign evidence requirements

At minimum, the canonical private record binds:

- campaign schema, source commit, seed, repetition and treatment;
- exact 20-program manifest and source/body digests;
- Guest artifact, runtime profile, imports, Plan, grant, privacy and policy identities;
- release offsets and actual monotonic timestamps;
- logical program/request, Run, physical execution, producer/consumer and decision IDs;
- physical-slot occupancy and queue reason;
- wait/resume authority-envelope relation;
- workspace parent/branch/root/verifier identities;
- status, reason code, cleanup/disposition and semantic oracle result;
- process CPU and wall intervals with explicit accounting method;
- export seal and private/public classification.

The portable projection must not expose credentials, Host paths, private repository bodies,
secret-bearing arguments, cookies, tokens or hidden chain-of-thought. Python sources may
be checked in only when deliberately authored as public, credential-free test fixtures.

## Evaluation questions

1. Does delegation attenuation reject every tested widening and budget-overcommit before
   physical child execution?
2. Does authority-bound resume preserve reusable pure nodes without consuming stale
   authority?
3. Can multiple logical consumers share one authority-free physical producer while
   keeping grants and workspace ownership disjoint?
4. Can exact root-equivalent verification reduce physical verifier executions without
   semantic or privacy mismatch?
5. Across the 20-program campaign, which requests execute, share, wait, reject or resume,
   and is every decision explainable from sealed evidence?
6. What physical work is reduced, what overhead is added, and where is there no measured
   CPU/wall benefit?
7. Which negative cases show that visually similar programs correctly do **not** share?

## Metrics and claim discipline

Report:

- exactly 20 logical programs and their terminal classification;
- logical requests versus physical executions;
- producer followers, retained hits and sharing rejections by reason;
- queue wait, execution, wait/release/resume and terminal intervals;
- fresh Run/Guest counts and cleanup;
- capability/authority rejection counts;
- workspace roots, exact-root verifier sharing and physical verifier counts;
- process CPU, wall time, evidence and storage overhead;
- semantic/workspace oracle divergences;
- evidence completeness and corruption/adversarial results.

Never infer:

- a performance gain from fewer physical executions alone;
- model/Agent quality from deterministic Python fixtures;
- representative workload behavior from 20 curated programs;
- exactly-once external effects;
- safety for arbitrary Python or unknown capabilities;
- scheduler optimality from FIFO campaign execution;
- semantic equality from matching prose or trajectories.

## Per-slice execution protocol

For each executable slice:

1. inspect the live governing code and tests;
2. write a RED test or explain why a design/evidence-only step cannot have one;
3. implement the smallest change, preferring refactor/composition over addition;
4. run focused tests and adversarial negatives;
5. inspect diff size and remove duplicate/stale paths;
6. update this roadmap's checkboxes, current pointer and completion log;
7. run risk-proportional global gates;
8. make a signed commit and push;
9. verify signature, upstream equality and clean status;
10. immediately continue to the next executable slice.

Use parallel workers only for bounded, independently valuable read-only audits or
file-disjoint implementation lanes. The controller owns architecture decisions, shared
contracts, integration, review, gates, signed commits and push. Never allow concurrent
writers in one worktree.

## Global gates

Run focused gates after each slice and the following before final closeout:

```bash
go test -race ./... -count=1
go vet ./...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
cd apps/lab-web && npm test -- --run && npm run build && npm run test:e2e
cd ../.. && git diff --check
scripts/track-f-gate.sh release-check
```

Also run the repository's real Guest build verification and Track F gate on the final
behavior commit. Prefer local and bounded workstation evidence over manually triggering
GitHub Actions; CI quota is limited and CI is not a substitute for the required local or
Linux evidence.

## Mandatory decision/stop conditions

Continue automatically after verified slices. Stop and ask Yuzhe only when:

1. a result would change the authority model or require a broad architectural rewrite
   before subsequent conceptual tracks;
2. child attenuation cannot be expressed by the canonical Plan/grant model;
3. resume correctness requires preserving Guest continuation or building a general
   workflow engine;
4. qualified sharing requires grant union, mutable-state sharing, semantic similarity,
   virtual Git or general merge;
5. the campaign requires a new production scheduler, live Harness or external-effect
   system rather than a bounded experiment driver;
6. the exact 20-program corpus cannot exercise the intended mechanisms with real semantic
   oracles;
7. repeated gates fail in a way requiring a product/resource/risk decision;
8. all executable work and final gates are complete.

A failed candidate mechanism is not automatically a blocker. Record the no-go and
continue through independent campaign, observability and paper-story tracks where their
claims remain valid.

## Current execution pointer

**Track E real adapter:** the architecture gate is resolved in favor of a strict boundary:
Pysolate Runtime remains general, while the 20 programs are a declarative research fixture.
Manifest v2 now carries a small typed `execution` union for producer/consumer, exact
request, exact verifier, fresh workflow resume, delegation and cancellation operations.
The adapter must compose existing Runtime APIs from those typed fields. It must contain no
dispatch on `Pxx`, family or `Expected`; expected values remain independent oracles only.

## Completion log

- 2026-08-15: Goal formed from the completed trajectory/Lab reset, workflow-boundary v1
  evidence, workspace/subagent/workflow archaeology and Yuzhe's KISS/paper-story
  direction. No implementation was started in the authoring session.
- 2026-08-15: Track 0 inspected the live capability/subagent/workflow/workflowbench seams,
  froze the reuse/refactor/remove/add map, exact-comparison and conservative-reservation
  semantics, candidate claims, invalid inferences, metrics, corpus roles and balanced
  walkthrough selection. The KISS gate passed without a forbidden subsystem.
- 2026-08-15: Track A added exact canonical child Plan/grant attenuation and an optional
  authority-aware `subagent.Orchestrator` admission path. It reserves the full child call
  ceiling conservatively, rejects unknown/widened Plans and sibling overcommit before
  workspace fork or executor start, and proves cancellation discards private refs and
  blocks late children. Historical digest-only fixtures remain available but cannot be
  cited as authority proof; the campaign must use the authority-aware path.
- 2026-08-15: Track B bumped explicit workflow state to v2 and bound it to a Host-authored
  Plan/grant/privacy/epoch/expiry envelope. Resume rejects expired, revoked and
  cross-privacy authority before Guest creation; valid authority changes invalidate only
  observations and descendants, retain independent compute, and mint a fresh physical
  execution identity. Real-Guest integration covers both accepted and revoked resume.
- 2026-08-15: Track C composed existing single-flight, authority-aware subagent and
  immutable-workspace mechanisms without a new sharing subsystem. A real-Guest run from
  clean source `342fd06fc31c1dd4d977ff2c68b60960a48cee33` passed: two logical producer
  requests mapped to one physical producer; two independently planned child Guests used
  private branches; exact verifier requests shared one physical verifier; a byte-distinct
  root ran another. This is correctness/attribution evidence, not a speedup claim.
- 2026-08-15: Track D added the canonical, byte-deterministic 20-program manifest inside
  `research/workflowbench`, actual Python sources, canonical Plan/grant identities,
  release/dependency/oracle/admission controls, exact and near-match pairs, per-family
  prohibited claims, syntax checks and the readable v1 corpus protocol.
- 2026-08-15: Track E core added an experiment-only release driver, a FIFO three-slot
  physical gate used only when adapters report actual physical starts, monotonic logical
  and physical events, process CPU/wall accounting, visible rejected rows, balanced
  treatment order, a sealed evidence schema and an independent validator. Fixture runs
  reconstruct 20 rows and show the qualified exact pair reducing physical starts `16 →
  15`; this is driver validation only, not real-Guest campaign evidence.
- 2026-08-15: Real-adapter design reached mandatory stop condition 6. The frozen v1 rows
  say what should happen but omit typed executable producer/verifier/resume/delegation
  contracts. Continuing would require hidden per-ID behavior and fabricated causal events.
  No such adapter was added; choose typed manifest v2 (recommended) or narrow the unified
  campaign claims before Track E/F resumes.
- 2026-08-15: Yuzhe resolved the gate: Pysolate itself stays general and the 20 programs
  remain demonstration fixtures. Manifest v2 adds only a research-local tagged execution
  contract; no `runtime/` package, production scheduler or case-specific Runtime behavior
  was added. Validation rejects unknown operations and incomplete producer, verifier,
  resume and delegation contracts.
- 2026-08-15: Hardened the adapter boundary: `CampaignRequest` deliberately omits program
  ID, family and `Expected`; dependency results and typed execution contracts are copied
  into the request. Rejected dispositions now come from adapter/Runtime admission, not
  from the oracle. Delegation contracts bind the canonical parent Plan identity.
- 2026-08-15: Added the first real-Guest adapter substrate without changing `runtime/`:
  the research fixture can materialize canonical Plans by identity, `CampaignGuestExecutor`
  validates source/input/Plan/grant bindings before creating a fresh Wazero Guest, and typed
  workflow operations carry an opaque state key rather than requiring adapter dispatch on
  `P13`. The real workstation Guest executed the first canonical program successfully using
  artifact `sha256:0a37a963a09b4e763cb6a40886a771e9c13e2f6a9d3a2d295788752e319c5795`
  built from source commit `ae922641cd9c539b68a0ea7110b5dc205e5c9a8a`. This is a
  correctness checkpoint, not a campaign or performance result.
- 2026-08-15: Added the research-only `RuntimeCampaignAdapter`, composed entirely from
  existing generic APIs: `agentfunction` exact sharing, private `workspace` branches and
  sealed-root verification, authority-bound `workflow` resume, and bounded `subagent`
  staging. A full qualified P01-P20 run against the real workstation Guest passed with 20
  logical rows and 17 recorded physical executions. P05/P06 shared one physical Guest,
  P10/P11 shared exact-root verification, and expired/widened/over-budget/late requests
  produced no physical execution. This is correctness evidence only; balanced repetitions
  are still required before any performance claim. A private evidence CLI records each
  baseline/qualified run with artifact and campaign source attribution.
- 2026-08-15: Independent adversarial review found that a forged delegation parent digest
  could remain well-formed while no longer matching its typed parent role. The research
  manifest and adapter now both bind role → canonical parent Plan and bind
  `child_reserved_calls` to the child Plan's actual reservation before staging. The private
  run summary was upgraded to v2 with manifest/evidence hashes and host/kernel/Go
  provenance; the earlier three-pair v1 run is exploratory and is not publication evidence.
- 2026-08-15: The publication gate caught a second research-layer issue before any public
  projection: pretty-printing JSON rewrote `json.RawMessage` whitespace, so reloaded
  manifests/evidence failed canonical-byte validation. The private writer now emits compact
  canonical JSON, strict duplicate-key/unknown-field decoders are tested, and the affected
  five-pair v2 run was discarded. The deterministic projector validates every private file
  and its bound SHA before deriving the Lab fixture, report or SVG.

## Short prompt to start this Mega-Goal

```text
Read docs/plans/2026-08-15-authority-bound-multi-agent-transparent-campaign-megagoal.md fully, then execute it in ~/projects/agent-python-runtime. Start at Track 0 and continue across verified slices; update the roadmap, use TDD, refactor before adding abstractions, signed commit and push after each coherent slice, and stop only at its explicit architecture/permission/risk decision gates or final completion. Do not build a live Harness, virtual Git, general scheduler or external-effect plane.
```
