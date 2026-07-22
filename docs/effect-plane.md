# Effect Plane: playback, COMMIT, and APPROVE

## Status

This document is a future design discussion. V1 remains read-only and does not implement external writes, confirmation flows, or an effect transaction system.

The Effect Plane may be implemented only after the read-only runtime proves its capability boundary, hard budgets, receipts, and next-run freshness. It is a separate Host-owned authority layer, not Guest Python state.

## Problem

Some useful Agent actions have unavoidable external side effects:

- sending email or messages;
- publishing content;
- creating or modifying calendar events;
- writing records to external services;
- charging money or placing orders.

A sandbox can prevent unauthorized effects, but it cannot make an authorized email send reversible. The design must therefore control **when authority crosses the external-effect boundary**, persist what was authorized, and make retries safe.

## Terms

### Playback

Playback means returning recorded inputs, outputs, intent state, and Host receipts from an execution journal.

Playback does **not** mean invoking the external provider again.

- Read playback may return a historical recorded result.
- Staged-effect playback returns the existing pending intent.
- Applied-effect playback returns the original receipt.
- Unknown-effect playback returns `reconciliation_required` and blocks blind retry.

### EffectIntent

An immutable proposal for one external operation. It includes canonical arguments, identity, digest, policy decision, and lifecycle state.

Creating an `EffectIntent` does not apply the external operation.

### COMMIT

An explicit Agent decision to proceed with an already staged intent. COMMIT is a second reasoning checkpoint, not human authorization.

A COMMIT must happen in a later Agent turn with a separate Host grant. Generated Python must not be able to stage and commit the same effect in one run.

### APPROVE

A trusted user decision bound to the exact immutable intent. Approval comes from a Host control plane or trusted UI, never from Guest Python.

### Apply

The Host Effect Kernel's attempt to execute a ready intent against the external provider.

### Receipt

Host-authored evidence of what the Effect Kernel attempted and what outcome it observed. Guest-provided receipt-like JSON has no authority.

### Reconciliation

Resolution of an unknown provider outcome, such as a timeout after the provider may already have accepted an email.

## Effect classes

### Pure computation

No external authority. Replay may execute deterministically or reuse a recorded result.

### Read effect

Observes external state without intentionally mutating it. Reads may be replayed from a journal for deterministic testing or recovery, but the result must be labelled historical rather than fresh.

### Compensatable write

Has an explicit follow-up operation that may reduce or reverse its practical impact, such as deleting a newly created calendar event.

Compensation is another external effect. It is not transactional rollback and may fail independently.

### Irreversible or externally visible write

Examples include sending email, publishing a message, or charging money. Once applied, playback must never execute it again.

## Layer boundary

```text
Agent harness / generated Python
             │ propose typed action
             ▼
Host capability broker
             │ canonicalize + validate
             ▼
Effect journal ── immutable EffectIntent + digest
             │
             ▼
Host policy engine
   ├─ DENY
   ├─ AUTO_COMMIT
   ├─ AGENT_COMMIT_REQUIRED
   └─ USER_APPROVAL_REQUIRED
             │
             ▼
Host Effect Kernel
             │ provider call + idempotency key
             ▼
External provider
             │
             ▼
Host receipt / reconciliation state
```

The Guest never receives provider credentials, approval authority, a provider-native client, or a raw network path.

## Python API model

Python may be the composition interface without becoming the authority boundary.

```python
from agent_runtime import mail

pending = mail.send(
    to=["alice@example.com"],
    subject="Meeting notes",
    body=report,
)
```

For an effectful capability, `send()` means:

> submit and return an EffectIntent under Host policy

It does not guarantee that an email was sent. The return type must force the caller to inspect status rather than imply success.

Example result:

```json
{
  "effect_id": "eff_01J...",
  "action": "mail.send",
  "status": "awaiting_user_approval",
  "digest": "sha256:...",
  "preview": {
    "to": ["alice@example.com"],
    "subject": "Meeting notes",
    "body_sha256": "sha256:...",
    "attachment_sha256": []
  }
}
```

The Guest must not select policy with fields such as `approval="auto"`. Host-owned policy determines the route.

## One canonical capability definition

Python APIs should not create a second security implementation. One typed `CapabilitySpec` should drive:

