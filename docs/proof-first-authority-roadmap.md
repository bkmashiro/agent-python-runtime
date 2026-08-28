# Pysolate composable mechanism roadmap

Status: **Long-term mechanism inventory; active execution is governed by the approved Semantic Execution Experimental Megagoal.**
Date: 2026-08-14

This roadmap replaces the earlier linear effect-first ordering and remains the
long-term mechanism inventory. The active executable subset, exploration/deferral
rules, gates, and stop conditions now live in
[the Unified Effect-Aware Runtime Megagoal](plans/2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md).
The completed Semantic Execution Experimental Megagoal remains the verified AST,
whole-Run reuse and cold-continuation foundation; the completed Full Composable
Runtime Megagoal remains historical implementation evidence. This inventory preserves the authority-lifecycle correctness direction
while adding bounded AST semantic planning, whole-function reuse, and
continuation-preserving cold-I/O experiments as explicitly Experimental successor
work.

The 2026-08-14 Track B contract supersedes the inventory's old
`SpeculativeSafe`/literal eager-preflight admission rule. That legacy dispatch path is
disabled; capability-plan v5 metadata alone cannot start work. Future pre-dispatch
requires the verified overlay and legality gates in the active roadmap. See
[the canonical v0 contract](research/effect-aware-contract-v0.md).

Related decisions:

- [product direction](product-direction.md)
- [authority-lifecycle positioning](authority-lifecycle-positioning.md)
- [content-addressed Agent Functions](content-addressed-agent-functions.md)
- [streaming authority-staged execution](streaming-authority-staged-execution.md)
- [active Logical-Time-Preserving PLM megagoal](plans/2026-08-28-logical-time-preserving-plm-autonomous-megagoal.md)
- [completed Semantic Execution Experimental Megagoal](plans/2026-08-14-semantic-execution-autonomous-megagoal.md)
- [completed Full Composable Runtime megagoal](megagoal-full-composable-agent-runtime.md)
- [completed streaming execution megagoal](megagoal-streaming-authority-staged-execution.md)
- [Cloudflare comparison](research/cloudflare-code-mode-comparison.md)

## Product objective

Pysolate should remain useful as a minimal fresh Python runtime, while optional
mechanisms compose when a Harness needs stronger state, reuse, effect, evidence,
or density behavior:

```text
stable minimal Run contract
├─ optional sealed-source PLM preparation and original-point linearization
├─ one final-Guest lowering path and one synchronous Guest execution
├─ optional streamed subagent fan-out
├─ optional immutable workspace roots / branches
├─ optional local content-addressed result reuse
├─ optional workflow re-evaluation at explicit I/O boundaries
├─ optional private workspace attempts and publication
├─ optional qualified external-write lifecycle
├─ optional playback and independent verification
├─ optional prepared-runtime / memory-COW acceleration
└─ optional Lab projection and second-backend conformance
```

The roadmap succeeds only if disabling an optional mechanism restores a clear,
tested fallback rather than changing unrelated semantics or making the Runtime
unusable.

## Always-on substrate versus optional mechanisms

### Always-on semantic substrate

These are Pysolate's baseline contract, not feature flags:

- every Run receives a fresh Guest instance and retires it after execution;
- no ambient shell, subprocess, network, credential, package installer, or Host
  filesystem authority;
- ordinary Python and admitted mounted filesystem operations remain ordinary
  Python/WASI operations;
- authority-bearing external operations cross the typed Host Broker;
- artifact/profile, source/input, limits, workspace binding, and authority plan
  are Host-selected before Guest startup;
- `/tmp` is per-Run scratch and never continuation state;
- interpreter state, workspace state, authority, external effects, and evidence
  have independent identities and dispositions;
- truthful fallback modes are named explicitly; an optimization miss or disabled
  mechanism never silently broadens authority.

### Optional mechanism registry

