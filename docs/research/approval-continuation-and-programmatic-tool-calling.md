# Approval continuation, memory tiering, and programmatic tool calling

Status: **Programmatic surface and hot approval v0 implemented; cold continuation remains proposed**

Date: 2026-08-15

## Implemented v0 boundary

The first bounded slice is implemented and evidenced by
[`programmatic-hot-approval-real-guest-v0.json`](../evidence/programmatic-hot-approval-real-guest-v0.json):

- `ProgramSurface.mode = direct | programmatic | both` is Host-owned and
  programmatic exposure is independently default-off;
- direct schemas and parent-bound Python projection are generated from the same
  sealed `pysolate.capability-plan.v6`;
- programmatic child IDs are exactly `parent:program:<n>` and near-match IDs are
  denied before call-budget consumption;
- one optional Plan-bound approval lease blocks synchronously inside the existing
  Broker call before the live handler;
- approve reaches an explicit dispatch-commit linearization point and dispatches
  once; reject and expiry dispatch zero times, while cancellation dispatches zero
  when it wins that gate;
- body-safe approval audit state remains in the independent controller after the
  Broker/Guest is closed;
- receipts bind child, parent, approval request and Plan identities.

The real CPython/WASM test retained a Python local across the pending Host ABI
call and consumed it after approval. This is evidence for same-process hot
zero-replay continuation only. It is not evidence for crash-safe restore,
pageout, migration or arbitrary-Harness durability.

## Decision

Pysolate should treat the following as separate, Host-owned mechanisms rather
than one bundled “durable execution” mode:

1. **programmatic tool calling (PTC)** — how tools are presented to and composed
   by the model;
2. **approval suspension** — whether a capability call may block on an approval
   promise and resume the same execution;
3. **continuation memory tiering** — whether a blocked execution remains resident
   or becomes cold/page-backed;
4. **approval leases** — when a stale proposal loses semantic validity;
5. **durable approval/audit records** — what survives after execution state is
   destroyed.

Each mechanism is independently configurable, has explicit dependencies, and
must fail closed when its required backend contract is unavailable. The zero
value remains ordinary fresh execution with all optional mechanisms disabled.

This preserves the central split:

```text
short/medium wait, while qualified and semantically valid
  -> preserve the actual continuation; zero replay

long/stale wait
  -> expire and destroy execution
  -> retain only intent/provenance/audit state
```

Pysolate does **not** turn every Agent run into an indefinitely durable workflow.

## Why approval can be a real suspension point

A CPython/WASM capability call already crosses a Host-owned ABI:

```text
Python -> generated tool bridge -> WASM Host ABI -> Broker / Harness
```

A capability that requires approval can therefore leave the ABI call pending:

```text
RUNNING
  -> approval-required Host call
  -> WAITING_APPROVAL
  -> approval resolves
  -> the same ABI call returns
  -> the same Python execution continues
```

To Guest Python this remains a blocking function call. No tool-log replay is
required for the hot path.

However, this is a **Proposed backend contract**, not a claim that current
Pysolate can serialize and restore an arbitrary CPython/Wazero continuation.
The existing audit in
[`wait-suspension-and-reuse-tradeoffs.md`](../wait-suspension-and-reuse-tradeoffs.md)
remains authoritative: linear memory alone is insufficient because complete
continuation state also includes Wasm execution state and Host-side resources.
A true cold restore claim requires a version-pinned backend proof that captures
and restores every required state class.

## Lifecycle

```text
RUNNING
   |
   | capability requires approval
   v
WAITING_APPROVAL (hot continuation)
   |                         |
   | cooling threshold Ts    | approval/rejection
   v                         +-----------------> resume/fail call
COLD_SUSPENDED
   |                         |
   | semantic lease Te       | approval/rejection
   v                         +-----------------> restore + resume/fail call
EXPIRED
   |
   +-> destroy stack, interpreter, memory and Host continuation
   +-> retain bounded approval/audit record
```

`Ts` and `Te` are deliberately different:

- `Ts` is a resource policy deciding when resident pages become cold;
- `Te` is a semantic/authority policy deciding when the proposed operation is no
  longer executable.

The resource policy must never silently extend the semantic lease.

## Approval lease

An approval request should bind at least:

```text
request identity
capability/tool identity
canonical argument digest
Plan/grant/execution identity
creation time
expiry time
status
```

Approval authorizes only that bound request before expiry. On expiry:

