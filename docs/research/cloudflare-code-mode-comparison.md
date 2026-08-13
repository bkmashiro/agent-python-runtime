# Cloudflare Code Mode comparison reset

Status: **source review, 2026-08-13**. This document compares the current
Pysolate repository with Cloudflare's public Code Mode documentation available
on that date. It does not claim anything about private Cloudflare systems or
future releases.

## Why this reset exists

Generated code in a sandbox, typed connectors, durable call logs, approval,
replay, and compensation are no longer a credible novelty claim by themselves.
Cloudflare Code Mode publicly documents all of those mechanisms. Pysolate must
not position itself as merely a Python implementation of that bundle.

## Pinned public Cloudflare facts

The reviewed official documentation states that:

- a model writes code that composes connectors in an isolated executor;
- each Dynamic Worker execution pass is transient;
- connector calls cross the sandbox boundary through RPC and are intercepted by
  the durable runtime before connector execution;
- the runtime stores execution records, connector-call logs, approvals, and
  snippets in Durable Object SQLite storage;
- approval aborts the current pass; a later pass reruns the same source and
  returns recorded results for calls already marked applied;
- rollback walks applied connector calls in reverse order and invokes available
  connector `revert` implementations;
- methods without `revert`, missing connectors, and failed reverts may leave
  effects applied;
- Cloudflare explicitly describes rollback as compensation, not database
  transaction isolation;
- durable fibers distinguish interrupted work from automatically replayable
  closures and leave resume/compensate decisions to application logic.

Sources:

1. Cloudflare, [How Code Mode works](https://developers.cloudflare.com/agents/tools/codemode/how-it-works/), reviewed 2026-08-13.
2. Cloudflare, [Code Mode](https://developers.cloudflare.com/agents/tools/codemode/), reviewed 2026-08-13.
3. Cloudflare, [Durable execution with fibers](https://developers.cloudflare.com/agents/runtime/execution/durable-execution/), reviewed 2026-08-13.
4. Cloudflare, [Code Mode: the better way to use MCP](https://blog.cloudflare.com/code-mode/), reviewed 2026-08-13.

## Mechanism matrix

| Mechanism | Cloudflare public surface | Pysolate Current | Pysolate Proposed | Positioning consequence |
|---|---|---|---|---|
| Model-authored code composes tools | Yes, TypeScript Code Mode | Yes, Python Guest plus generated proxies | — | Commodity programming model |
| Isolated transient executor | Dynamic Worker per execution pass | Fresh CPython/WASI Guest per Run | Other backends may conform | Substrate choice is not novelty |
| Mediated connector/tool boundary | RPC intercepted by runtime | `agent_runtime_v1.host_call` through sealed Broker | Common contract across backends | Mediation alone is commodity |
| Durable call ledger | Durable Object SQLite call records | Compact receipts and bounded playback records; no complete durable effect ledger | Complete operation/attempt record | Do not claim Current parity |
| Approval and resumed execution | Abort then rerun with applied-call replay | Not implemented for external writes | Exact-intent approval in a later Run | Approval alone will not differentiate |
| Replay of prior calls | Applied calls return recorded results | Scoped read-only playback and Experimental branches | Capability-specific treatment | Must qualify what is replayed |
| Rollback/revert | Reverse-order connector compensation | No external-write rollback surface | Qualified compensation only | Never market generic rollback |
| External-effect ambiguity | Stale/running and interrupted states documented; provider truth remains connector-specific | No real external-write adapter | Ambiguous dispatch plus reconciliation | Potential proof target, not Current |
| Filesystem state | Connector/resource dependent | Host-owned `/workspace`, per-Run `/tmp`, Capsules, direct writes | Private attempt overlay with commit/discard/freeze | Current workspace is not transactional |
| Frozen per-execution authority identity | Connector set recorded per execution | Canonical specs, opaque grants, handler identities, budget, sealed plan digest | Delegation/revocation semantics | Strong Current mechanism; competitive uniqueness not yet proved |
| Artifact/profile identity | Not assessed by this review | Guest artifact, manifest, profile and request binding | Cross-backend qualification | Candidate contribution only as a measured conjunction |
| Backend-neutral capability conformance | Not assessed by this review | One Wazero implementation path | Same capability/effect contract across constrained backends | Must be implemented before claiming |

`Not assessed` means the reviewed public sources did not establish the fact. It
does not mean Cloudflare lacks it.

## Claims retired by this review

Pysolate must not use any of these as a standalone differentiator:

- “Agents compose tools by writing sandboxed code.”
- “Tool calls cross a controlled boundary.”
- “The runtime records a ledger.”
- “Approvals resume a code workflow.”
- “Recorded calls can be replayed.”
- “Rollback walks previous operations and calls revert handlers.”
- “Python rather than TypeScript is the systems contribution.”

## Candidate non-commodity conjunction

The remaining thesis candidate is a conjunction, not a feature checklist:

```text
fresh/disposable computation
× immutable identity-bound per-Run authority
× private attempt state with explicit terminal disposition
× Host-owned external-effect operation/attempt truth
× ambiguity that blocks blind retry
× artifact/profile/handler/policy verification
× one capability contract across execution backends
```

This conjunction is **Proposed** until the attempt, effect, reconciliation, and
cross-backend conformance pieces are implemented and tested together. Current
Pysolate proves only a subset.