| Mechanism | Initial toggle concept | May operate without | Hard dependency | Required fallback when off |
|---|---|---|---|---|
| Incremental source validation | `incremental_validation=off|on` | every execution optimization | append-only source stream; exact target-Guest parser contract | buffer and validate complete source |
| Streaming local execution | `streaming_execution=off|local` | tools, cache, memory COW, subagent fan-out | incremental validation; private unpublished state | ordinary complete-source fresh Run |
| Eager speculative reads | per-capability `speculative_read=off|eager` | result cache, memory COW, external writes | Host-qualified `read_only + idempotent + speculative_safe`; canonical closed arguments; speculation budget | dispatch only when normally reached after admission |
| Streamed subagent fan-out | `streamed_fanout=off|staged` | cache, external writes, memory COW | closed child descriptor; private child state; publication barrier | launch children after parent plan seals |
| Persistent workspace | `workspace.mode=none|direct` | cache, playback, effects, COW | none | no `/workspace`; structured input/output only |
| Immutable workspace roots / branches | `workspace.versioning=off|immutable` | result cache, memory COW, effects | persistent workspace | direct rooted workspace |
| Private workspace attempt | `workspace.write_mode=direct|attempt` | result cache, playback, memory COW | persistent workspace | current explicit direct-write semantics |
| Local result cache | `function_cache=off|local` | memory COW, attempts, effects, playback | canonical invocation identity; immutable declared inputs | execute a fresh Guest normally |
| Concurrent single-flight | `singleflight=off|on` | durable result retention, memory COW, attempts | canonical invocation identity | execute each request independently |
| Workflow re-evaluation | `workflow_resume=off|reevaluate` | memory COW, external writes, Lab | explicit workflow nodes; immutable completed outputs; local lookup | Harness starts a new ordinary Run from explicit state |
| Live-read capture | per-capability `capture=off|on` | result cache, workspace attempts | typed capability and protected body storage | dispatch current live read normally |
| Playback | `playback=off|strict` | result cache, memory COW, attempts | matching captured capability results and frozen identities | live execution or explicit refusal |
| External-write lifecycle | per-capability `effect_mode=deny|qualified` | cache, playback, immutable branches, memory COW | typed qualified adapter | write denied; read/local compute unaffected |
| Approval/compensation | per-adapter policies | cache, memory COW, workflow re-evaluation | qualified external-write lifecycle | deny or explicitly configured direct policy; never fake rollback |
| Prepared runtime | `prepared_runtime=off|pool` | every semantic mechanism | compatible backend/artifact | ordinary fresh instantiation |
| Memory COW | `memory_cow=off|local` | workspace COW, cache, workflow re-evaluation | prepared runtime and qualified platform | private fresh memory per instance |
| Independent verifier | `verification=off|record` | cache, COW, Lab | versioned records for claims being verified | bounded ordinary receipts/response |
| Lab projection | `lab=off|recorded` | all performance mechanisms | Runtime records for displayed fields | no Lab artifact |
| Second backend | Host backend selection | cache, COW, Lab | shared baseline contract | Wazero remains supported path |

These are conceptual configuration seams. Exact CLI/config fields require an ADR
and tests before becoming public API.

## Composition rules

### Rule 1: optimizations cannot change semantic identity

For the same admitted invocation, enabling prepared runtime, memory COW,
single-flight, or a cache hit must not change:

- structured result and output schema;
- derived filesystem root when one exists;
- capability/effect behavior;
- authority plan;
- failure classification, except for explicitly reported cache/optimizer
  evidence;
- privacy partition.

### Rule 2: storage mechanisms do not imply execution mechanisms

Immutable workspace roots and branch lineage are portable storage semantics.
They must not require Linux memory COW, a warm worker, a live Guest, or a pinned
Sandbox. Memory COW is a local acceleration only.

### Rule 3: caching does not imply effects or playback

A cacheable function has no live Host call inside its boundary. Result caching
must not synthesize a capability receipt or claim that a live read occurred.
Live-read capture/playback is a separate capability mechanism.

### Rule 4: playback does not imply memoization

Strict playback may re-execute the complete fresh Guest with captured capability
results while function caching is disabled. Conversely, a cacheable function may
use only immutable input roots and require no capability tape.

### Rule 5: attempts do not imply content-addressed functions

A normal non-cacheable Run may execute inside a private workspace attempt. A
cacheable function may instead emit an immutable derived-output root without
publishing it to a durable workspace.

### Rule 6: effects terminate at explicit boundaries

External-write lifecycle is optional because writes may remain denied. When a
qualified write adapter is enabled, no cache, replay, workflow resume, or COW
optimization may suppress, duplicate, or reinterpret its dispatch.