1. the waiting capability resolves with typed `ApprovalExpired`;
2. the current execution is cancelled and destroyed;
3. no capability is dispatched;
4. a minimal body-safe audit record remains;
5. a later attempt requires fresh observation, a fresh proposal, and fresh
   approval.

Execution lifetime and audit lifetime are independent. UI durability is not a
reason to retain a Python interpreter.

## Continuation memory tiers

### Hot waiting

Keep the Wasm stack, linear memory and Host continuation resident. This is
appropriate only for short waits under a bounded lease.

### Cold resident/page-backed waiting

A qualified memory provider may use a dedicated mmap-backed linear-memory region
and operating-system mechanisms such as `MADV_COLD`, `MADV_PAGEOUT`, swap, or a
private backing file. Harness knowledge that an execution is waiting on a human
is a stronger dormancy signal than generic page-access history.

This tier still preserves the same virtual execution. Waking it faults pages
back and resumes the same continuation; it does not replay the Python program.

This remains Experimental until a pinned backend proves:

- complete continuation capture/retention, not just linear-memory bytes;
- safe handling or rejection of WASI descriptors and Host resource handles;
- authority and approval revalidation before the pending call returns;
- deterministic teardown on rejection, expiry, cancellation and process death;
- compatibility bounds across runtime/artifact versions;
- measured memory, storage, suspend and wake costs.

### Expired

Destroy all physical execution state. Retain only bounded identities, hashes,
timestamps, decision and non-execution reason. Never retain credentials or raw
private arguments unless a separately governed private audit policy requires
and protects them.

## DeepSeek Harness PTC: source-backed comparison