```text
CapabilitySpec
├─ Python SDK types and stubs
├─ direct Agent tool schema
├─ Host argument validation
├─ effect classification
├─ policy hooks
├─ budget accounting
└─ receipt schema
```

Simple operations may remain direct Agent tool calls. Compound workflows may use the Python SDK. Both paths enter the same Host capability and Effect Kernels.

A generic `call_tool(name, arbitrary_json)` is insufficient for high-risk writes because the Host policy needs semantic fields such as recipients, destination, amount, visibility, and attachment identity.

## Policy outcomes

### DENY

Reject the intent before provider execution. Preserve a bounded denial receipt for audit without storing secrets unnecessarily.

### AUTO_COMMIT

Allowed only by prior Host/user policy for a bounded low-risk action. The kernel still journals the immutable intent before applying it.

AUTO_COMMIT is pre-authorized user authority, not authority selected by the Agent.

### AGENT_COMMIT_REQUIRED

The first run returns the staged preview to the harness. A later Agent turn may request COMMIT for that exact digest.

Required controls:

- commit capability absent from the staging run;
- new Agent turn and Host-issued phase grant;
- exact `effect_id` and digest match;
- policy and intent still valid and unexpired;
- no mutable fields supplied during COMMIT;
- changed arguments create a new intent.

Agent COMMIT reduces accidental execution and gives the model a reflection checkpoint. It is not a defense against a malicious or prompt-injected Agent.

### USER_APPROVAL_REQUIRED

The intent waits for a trusted user/control-plane decision. Guest Python and the Agent may request approval but cannot approve themselves.

An approval token must bind at least:

- effect ID and canonical digest;
- action and schema version;
- recipient/destination and content/attachment hashes;
- approving user and channel;
- policy version;
- issue and expiry time.

Changing any approved field invalidates the approval and creates a new intent.

## State machine

```text
PROPOSED
    │ validate + canonicalize + journal
    ▼
STAGED
    │ Host policy
    ├──────────────► DENIED
    ├──────────────► AWAITING_AGENT_COMMIT
    ├──────────────► AWAITING_USER_APPROVAL
    └──────────────► READY
                         │ durable apply lease
                         ▼
                      APPLYING
                         ├────────► APPLIED
                         ├────────► FAILED_RETRYABLE
                         ├────────► FAILED_TERMINAL
                         └────────► RECONCILIATION_REQUIRED
```

State transitions are Host-owned compare-and-swap operations against the expected prior state. Run-level status is derived from effect states rather than updated independently.

Normal commit and retry paths are blocked while any effect requires reconciliation.

## Why COMMIT cannot occur in the same Python run

This provides no real checkpoint:

```python
pending = mail.send(...)
mail.commit(pending.effect_id)
```

If both capabilities exist in one Guest run, generated code can immediately consume its own proposal. A valid two-phase design ends the initial run after returning pending intents. The harness then chooses one of:

- start a later Agent turn with an Agent-COMMIT grant;
- wait for trusted user approval;
- deny or expire the intent;
- apply automatically under pre-authorized Host policy.

The original Python interpreter does not need to suspend and resume. A later run receives the applied receipt or pending status as explicit input.

## Intent identity and idempotency

Exactly-once effects cannot generally be guaranteed across an unreliable network. The target is stable request identity, provider idempotency where available, and fail-closed unknown-outcome handling.

A retry-stable effect identity should bind:

```text
workflow identity
step identity
operation index
capability/action version
canonical argument digest
```

The Host supplies workflow/step identity. Guest code must not gain authority by choosing an arbitrary idempotency key.

Properties:

- retrying the same workflow step resolves to the same intent;
- changing a material argument creates a different digest/intent;
- deliberately starting a new workflow can send a second identical email;
- provider idempotency headers are used only when explicitly supported and validated;
- the provider request ID is stored before dispatch.

## Durable outbox and apply protocol

The Effect Kernel journals before making the provider call.

```text
1. transition READY -> APPLYING with lease and attempt identity
2. persist provider idempotency/request identity
3. dispatch the provider request
4. persist observed provider result and receipt
5. transition APPLYING -> APPLIED or a failure state
```

A process crash before step 3 can safely retry after lease recovery. A crash or timeout during steps 3–4 may leave the provider outcome unknown and must enter reconciliation unless provider idempotency or a status query proves the result.