### Rule 7: disabled means absent, not emulated

- cache off means execute; it does not use an undocumented cache;
- playback off means live call or refusal; it does not return stale capture;
- COW off means private allocation; it does not weaken isolation;
- attempts off means documented direct write; it does not claim rollback;
- verifier off means no independent-verification claim;
- Lab off means no projection side effects.

### Rule 8: eager read speculation is explicit authority

A closed call may dispatch before control-flow reach only when the Host adapter
qualifies it as `read_only + idempotent + speculative_safe` and a separate
speculation budget admits it. Other calls require confirmed dynamic reach;
writes additionally require the final source/authority/approval barrier. An
invalid or abandoned program may waste eager read resources, but may not publish
filesystem state or dispatch a write.

## Track S — streaming authority-staged execution

**Promise:** overlap model generation with exact incremental admission, local
Python execution, selected real reads, and recursive subagent launch without
allowing incomplete programs to publish files or dispatch writes.

- [x] freeze append-only stream, import-preamble, complete-suite, and final-seal
  contracts using the exact target Guest compiler;
- [x] execute closed suites in one private module namespace over an immutable
  input root, private filesystem overlay, and `/tmp`;
- [x] discard Guest, overlay, outputs, and unpublished state when the final
  source is invalid or abandoned;
- [x] historical proof admitted eager preflight only for Host-qualified
  `read_only + idempotent + speculative_safe` calls with canonical immediate
  arguments and an independent budget; that path is now disabled and superseded by
  capability-plan v5 plus the future verified overlay;
- [x] require actual dynamic reach for all other allowed calls, and keep writes
  behind final source/authority/approval seal;
- [ ] bind staged read results to source range, dynamic occurrence, canonical
  arguments, adapter/grant/policy, freshness, and privacy partition so formal
  continuation cannot dispatch twice;
- [x] count unused eager requests as orphaned speculation rather than hiding
  their quota, cost, access-log, or privacy consequences;
- [ ] pipeline one structured parent stream into two staged child Agents, each
  with a private filesystem branch and no publication/write authority;
- [ ] compare complete-source baseline, validation-only, local streaming,
  eager-read streaming, invalid suffix, and parent-invalid fan-out treatments;
- [ ] measure overlap, end-of-source-to-result latency, invalid/abandoned waste,
  read cost, overlay bytes, and fan-out critical path.

Definition of Done:

- valid supported programs match ordinary complete-source semantics;
- eager-qualified unreachable calls may dispatch once and are reported as
  orphaned waste; unreachable non-qualified calls never dispatch;
- a reached or preflighted read is never duplicated by final execution;
- invalid suffixes and invalid parent plans publish no files/results and dispatch
  no writes;
- streaming, eager reads, fan-out, prepared runtime, memory COW, and cache each
  retain an independent off-state.

Stop/reframe if useful calls rarely close early, generation/read overlap is
small, invalid-source waste is excessive, provider policy excludes most reads,
or supported Python semantics become unnaturally narrow.

## Track M — authority-bound multi-Agent delegation

The bounded authority-aware subagent path compares a child Plan with the
Host-held parent Plan before workspace fork or executor start. Only exact
canonical Spec and grant identities may be retained, the child call ceiling may
not exceed the parent's, and sibling reservations may not exceed an explicit
delegation ceiling. Unknown Plans, widened Specs or grants, and aggregate
overcommit fail closed. Because no trusted child-consumption report exists, the
first slice conservatively consumes the full admitted reservation; it does not
pretend to return unused calls.

This extends `capability.Plan` and `subagent`, not a second policy registry. The
existing digest-only mode remains for historical fixtures; new authority claims
and the transparent campaign must use the authority-aware configuration and may
not cite digest-only admission as proof.

Fresh workflow state v2 also binds Plan, grant set, privacy partition, authority
epoch and expiry. Resume never restores Guest/Broker state: it creates a fresh
physical execution identity. Expired or revoked current authority and
cross-privacy resume fail before Guest creation. A valid Plan, grant, epoch or
expiry change conservatively invalidates every observation and its descendants
while retaining independent compute; unchanged authority still follows the
existing per-observation freshness policy.

