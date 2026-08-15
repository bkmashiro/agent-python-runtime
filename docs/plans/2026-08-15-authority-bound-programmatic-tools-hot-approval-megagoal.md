# Authority-Bound Programmatic Tools and Hot Approval Mega-Goal

> **For Hermes:** Yuzhe started this goal on 2026-08-15. Continue through the
> implementation and real-Guest evidence gates. Do not enter cold continuation,
> pageout, or snapshot/restore without a new discussion.

**Status:** Active
**Date:** 2026-08-15
**Owner:** Yuzhe
**Repository:** `~/projects/agent-python-runtime`
**Prepared baseline:** `0fa95dc922110bd653d338563a9b5aab8c8fecf8`

## Progress

- [x] Phase 0 — source archaeology and frozen implementation map
- [x] Phase 1 — presentation contract and parent/child evidence
- [x] Phase 2 — approval suspension, lease and audit
- [x] Phase 3 — real-Guest parity, cancellation and zero-replay evidence
- [ ] Phase 4 — full verification, independent review and closeout

## Mission

Add an optional programmatic tool presentation and bounded hot approval waiting
without weakening Pysolate's Host-owned capability plane:

```text
model-authored Python
        -> generated typed wrappers
        -> existing host_call ABI
        -> same Broker / Plan / grant / handler / receipt pipeline
```

An approval-required call may wait inside the existing synchronous Host ABI and
return to the same live Python execution after approval. Rejection, expiry, and
cancellation terminate that call without invoking its handler. No program replay
or continuation reconstruction is permitted.

## Claim boundary

Target claim:

> A Pysolate Python/WASM Guest can compose multiple typed Host calls through the
> same authority and evidence path as direct tool presentation, while one
> approval-required call can remain pending and resume at the exact ABI boundary
> after a bounded Host decision without replay.

This goal does **not** claim crash-safe continuation restore, cold residency,
workflow durability, scheduler optimality, performance improvement, or support
for arbitrary Harnesses.

## Frozen boundaries

- Program surface is `direct | programmatic | both`; presentation is independent
  of `pysolate_wasm | native_sandbox` placement.
- Programmatic mode does not create a registry or handler path.
- Programmatic subcalls use a Host-bound parent identity and deterministic child
  identities. Guest code cannot widen Plan or grant authority.
- Approval policy is Host-authored and Plan-bound. Approval does not imply PTC,
  cache, playback, semantic reuse, staged execution, cold memory, or retry.
- Every optional mechanism is default-off. Invalid mechanism/surface/provider
  combinations fail closed.
- Approval lease expiry is semantic policy. It is not a memory cooling timeout.
- Audit state may outlive execution state, but retains bounded digests and status,
  not Guest memory, source, credentials, or raw arguments.
- Approved handlers execute at most once. Rejected, expired, cancelled, denied,
  malformed, and duplicate calls execute zero times.
- Started or ambiguous effects are never replayed, retried, migrated, or treated
  as not-started.
- No cold pageout, mmap allocator, cross-process restore, indefinite workflow,
  production scheduler, Cloudflare replay, or DeepSeek worker trust model.

## Source-backed related implementation

DeepSeek Harness commit
`47f943859bef60e4160492346772ded9b24f765a` confirms the external
`native | code | both` presentation pattern, deterministic nested call IDs, same
registry re-entry, cancellation inheritance, and nested Web evidence. Pysolate
uses `direct | programmatic | both` to avoid collision with its native-sandbox
backend and preserves a stronger WASM/authority boundary. Full provenance is in
`docs/research/approval-continuation-and-programmatic-tool-calling.md`.

## Baseline archaeology

The implementation must reuse these seams:

- `runtime/capability.Registry` and sealed `Plan` own Specs, grants, Python
  projections, tool schemas, handlers, and generated wrappers.
- `runtime/capability.Broker.call` is the single admission, schema, budget,
  playback/branch, dispatch, result-validation, receipt, and transcript path.
- `runtime/engine/wazero.hostCall` is already synchronous. Blocking inside
  `Broker.Call` leaves the actual Wazero/Python invocation live and resumes it
  when the Host function returns.
- `runtime/engine/wazero.runWithPrepares` binds one fresh Broker to one Run,
  finalizes it once, projects receipts, and cancels on Run teardown.
- `guest/src/runtime.c::python_host_call` performs one blocking ABI call; no Guest
  protocol change is required for hot waiting.
- `runtime.MechanismSet` already provides default-off selection and dependency
  validation.

Do not duplicate any of these paths.

## Track 0 — frozen implementation map

