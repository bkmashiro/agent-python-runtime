# Megagoal 1: Source-Bound Compiler Pass and Provenance Foundation

> **For Hermes:** Execute only this plan. Use `docs/plans/2026-08-15-source-bound-agent-program-roadmap.md` as the master architecture. Stop after closeout; do not enter recorder/Lab work.

**Status:** Active

**Baseline:** `a5bd564c2dc5bf675952ec4eafa3519c6958ff31` on `feat/programmatic-hot-approval`.

**Goal:** Turn the existing target-Guest semantic overlay and one-off pre-dispatch legality path into a thin, deterministic, default-off source-bound pass planner; bind supported real PTC capability executions to Host-verified exact source occurrences; and evaluate CPython `sys.monitoring` as optional executed-line evidence without changing authority or baseline execution.

## Frozen boundaries

- Exact target CPython remains parser and executor.
- The semantic overlay is analysis-only, not an executable IR.
- No SSA, source rewriting, second Python executor, arbitrary region execution, cache/coalescing/batching/branch-hoisting pass, cold continuation, snapshot or scheduler work.
- Passes are pure planning code: no capability/provider handle, Broker, network, filesystem or dispatch authority.
- Every pass is independently default-off. Unknown pass/config/conflict fails closed.
- AST source binding and runtime executed-line evidence are separate claims.
- Direct presentation calls without a Python source occurrence remain `not_recorded`; do not fabricate a span. PTC calls may become `source_bound` only after the Host matches an actual Broker call against a unique occurrence in a verified source plan.
- `sys.monitoring` evidence is optional/debug-only and never authorizes execution or upgrades a static source claim.
- Do not replace trajectory/experiment schemas or fixtures in this megagoal.

## Current source archaeology

### Current verified analysis path

```text
runtime/semantic.NewRequest
  -> exact target-Guest Engine.AnalyzeSemantic
  -> guest/bootstrap/agent_runtime/semantic.py
  -> canonical pysolate.semantic-analysis.v3
  -> runtime/semantic.DecodeAnalysis
  -> AnalyzeVerified / VerifiedAnalysis
```

The analysis already binds source, AST, analyzer, artifact, execution profile, import closure and capability Plan identities. It exposes source spans, function/effect summaries, barriers, positive-only exact call sites and candidate regions.

### Current planning and execution path

```text
VerifiedAnalysis + sealed capability.Plan + PreissueContext
  -> semantic.CanPreissue(callSiteID)
  -> opaque QualifiedCall
  -> NewSemanticPreDispatch
  -> staged observation
  -> unchanged Broker boundary dynamically claims it
```

`QualifiedCall` is already the correct opaque Host proof. `SemanticPreDispatch` is already the bounded runtime consumer. The missing abstraction is a deterministic pass set and canonical plan/decision projection around them.

### Current source-link limitation

Call sites contain exact spans and canonical arguments, while actual Broker receipts contain call/parent/capability/request/approval identity but no source binding. The PTC wrapper must not be trusted to author source identity. Initial source binding therefore uses a Host-owned resolver constructed from `VerifiedAnalysis`: it matches a real programmatic Broker call to a unique verified occurrence by source digest, capability and canonical arguments. Ambiguous or absent matches remain `not_recorded`.

### `sys.monitoring` feasibility baseline

A bounded local probe against the exact CPython 3.14 WASM Guest established that:

- `sys.monitoring` exists in the real Guest;
- local `CALL` events expose instruction offsets that `dis.get_instructions(code).positions` maps to exact line/column ranges;
- local `LINE` tracing worked but cost about 18x on a line-heavy microbenchmark;
- local `CALL` tracing cost about 3x on a deliberately call-heavy microbenchmark.

These numbers are preliminary and must be rerun against the final named artifact. They justify an optional CALL-oriented debug spike, not always-on line tracing.

## Target architecture

```text
VerifiedAnalysis
  + sealed capability.Plan
  + frozen PreissueContext
  + default-off PassSet
        |
        v
thin deterministic Planner
  -> canonical SourceBoundPlan
       - SourceDocument
       - SourceOccurrence[]
       - PassDecision[] admitted/rejected
       - stable reasons and identity
       - private opaque QualifiedCall lookup
        |
        +-> existing SemanticPreDispatch consumer
        |
        +-> Host SourceBinder
               -> real programmatic Broker call
               -> bound receipt source occurrence
```

## Planned API shape

Names may change during TDD, but the contract must preserve this shape:

```go
type PassName string

type PassConfig struct {
    Name    PassName
    Enabled bool
}

type PlannerConfig struct {
    Passes          []PassConfig
    PreissueContext PreissueContext
}

type SourceDocument struct {
    ID       string
    Language string
    SHA256   string
    Span     SourceSpan
}

type SourceOccurrence struct {
    ID                string
    DocumentID        string
    Span              SourceSpan
    Capability        string
    DynamicOccurrence uint32
}

type PassDecision struct {
    PassName       PassName
    PassVersion    string
    OccurrenceID   string
    Disposition    admitted | rejected
    RejectionCodes []RejectionCode
}
```

`SourceBoundPlan` must return defensive copies/canonical projection and retain qualified calls only in private fields. Its serialized identity must not contain handlers, provider objects, Broker pointers or capability authority. The plan/overlay digest is provenance, not a cache key.

## Source binding contract

A source binding attached to capability evidence contains only:

- schema/claim level (`source_bound`);
- source/document digest;
- static occurrence ID;
- exact start/end line and column;
- capability;
- dynamic occurrence.

Rules:

1. Broker obtains it from a Host-owned resolver, never from untrusted request JSON.
2. Resolver uses a verified source plan and the actual admitted capability/canonical arguments.
3. Match must be unique; duplicate or ambiguous candidates produce no binding.
4. Programmatic identity and source binding are independently validated.
5. Receipt identity binds the source fields when present.
6. Response decoding recomputes and validates the resulting receipt identity.
7. A direct call or unsupported dynamic Python path returns no source binding rather than a guessed span.
8. Source binding has no effect on grants, policy, dispatch, approval, replay or cache behavior.

## Phase 0 — Plan and baseline

- [x] Freeze master roadmap and branch baseline.
- [x] Trace semantic analyzer, VerifiedAnalysis, legality, pre-dispatch, Broker and receipt seams.
- [x] Run disposable CPython/WASM monitoring availability, exact CALL-position and rough overhead probes.
- [x] Commit this extracted plan before code changes.

Gate: documentation passes `git diff --check`; signed commit is pushed.

## Phase 1 — RED: pass/planner contract

**Status:** Complete at the source-bound planner slice. The default-off plan projects source facts without decisions; the explicit semantic pre-dispatch pass reuses `CanPreissue`, retains only opaque qualified calls, rejects unknown/duplicate/version-mismatched pass configuration, and produces a deterministic defensive projection.

Write failing tests for:

- empty/default config produces no pass decisions and no behavior change;
- explicit semantic pre-dispatch pass produces the same admitted `QualifiedCall` as direct `CanPreissue`;
- rejected sites preserve sorted stable rejection reasons;
- unknown pass, duplicate pass, invalid version/config and conflicting admitted decisions fail closed;
- pass ordering and plan identity are deterministic;
- plan output is defensively copied;
- qualified call cannot be reconstructed from public projection;
- no pass can directly access or dispatch a handler.

Expected implementation seams:

- new `runtime/semantic/planner.go`;
- new `runtime/semantic/planner_test.go`;
- minimal helpers in `legality.go`/`verified.go` only if needed;
- update current pre-dispatch integration test to consume the plan-produced qualified call.

Gate: focused semantic tests and `git diff --check`; signed commit and push.

## Phase 2 — RED: source occurrence and Broker evidence binding

**Status:** Complete. A Host-created resolver matches only unique canonical programmatic occurrences from a verified source-bound plan; direct calls remain unbound, ambiguous/mismatched calls produce no source claim, invalid resolver output is denied before dispatch, and receipt v3 binds the exact source span into operation identity. The real CPython/WASM E2E validates the source digest, static occurrence and line/column range.

Write failing tests for:

- verified analysis projects one canonical source document and exact occurrences;
- real programmatic call with unique capability+canonical arguments binds exact span;
- wrong capability, changed arguments, duplicate matching occurrences, near-match source ID and ordinary direct call do not receive a binding;
- source resolver cannot widen Plan/grant/admission;
- receipt identity changes when any source binding field changes;
- forged/missing/malformed source-bound receipt is rejected by response validation;
- approval and programmatic parent binding remain intact when source evidence is present;
- baseline broker response/outcome/handler count is identical with source binding disabled and enabled.

Expected implementation seams:

- source document/occurrence projection in `runtime/semantic`;
- Host-owned resolver implementing a narrow interface declared at the capability boundary;
- optional source evidence in `runtime/receipt`;
- Broker records a validated binding after actual call admission;
- runtime response/schema validation binds the new receipt version;
- real Guest E2E with one exact PTC call and an adversarial no-binding case.

Gate: focused semantic/capability/receipt/response/race tests; signed commit and push.

## Phase 3 — bounded monitoring spike

Implement an explicitly experimental test/probe, not production tracing:

- confirm exact CPython version and `sys.monitoring` availability in the final artifact;
- capture a local CALL event and map instruction offset to exact line/column range;
- compare disabled versus local CALL monitoring in a bounded repeated microbenchmark;
- compare disabled versus LINE monitoring only to document why it is not default;
- verify agent result/exception parity;
- record exact command, source commit/tree, artifact SHA-256, sample count and raw medians;
- classify outcome:
  - `accepted_for_future_debug_profile`, or
  - `rejected` with concrete reason.

Do not integrate monitoring into authority, receipts or default execution in this megagoal. Do not label static AST spans as executed lines.

Gate: reproducible private/raw output plus body-safe checked-in evidence; signed commit and push.

## Phase 4 — real Guest evidence and closeout

Required verification:

```text
go test -race ./runtime/semantic ./runtime/capability ./runtime/receipt ./runtime ./runtime/engine/wazero -count=1
go test ./integration/e2e -run '<source-bound and semantic pre-dispatch tests>' -count=1 -v
go test -race ./... -count=1
go vet ./...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
git diff --check
```

Real Guest evidence must name exact source commit/tree and rebuilt WASM artifact SHA-256. Tests that skip because `AGENT_RUNTIME_GUEST` is unset do not count.

Independent review scope:

- public pass projection cannot forge qualified authority;
- planner determinism and conflict handling;
- unique/ambiguous source matching;
- direct-versus-programmatic separation;
- receipt/source/parent/approval cross-binding;
- no handler count or outcome drift;
- claim vocabulary for source-bound versus executed-line evidence;
- no accidental Megagoal 2 work.

Closeout only after blocker fixes, broad gates, signed commits, push and clean upstream state.

## Stop gate

Stop and report after all of the following are available:

- thin default-off pass API and canonical plan identity;
- current pre-dispatch obtained through the pass planner with unchanged behavior;
- real CPython/WASM PTC source binding to one exact verified span;
- explicit no-binding evidence for unsupported/ambiguous/direct cases;
- bounded `sys.monitoring` verdict and overhead evidence;
- focused/full verification and independent review outcome;
- updated master roadmap status.

Do not start recorder vNext, delete historical Lab data, implement Lab UI, add new optimization families or merge the branch.