## Recommended product profiles

Profiles are test fixtures and examples, not mandatory bundles.

### Minimal fresh execution

```text
workspace optional/direct
cache off
playback off
external writes denied
prepared runtime off
Lab off
```

Purpose: preserve the smallest understandable Pysolate contract.

### Dense local compute

```text
cache off or local
single-flight optional
prepared runtime pool
memory COW when supported
external writes denied
```

Purpose: measure many short fresh Python Runs without requiring workflow or
effect machinery.

### Local content-addressed functions

```text
immutable declared inputs
local private cache
single-flight on
prepared runtime optional
memory COW optional
external writes denied inside functions
```

Purpose: eliminate repeated pure compute on one Host.

### React-like workflow re-evaluation

```text
explicit workflow nodes
local lookup of completed cacheable nodes
live I/O only at typed boundaries
Guest destroyed while waiting
fresh Guest re-evaluates to next live boundary
```

Purpose: release instances during waits without interpreter checkpointing.

The cost and correctness boundary...[truncated]

### Strict playback verification

```text
function cache off
strict captured read tape
fresh full Guest re-execution
matching frozen artifact/input/workspace identities
```

Purpose: prove playback remains independent from memoization.

### Qualified commit workflow

```text
cache optional for prior pure nodes
workspace attempt optional but recommended
qualified external-write adapter
operation/attempt journal
approval/reconciliation as adapter policy requires
```

Purpose: keep eventual external commit truthful without making writes a
prerequisite for local compute.

## Track A — baseline and orthogonality harness

**Promise:** every optional mechanism has an explicit fallback and combination
tests prevent hidden coupling.

- [x] ordinary Python filesystem API; no `pysolate.fs` facade;
- [x] fresh Guest and typed Host authority baseline documented;
- [x] Cloudflare comparison and Current/Proposed reset;
- [x] mechanism registry and composition rules in this roadmap;
- [x] define an internal feature-set/config object without committing public CLI
  names;
- [x] create pairwise configuration tests for each mechanism and its nearest
  dependency/fallback;
- [x] create negative tests proving optimizations cannot widen Broker authority;
- [x] expose selected mode names in Host evidence so behavior is explainable.

Definition of Done:

- Minimal fresh execution passes with every optional mechanism disabled;
- each optional mechanism passes alone with only its declared hard dependencies;
- invalid combinations fail before Guest startup with a precise error;
- disabling an optimization does not change result/effect semantics.

## Track B — local content-addressed Agent Functions

**Promise:** selected explicit Python computations can be safely reused on one
Host without requiring memory COW, workflow resume, playback, workspace
transactions, or external-write support.

- [x] freeze binary `cacheable | not_cacheable` admission contract;
- [x] define canonical invocation identity over source/function, artifact/profile,
  admitted import closure, structured inputs, immutable filesystem roots,
  deterministic settings, output schema, privacy partition, and policy epoch;
- [x] implement whole-Run local private cache behind an internal toggle;
- [x] implement concurrent single-flight separately from durable retention;
- [x] fail closed if a cacheable invocation attempts a Host call, undeclared
  filesystem read, shared write, clock/random access, or dynamic import;
- [x] support eviction followed by safe recomputation;
- [x] measure cold execution, cache lookup/materialization, single-flight, storage
  amplification, and break-even points.

Bounded closure evidence: the exact target-Guest AST path and opaque Host-minted
whole-Run qualification are implemented Experimental/default-off; see
[content-addressed-agent-functions.md](content-addressed-agent-functions.md) and
[evidence/semantic-reuse-observation.json](evidence/semantic-reuse-observation.json).
Arbitrary Guest Python remains `not_cacheable`.

Definition of Done:

- cache off and cache on return identical semantic outputs;
- concurrent identical requests execute once only when single-flight is enabled;
- disabling retention while keeping single-flight works;
- private partitions do not share lookup existence or results;
- no external effect is skipped or fabricated by a cache hit.

Stop/reframe if lookup/materialization dominates bounded workloads, conservative
admission excludes nearly all useful work, or repeated pure computation is rare.

## Track C — explicit workflow re-evaluation

**Promise:** a waiting workflow may release its Guest and later reach the next
live I/O boundary through cached explicit nodes, without restoring Python state.