| Concern | Reuse | Minimal addition |
|---|---|---|
| presentation | `Plan.ToolSchemas`, `Plan.PythonPrelude` | typed surface projection plus parent-bound programmatic prelude |
| child identity | Broker request validation and operation sequence | deterministic `parent:program:<n>` validation and receipt projection |
| approval | Broker immediately before live handler dispatch | Plan-bound requirement plus one controller/provider seam |
| hot wait | synchronous Wazero Host function | controller blocks on approve/reject/expiry/context only |
| audit | Host-owned approval records | bounded digest-only store independent of module teardown |
| evidence | existing receipts and real-Guest response projection | call parent/child plus approval request/status binding |

A new package is justified only for approval because it owns an independent
request/lease/decision/audit lifecycle. Program presentation belongs with the
existing capability Plan unless implementation evidence proves otherwise.

## Track 1 — RED/GREEN program surface

RED tests must first prove:

- zero-value/default config is direct with PTC disabled;
- PTC mode without its mechanism is invalid and vice versa;
- direct exposes schemas only, programmatic exposes generated Python only, both
  exposes both;
- parent IDs are bounded, generated child IDs are stable, and near-match or
  duplicate child IDs fail closed;
- direct and programmatic calls reach the same handler and produce equivalent
  authority/result evidence.

GREEN must not add another registry, scheduler, handler, or transport.

## Track 2 — RED/GREEN approval lifecycle

RED tests must first prove:

- approval-required Spec cannot dispatch without enabled approval/controller;
- approve crosses one explicit dispatch-commit gate and dispatches exactly once;
- reject and expire dispatch zero times; cancellation dispatches zero when it wins
  that gate;
- late approval after expiry cannot revive a request;
- cancellation unblocks a pending call;
- duplicate decision is rejected;
- audit retains request ID, call binding, capability, argument digest, timestamps,
  status, and terminal outcome without raw arguments/results;
- audit remains readable after Broker/Guest teardown;
- PTC does not activate approval and approval does not activate PTC.

GREEN uses the same Broker live-handler branch after authorization. No approved
result cache or replay log is introduced.

## Track 3 — real-Guest evidence

Build or reuse the exact WASM artifact and run bounded E2E cases:

1. direct and programmatic calls over the same Plan/handler produce equivalent
   results and shared Broker evidence shape;
2. a programmatic Guest makes at least two ordered child calls under one parent;
3. one Guest local value is set before an approval call and consumed after Host
   approval, proving continuation at the same Python invocation;
4. the handler call count is one, source execution count is one, and no replay
   path is called;
5. reject and expiry execute no handler; cancellation that wins the explicit
   dispatch-commit gate also executes no handler and terminates cleanly;
6. parent/child IDs, receipt IDs, execution ID, Plan digest, approval request, and
   audit status cross-bind.

Evidence is fixture-bounded. Do not infer arbitrary-Harness or crash durability.

## Gates

Focused slice gates:

```text
go test -race ./runtime/... -count=1
go vet ./runtime/...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
```

Final gates:

```text
go test -race ./... -count=1
go vet ./...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
```

Run the real-Guest E2E against a freshly built or identity-verified artifact.
Preserve artifact/profile/source identities and exact commands in evidence docs.

## Real-Guest evidence record

The bounded campaign in
`docs/evidence/programmatic-hot-approval-real-guest-v0.json` passed against:

```text
Host source commit: cea536308794ce9188bcd8b7109db32c0a6ff3fd
Guest source commit: db756fd7b40d465072b5fb1b6f3867d29c5d8114
Guest artifact SHA-256: sha256:d5706fbf113c7042a4484ad5713ee5baa8fe4788c33beb9b6223b0ff9f1201af
Guest target: wasm32-wasip1
```

Observed tests cover two ordered programmatic calls, direct/programmatic Broker
parity, one approved same-invocation continuation, and reject/expire/cancel with
zero handler dispatch. The Guest source predates only later Host-side receipt
validation and cancellation-race hardening; no Guest source changed between those
commits.

No performance, cold-residency, crash-recovery, native-sandbox or arbitrary
Harness claim follows.

## Decision and stop gates

Stop and discuss instead of broadening if:

- true hot waiting requires a wazero fork or stack snapshot;
- approval cannot be inserted before handler dispatch without duplicating Broker;
- existing trusted preparation cannot produce parent-bound wrappers safely;
- native sandbox parity is required for the target claim;
- a valid approval result would require retry or effect replay;
- audit correctness requires retaining raw private bodies;
- real-Guest tests cannot distinguish same execution from replay.

After PTC parity and hot approval real-Guest evidence pass, update this roadmap to
Complete, obtain a bounded independent post-fix review, sign commits, push the
feature branch, and **stop before cold continuation/memory tiering**.
