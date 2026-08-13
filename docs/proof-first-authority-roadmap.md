# Proof-first authority-lifecycle roadmap

Status: **Active planning roadmap; implementation not started by this document.**
Date: 2026-08-13

This roadmap supersedes the post-demo phase ordering in
[product-maturity-and-roadmap.md](product-maturity-and-roadmap.md). That file's
source-pinned capability assessment remains historical context. Current Runtime
contracts remain governed by [product-direction.md](product-direction.md); the
new positioning decision is [authority-lifecycle-positioning.md](authority-lifecycle-positioning.md).

## Goal

Demonstrate one falsifiable, end-to-end property that adjacent code-mode
products do not make unique merely by offering sandboxed code, a call ledger,
approval replay, or compensation:

> One Agent program executes with immutable identity-bound authority, private
> attempt state, explicit external-effect uncertainty, and a Host-verified
> terminal disposition across independent state planes.

This remains a correctness foundation, not the only candidate differentiator.
The complementary feature hypothesis is that explicit authority and effect
boundaries can make selected Agent computations safely reusable across fresh
Runs on one Host. See
[content-addressed-agent-functions.md](content-addressed-agent-functions.md).

## Revised phase ordering

Do not treat the numbered effect phases below as an automatic implementation
queue. Before broad effect-plane work, test a smaller composition:

1. define a binary `cacheable | not_cacheable` whole-Run/function contract;
2. implement one-Host private memoization and concurrent single-flight;
3. split one synchronous workflow into live I/O and explicit cacheable nodes;
4. destroy the Guest at a wait/I/O boundary, then use a fresh Guest to
   re-evaluate through unchanged cached nodes;
5. change one live observation and prove that only downstream nodes recompute;
6. measure cold/warm, cache materialization, prepared-runtime, and optional
   memory-COW costs;
7. retain private workspace attempts and detailed effect operation modeling as
   the later commit/effect correctness path, not as the sole product identity.

Initial scope is one trusted Host. No cross-machine synchronization,
cross-tenant result sharing, arbitrary-region JIT, or Python heap checkpoint is
required. "JIT" initially means measured online retention, single-flight,
explicit-node fusion, and prepared-profile placement.

## Value filter

Proceed only with slices that upgrade at least one of:

- **Safety:** less ambient or accidental authority;
- **Truth:** uncertainty and terminal state are represented honestly;
- **Verification:** an independent checker can reject tampering or drift;
- **Portability:** the authority contract survives a backend change;
- **Product value:** the target workflow becomes easier to review or recover.

Do not add capabilities, UI, storage machinery, or abstractions solely to appear
feature-complete against Cloudflare.

## Frozen boundaries

- No shell, arbitrary executable, generic HTTP, Guest credential, package
  installer, or ambient Host filesystem.
- Ordinary Python computation and mounted filesystem operations remain local;
  do not convert them into fake Broker calls.
- All authority-bearing external effects cross a typed Host boundary.
- Compensation is never called rollback without a stronger provider-specific
  proof.
- Playback never redispatches an already-applied external effect.
- Ambiguous outcome blocks blind retry.
- Workspace, interpreter, authority, scratch, effect, and evidence dispositions
  remain independent.
- No real provider, paid service, deployment, or credential is required for the
  first proof.
- Lab UI follows recorded runtime truth; it does not lead contract design.

## North-star acceptance workflow

A deterministic fake provider must support:

1. accepted mutation with successful response;
2. rejection before dispatch;
3. failure before acceptance;
4. accepted mutation followed by response loss;
5. readback reporting applied, absent, or still unknown;
6. explicit forward compensation where qualified.

The full test must prove:

```text
freeze authority + base revision
→ create private attempt
→ stage exact immutable intent
→ journal before dispatch
→ inject accepted-but-response-lost
→ persist ambiguous outcome
→ deny blind retry
→ reconcile by stable operation identity
→ commit/discard/freeze workspace independently
→ verify the complete terminal vector offline
```

## Phase 0 — truth reset and contract freeze

**Purpose:** prevent implementation from outrunning the claim.

Deliverables:

- [x] retire `pysolate.fs`; use ordinary Python filesystem APIs;
- [x] source-reviewed Cloudflare comparison matrix;
- [x] authority-lifecycle positioning ADR;
- [x] this proof-first roadmap;
- [ ] define strict versioned schemas for Run terminal vector, workspace attempt,
  EffectIntent, operation, attempt, approval, reconciliation, and compensation;
- [ ] define Current/Proposed mappings to exact Go packages and symbols.

Exit gate:

- schemas reject unknown fields and cross-plane contradictions;
- docs make no transactional/effect claim unsupported by tests.

Stop/reframe signal: the model cannot represent partial or ambiguous outcomes
without one overloaded success/status field.

## Phase 1 — private workspace attempts

**Purpose:** isolate tentative Guest writes from the durable workspace.

Required behavior:

- immutable base workspace revision identity;
- private per-execution attempt root;
- ordinary Python sees the attempt as `/workspace`;
- commit publishes one new revision atomically at the implemented boundary;
- discard removes attempt state without changing the base;
- freeze preserves protected evidence for conflict/ambiguity review;
- conflict rejects commit if the expected base changed;
- current direct-write mode remains explicit and backward compatible until a
  migration decision is made.

Tests:

- success, Guest exception, timeout, cancellation, trap, quota failure;
- base unchanged after discard;
- no partial publication;
- stale-base conflict;
- `/tmp` never promoted;
- Host-path and symlink denial;
- Capsule and manifest identity binding.

Exit gate: a real Guest contaminates the attempt under every terminal path and
an independent base snapshot remains unchanged until explicit commit.

Stop/reframe signal: copy/overlay cost dominates the bounded target workflow or
cannot provide atomic publication under the current storage model.

## Phase 2 — effect operation/attempt truth

**Purpose:** represent external effects independently from Guest return status.

Required model:

- immutable `EffectIntent` with canonical digest;
- stable workflow, step, operation, and provider request identities;
- separate logical operation and physical dispatch attempt;
- journal-before-dispatch transition;
- effect class: `read_only | reversible | compensatable | irreversible | unknown`;
- commit policy: `DENY | AUTO_COMMIT | AGENT_COMMIT_REQUIRED | USER_APPROVAL_REQUIRED`;
- exact terminal states including `not_dispatched`, `rejected`, `applied`,
  `failed`, `ambiguous`, `reconciled_applied`, `reconciled_absent`, and
  `reconciliation_required`;
- unknown writes denied by default.

Use an in-process deterministic fake provider. Do not add a real SaaS adapter.

Exit gate: accepted-but-response-lost produces one applied provider mutation,
one ambiguous Host attempt, and no automatic second dispatch.

Stop/reframe signal: operation identity cannot be kept stable across Guest,
Host, journal, and provider readback.

## Phase 3 — approval and compensation

**Purpose:** bind authorization and recovery to exact immutable state.

Required behavior:

- Agent commit request is distinct from human approval;
- human approval originates outside Guest/Agent authority;
- approval binds intent digest, operation identity, policy version, destination
  scope, expiry, and one authorized transition;
- staging Run cannot also possess later commit authority;
- compensation is a new forward attempt with its own identity and evidence;
- failed/partial compensation remains visible;
- provider drift or changed target state refuses unsafe compensation.

Exit gate: stale, altered, replayed, wrong-user, wrong-policy, and expired
approvals all fail without dispatch; compensation never rewrites historical
truth to “never happened.”

Stop/reframe signal: the adapter cannot expose a stable readback or qualified
compensation contract.

## Phase 4 — terminal-vector verifier

**Purpose:** make claims independently checkable rather than UI assertions.

Verifier inputs must bind:

- source/input/schema;
- artifact/manifest/profile;
- capability plan/spec/grant/handler/policy;
- base and attempt workspace identities;
- intent/operation/attempt/approval identities;
- provider evidence and reconciliation result;
- terminal interpreter/authority/workspace/effect/evidence dispositions;
- final response and committed workspace revision.

Negative tests mutate each identity, reorder transitions, omit ambiguity, invent
rollback, reuse stale authority, and substitute another workspace base.

Exit gate: offline verification accepts the canonical north-star record and
rejects every bounded corruption fixture.

Stop/reframe signal: verification depends on mutable live Host state not captured
or referenced by a stable protected identity.

## Phase 5 — backend-neutral conformance

**Purpose:** determine whether authority semantics are independent of
CPython/WASI rather than merely claimed to be.

Start with a deliberately small second executor or adapter. Freeze source-level
workload, capability spec, policy, authority, provider, and oracle. Compare:

- plan and grant interpretation;
- operation/attempt transitions;
- denial and stale-authority behavior;
- workspace/effect terminal vectors;
- evidence accepted by the same verifier.

Do not build a general Computer backend. Compatibility expansion is not the
phase goal.

Exit gate: both backends pass one shared conformance suite and produce verifier-
accepted records without gaining undeclared authority.

Stop/reframe signal: backend adaptation requires ambient facilities that bypass
the Broker or changes the meaning of a capability/effect state.

## Phase 6 — evidence-led Lab update

Only after Phases 1–4 are real:

- visualize operation versus attempt;
- show workspace and effect dispositions independently;
- expose ambiguity and reconciliation without implying failure/success;
- label compensation honestly;
- link every view to verifier-backed records;
- retain protected/private body boundaries.

No speculative trace node may be presented as captured runtime truth.

## Global gates

Each implementation slice must run the focused tests plus, before commit:

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

## Decision gates

After Phase 2, choose one:

- **Proceed:** the full conjunction produces clear safety/truth evidence;
- **Narrow:** retain Pysolate as a bounded authority-runtime research prototype;
- **Pause:** adjacent systems cover the same measurable contract at lower cost;
- **Research-only:** preserve mechanisms without expanding toward a product.

No later phase is automatic. Proof precedes surface area.