Hard dependencies: explicit Harness workflow node identity and immutable
completed outputs. Initial implementation uses Track B local lookup. It has no
dependency on prepared runtime, memory COW, external writes, Lab, or Python frame
checkpointing.

- [ ] define a synchronous workflow skeleton with explicit compute and I/O nodes;
- [ ] persist node identities, dependency edges, immutable outputs, observation
  identities, and filesystem roots—not locals, frames, heap, FDs, `/tmp`, or
  WASM memory;
- [ ] destroy Guest at one wait/I/O boundary;
- [ ] create a fresh Guest and re-evaluate unchanged nodes by local lookup;
- [ ] execute the next live read under current freshness/policy;
- [ ] prove a changed observation invalidates only transitive downstream nodes;
- [ ] prove workflow resume off falls back to a normal Harness-directed Run;
- [ ] measure retained state size, re-evaluation latency, and instance-time
  released during waits.

Definition of Done:

- no Guest or Sandbox identity survives the wait;
- no Python hidden state is required to continue;
- unchanged nodes hit, changed descendants recompute, unrelated nodes remain
  reusable;
- cache eviction causes correct recomputation rather than failed continuation.

## Track D — immutable workspace roots, branches, and attempts

**Promise:** filesystem state can move, branch, compare, and optionally publish
without depending on a live execution instance.

- [ ] define immutable root and parent lineage identities over current
  manifest/Capsule substrate;
- [ ] implement child derived roots/deltas without requiring Linux COW;
- [ ] prove recursive branch-of-branch lineage across fresh Runs;
- [ ] implement explicit compare/select and bounded three-way merge semantics;
- [ ] separately implement optional private attempt publication with expected-base
  conflict detection;
- [ ] retain current direct-write mode as an explicit fallback during migration;
- [ ] measure changed bytes, materialization cost, branch depth, merge cost, and
  garbage collection reachability.

Definition of Done:

- immutable branch semantics pass with memory COW off and cache off;
- attempts pass for non-cacheable Runs;
- `/tmp` never becomes a workspace root implicitly;
- no Host path or authority-bearing reference enters portable state.

## Track E — typed effect truth and late commit

**Promise:** external writes remain denied unless a qualified adapter explicitly
models dispatch uncertainty and recovery; this track is optional for read/local
compute deployments.

- [ ] freeze minimal effect classes and deny unknown writes;
- [ ] define immutable `EffectIntent` and separate logical operation from physical
  dispatch attempt;
- [ ] journal before dispatch in an in-process deterministic fake provider;
- [ ] inject accepted-but-response-lost and record `ambiguous` without blind retry;
- [ ] reconcile through stable provider readback identity;
- [ ] bind optional user approval to exact intent/policy/target/expiry;
- [ ] model compensation as a new forward attempt, never rollback;
- [ ] prove all Track B/C/D optimizations stop before external dispatch.

Definition of Done:

- with Track E disabled, writes are denied while reads and local compute work;
- accepted-but-response-lost produces one provider mutation and no automatic
  second dispatch;
- cache/playback/workflow re-evaluation cannot suppress or duplicate dispatch;
- workspace disposition remains independent from effect disposition.

## Track F — worker-local density and online optimization

**Promise:** local performance mechanisms accelerate existing semantics but are
removable without correctness changes.

- [ ] restore a bounded prepared-runtime baseline behind capability detection;
- [ ] restore or reimplement Linux memory COW as an optional prepared-runtime
  strategy;
- [ ] preserve ordinary fresh instantiation as the control and fallback;
- [ ] benchmark startup, peak/private/shared RSS, refill pressure, and completion
  rate for short Runs;
- [ ] collect bounded local function frequency, concurrency, compute time,
  input/output size, startup, and materialization statistics;
- [ ] add measured retention/eviction decisions;
- [ ] spike explicit-node fusion only after Track B establishes cache economics;
- [ ] keep splitting, specialization, and arbitrary hot-region extraction behind
  separate later gates.

Definition of Done:

- strategy off/on produces identical semantic outputs and authority behavior;
- unsupported platforms skip with a precise reason and use the control path;
- reported density gains include shared/private memory rather than misleading
  aggregate RSS;