Do not keep an `APPLYING` row indefinitely. Lease expiry must produce a deliberate recovery decision, not an automatic duplicate dispatch.

## Unknown outcomes and reconciliation

Email example:

1. the Host sends a provider request;
2. the provider accepts the message;
3. the network fails before the Host receives the response;
4. the Host cannot know whether the message was sent.

The correct state is:

```text
RECONCILIATION_REQUIRED
```

The kernel may then:

- query by provider message/request ID;
- search the sender's outbox using stable metadata;
- use a validated provider idempotency lookup;
- request a trusted manual resolution.

It must not blindly retry.

Manual resolution updates only the Host journal. It does not accept provider credentials or issue another provider call through the `resolve` path.

## Playback semantics

Playback reads the journal using stable workflow/effect identity.

| Recorded state | Playback result | Provider call |
|---|---|---|
| no intent | create a new staged intent | no |
| `STAGED` | return existing intent | no |
| awaiting COMMIT/APPROVE | return existing preview and requirement | no |
| `READY` | return ready state; apply only through Effect Kernel | no direct playback call |
| `APPLYING` | return in-progress/lease state | no duplicate call |
| `APPLIED` | return original receipt | never |
| `DENIED` | return original denial | never |
| retryable failure | policy decides a new apply attempt | only through guarded retry |
| reconciliation required | return blocked reconciliation state | never automatically |

For read capabilities, playback must expose `recorded_at`, source identity, and freshness status so historical data is not mistaken for a live read.

## Multiple effects

External providers do not provide a shared transaction. A batch of emails cannot honestly be presented as atomic.

- stage and identify each effect separately;
- a user may approve a batch manifest, but approval binds every member digest;
- apply and receipt status remain per effect;
- partial application is represented explicitly;
- compensation, if supported, is a new effect with its own policy and receipt.

## Security invariants

- Guest Python can propose effects but cannot grant itself authority.
- Effectful actions have typed semantic schemas.
- The Host canonicalizes arguments before hashing and policy evaluation.
- Every approval and COMMIT binds the immutable intent digest.
- Commit authority is absent from the staging run.
- User approval originates outside the Agent/Guest trust boundary.
- The Host journals before provider dispatch.
- Applied effects are never re-executed by playback.
- Unknown provider outcomes block blind retry.
- Provider credentials never enter Guest memory or receipts.
- Receipts are Host-authored and bounded.
- Policy changes cannot silently mutate an already approved intent.
- Expired intents, approvals, and apply leases fail closed.

## Email policy example

Illustrative Host policy:

```text
read mailbox metadata                     -> read, execute under grant
save local draft                          -> AUTO_COMMIT if pre-authorized
send to user's own verified address       -> AGENT_COMMIT_REQUIRED
send to a known allowlisted recipient     -> AGENT_COMMIT_REQUIRED or USER_APPROVAL_REQUIRED
send to a new/external recipient          -> USER_APPROVAL_REQUIRED
send bulk mail or sensitive attachment    -> USER_APPROVAL_REQUIRED or DENY
provider outcome unknown                  -> RECONCILIATION_REQUIRED
```

The actual policy belongs to the Host/user configuration and must be tested independently from generated Python.

## Relationship to V1

V1 remains read-only. This design must not expand the initial runtime milestone.

A future implementation should begin only after V1 proves:

1. Host-enforced capability grants;
2. real denial of ambient network, filesystem, environment, and process authority;
3. hard wall-time, memory, output, and call budgets;
4. Host-authored receipts;
5. healthy-instance reset/discard behavior;
6. one real read-only end-to-end Agent workflow.

The first Effect Plane slice should then implement a fake/local provider plus durable journal and denial tests before connecting a real email account. A real provider test requires explicit user approval and must use a designated test recipient.

## Required future evidence

Before claiming safe external-effect handling, test:

- same-run COMMIT denial;
- Agent COMMIT with exact digest and later-turn grant;
- forged/expired/mismatched user approval denial;
- changed intent after approval forces reapproval;
- stable identity across crash/retry;
- duplicate apply prevention;
- crash before dispatch;
- timeout after possible provider acceptance;
- reconciliation blocks commit/retry;
- playback of `APPLIED` returns a receipt without provider traffic;
- partial batch outcomes;
- receipt and journal tamper detection;
- provider credential absence from Guest memory, logs, and receipts.