The following observations are against `deepseek-ai/deepseek-harness` commit
[`47f943859bef60e4160492346772ded9b24f765a`](https://github.com/deepseek-ai/deepseek-harness/tree/47f943859bef60e4160492346772ded9b24f765a),
inspected on 2026-08-15. This pinned source is the provenance for the factual
claim that DeepSeek exposes three tool-presentation modes: `native`, `code`, and
`both` (`packages/core/tools/src/index.ts`). Its shipped `code` preset provides
the concrete per-agent Code Mode composition
(`apps/cli/config/agent-presets/code/agent.cordis.yml`).

This is external corroboration, not provenance for Pysolate's authority-bound
Host tool plane, fresh execution or continuation design. In later paper text,
attribute the three-mode presentation comparison and DeepSeek implementation
facts to this pinned source; describe Pysolate's stronger WASM/authority and
zero-replay proposal separately.

DeepSeek’s PTC/Code Mode is principally a **tool-presentation and orchestration
layer**, not continuation durability:

- the shipped `code` preset says it keeps Standard capabilities and adds a
  per-agent `tool-presentation` row with `mode: code`;
- the tool registry supports `native`, `code`, and `both`; under `code`, the
  model-facing schema collapses to `run_code`, while generated SDK bindings
  still dispatch through the same Host tool registry;
- a model-authored TypeScript async-function body calls tools through
  `await tools.name(args)`;
- nested calls go through the native scheduler’s prepare/dispatch/finalize
  pipeline, preserve parallel/exclusive classifications, inherit the parent
  execution token and abort signal, and receive deterministic child call IDs;
- each nested dispatch is durably logged under its parent `run_code`, while only
  the program’s selected logs/return value enters the outer model-visible tool
  result;
- the Web UI presents `run_code` as a parent row with visible nested native-tool
  rows;
- its published worker-thread runtime explicitly states that worker containment
  is **not a security boundary** and model code has bash-equivalent trust.

Primary source locations:

- `apps/cli/config/agent-presets/code/agent.cordis.yml`
- `packages/core/tools/src/index.ts`
- `packages/core/tools/src/code-mode.ts`
- `packages/code-runtime/code-runtime-worker-thread/src/index.ts`
- `apps/web/tests/code-mode-round.e2e.ts`

### What Pysolate should reuse conceptually

1. PTC is a presentation choice, not a second tool registry.
2. Programmatic subcalls must re-enter the same Host-owned capability,
   authority, approval, effect and evidence pipeline as native calls.
3. A composite execution needs a parent identity and deterministic child-call
   identities.
4. Native and PTC calls should share scheduling and policy semantics.
5. The outer model result can be curated without losing private/full
   sub-dispatch evidence.
6. The UI should show the parent program and causal nested calls without becoming
   an execution authority.

### What Pysolate should not copy blindly

- Worker-thread isolation is insufficient for Pysolate’s authority boundary.
- PTC must not acquire ambient shell/filesystem/network authority merely because
  its program can express loops or concurrency.
- A PTC program’s ability to catch a tool error must not convert an ambiguous or
  authority-invalid effect into a safe retry.
- PTC is not a reason to replay a program after an approval pause. The same
  approval-capable Host ABI should work for native one-tool calls and PTC
  subcalls.
- PTC should not be fused with semantic analysis, caching, workflow resume,
  approval, or cold memory. Those mechanisms may compose, but one does not imply
  another.

## Independent mechanism controls

Use typed modes rather than an undifferentiated cluster of booleans. DeepSeek
names its three presentation values `native | code | both`; Pysolate should not
reuse `native` for this axis because it already has a native-sandbox execution
backend. Use presentation-specific names instead:

```text
ProgramSurface
  mode = direct | programmatic | both

Approval
  mode = off | lease
  default_lease
  max_lease

Continuation
  mode = off | hot | tiered
  cooling_after
  cold_backend = os_advice | page_backed

Audit
  approval_records = off | body_safe | private_encrypted
```

These are conceptual config boundaries; names are not yet public API.

### Dependency matrix

| Mechanism | Depends on | Must not imply |
| --- | --- | --- |
| Programmatic tool calling | code Guest + generated tool bindings | approval, cache, replay, semantic reuse |
| Approval suspension | capability Broker + approval provider + suspendable call | cold pageout, PTC |
| Approval lease | approval suspension + Host clock | durable continuation |
| Hot continuation | approval suspension + bounded execution lease | pageout |
| Cold tiering | hot continuation + qualified backend/memory provider | indefinite retention |
| Durable audit | approval state + private/body-safe storage policy | live execution retention |

Validation should reject invalid combinations rather than silently enable their
prerequisites. Examples:

```text
continuation=tiered without approval suspension -> reject
approval=lease without an approval provider      -> fail closed/unavailable
program_surface=programmatic without code Guest  -> typed unavailable
cold backend unsupported on this host            -> typed unavailable or hot-only,
                                                    according to explicit policy
```

Do not overload existing `cold_io_continuation`: cold I/O buffering and a full
approval-blocked interpreter continuation have different state and proof
requirements.

## Proposed package boundaries

```text
runtime/programcall       model-written program + child-call identities
runtime/approval          request, provider, lease, decision
runtime/continuation      suspend/resume lifecycle, no approval UI policy
runtime/memorytier        backend-specific hot/cold residency operations
runtime/audit             durable body-safe approval projection
runtime/capability        unchanged authoritative dispatch boundary
runtime/engine/wazero     backend adapter only
```

The Harness composes these packages. No package should import a Web UI, paper
fixture, P01–P20 label, model-specific prompt, or another mechanism merely to
turn itself on.

The current `runtime.MechanismSet` pattern remains useful: zero-value off,
explicit dependency validation, Host-owned selection and evidence for selected,
fallback and unavailable dispositions. New mechanisms should be added only with
an executable vertical slice and adversarial tests, not predeclared as a large
flag cluster.

## Slice order

1. **PTC boundary spike:** run one generated Python program in the existing fresh
   Pysolate Guest; expose two benign Host tools through the existing Broker;
   prove parent/child identity, native policy parity, cancellation and no second
   registry. No approval or persistence.
2. **Hot approval spike:** make one capability ABI call await a Host promise;
   approve/reject/expire it and prove the same execution continues only on valid
   approval. No snapshot or pageout.
3. **Lease/audit slice:** separate execution teardown from durable body-safe
   request state; prove expired operations never dispatch.
4. **Cold-residency experiment:** only after measuring retained memory and
   waiting population. Compare resident, OS-advised cold and page-backed modes on
   a pinned backend.
5. **Full continuation restore:** attempt only if the backend exposes and the
   experiment validates every required execution and Host state class.

Each slice is independently removable and independently measurable.

## Claims

Safe current architectural claim:

> Known semantic suspension points at the Harness/ABI boundary can let Pysolate
> preserve and tier actual Agent execution state instead of reconstructing it
> through program replay, provided the selected backend proves complete
> continuation retention and revalidates authority before resume.

PTC claim:

> Programmatic tool calling is an optional Agent-programming surface over the
> same Host-owned tool plane; it changes how calls are composed, not what
> authority they receive.

Do not claim yet:

- arbitrary CPython/Wazero snapshot and restore;
- crash-safe or cross-process continuation durability;
- indefinite continuation retention;
- replay equivalence;
- production memory savings;
- PTC performance improvement without a balanced real-Guest comparison.