- no optimizer decision crosses privacy partitions or live-effect boundaries.

## Track G — playback, evidence, and independent verification

**Promise:** evidence mechanisms can verify selected claims without becoming a
prerequisite for basic execution or caching.

- [x] scoped capture/playback exists for two curated external reads;
- [x] bounded Runtime observation and private Lab substrate exist;
- [ ] prove strict playback with function cache disabled;
- [ ] record cache mode/key/result relations without exposing private bodies or
  cross-partition hit existence;
- [ ] define versioned records for whichever D/E mechanisms are actually built;
- [ ] build an independent verifier for artifact/profile, authority plan, initial
  state, capability tape/effect attempts, output roots, and terminal disposition;
- [ ] add negative mutation/corruption fixtures;
- [ ] update Lab only with fields captured by the Runtime.

Definition of Done:

- basic Run works with verifier and Lab disabled;
- playback does not depend on function memoization;
- cache evidence does not claim code execution on a hit;
- verifier rejects identity substitution and cross-plane contradictions.

## Track H — backend-neutral conformance

**Promise:** the stable Run/authority contract can survive a deliberately small
second executor without forcing every optional optimization onto that backend.

- [ ] select one bounded second executor only after A and at least one of B/D/E
  has a frozen contract worth comparing;
- [ ] run shared tests for artifact/profile admission, capability plans, denial,
  stale authority, and terminal dispositions;
- [ ] mark prepared runtime, memory COW, cache, or filesystem strategy as backend
  capabilities rather than universal assumptions;
- [ ] reject adapters that require ambient facilities bypassing the Broker.

Definition of Done:

- both backends pass baseline conformance;
- an optional mechanism may be unsupported with explicit capability reporting;
- lack of an optimization never changes authority semantics.

## Recommended execution order

This order minimizes irreversible architecture while preserving independent
tracks:

```text
A: orthogonality harness and explicit fallbacks
→ S1: incremental target-Guest validation, no execution
→ S2: streaming local execution with all tools denied and private FS overlay
→ S3: eager qualified reads plus reach-gated controls
→ S4: staged two-child streamed fan-out
→ F: prepared runtime and memory COW measured against the streaming workload
```

Tracks B/C remain optional compute-reuse and wait-re-evaluation experiments, not
the critical path. Track D provides the portable immutable roots/private overlay
needed by S2/S4 but need not implement general merge first. Tracks E and G
proceed only far enough to enforce and evidence the streaming read/write
barriers. Track H waits until a stable contract has evidence. Lab work follows
Runtime records.

The first combined north-star proof should enable only:

```text
baseline fresh Guest
+ append-only source stream and exact incremental validation
+ private unpublished filesystem overlay
+ streaming local execution
+ one eager Host-qualified speculative-safe read
+ one non-qualified reach-gated control
+ final write/publication barrier
```

Then rerun the same workload independently with:

```text
eager speculative reads off/on
streamed fan-out off/on
prepared runtime off/on
memory COW off/on
local content-addressed cache off/on (optional secondary treatment)
```

This factorial comparison is more valuable than one everything-enabled demo: it
shows which mechanism creates which benefit and catches accidental coupling.

## Global gates

Every implementation slice requires focused tests. Before code commits:

```text
go test ./... -count=1
go test -race on changed stateful packages
go vet ./...
python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s tests -p 'test_*.py'
git diff --check
```

Real Guest behavior changes additionally require a rebuilt Linux x86_64 artifact
and the relevant real-Guest acceptance workflow. GitHub CI is not a default gate.

Performance claims require a control path with the mechanism disabled, structured
machine-readable output, repeated trials, and separate correctness gates.

## Decision gates

After the Track B/C proof, decide independently:

- retain local content-addressed functions if reuse/single-flight/re-evaluation
  materially reduces work or instance-time;
- retain memory COW only if it adds density beyond ordinary compiled-module reuse;
- retain immutable workspace branching if branch/transfer economics beat direct
  copies/worktrees for representative Agent tasks;
- implement broader external-write semantics only for a qualified target workflow;
- keep each unsuccessful mechanism disabled or research-only without discarding
  the successful baseline and other tracks.

No track is automatically promoted to default. Proof, compatibility, and a
working off-switch precede default enablement.
