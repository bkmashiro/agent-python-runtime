# Canonical pre-dispatch capability contract v0

Status: **Track B complete**

This contract is the Host-owned half of the first semantic legality question. It does
not prove that a Python call is necessarily reached and does not authorize execution
on its own.

## Capability plan v5

`capability.Spec` keeps `ReadOnly` and `Idempotent`, removes the undifferentiated
`SpeculativeSafe` bit, and optionally carries one `PreDispatchContract`:

```text
resource.namespace
resource.argument XOR resource.constant
freshness = plan_epoch
unclaimed = discard_with_disposition
```

Presence is accepted only for a pure, workspace-read or external-read capability with
an exact Python projection and the full `read_only + idempotent` conjunction. An
argument selector must name an exact projected argument. Unknown values, incomplete
contracts, write-class effects and partially asserted metadata fail registration.

The contract means:

- **resource:** one bounded logical read resource can later be instantiated from a
  canonical call argument or a Host-authored constant;
- **plan epoch:** the observation is valid only under the exact frozen plan freshness
  epoch and expiry already carried by staged-observation identity;
- **discard with disposition:** an unclaimed physical request may not become a logical
  call or a durable cache hit; it must end with an existing typed cancelled, late or
  orphaned disposition.

The complete contract is canonical JSON inside sealed capability-plan v5 identity.
Changing a nested resource selector changes the plan digest. `Plan.Specs` and
`Plan.PreDispatch` return defensive values.

## Minimal admitted fixtures

No production built-in is annotated in v0. `PlaybackCaptured` permits transport
evidence capture, but it does not create a Host-frozen plan-epoch snapshot; the current
exact JSON source handlers may fetch a fresh response on each call.

The contract is exercised only by a checked-in capability test fixture and the Track A
research fixture plan. Those fixtures verify canonicalization, identity, defensive
copying and presentation-surface isolation. They are not production legality claims.
Mutable workspace reads, Git reads, live sources, writes and unknown capabilities
remain without a pre-dispatch contract.

## Authority boundary

`Plan.PreDispatch` and `StreamingObservationBinding` expose qualification only from a
sealed Host plan. Python projections and tool schemas remain presentation surfaces;
they cannot create or widen a contract. In particular, capability metadata no longer
populates the legacy streaming `_stream_eager_calls` map: a real-Guest regression test
proves a qualified call dispatches only when unchanged Python reaches it, while an
unreachable or invalid-suffix call dispatches zero requests. A future legality engine
must still prove the exact call occurrence, canonical arguments, authority binding,
control/exception reachability, resource non-conflict and claim identity. Until then
no new runtime request is started.

## Verification

The full Go suite, focused race tests and vet pass locally. The real target-Guest
streaming regression passes with artifact
`a62ae62b13a502152673e1c40c7bee80412d1724302bf8922eb7e3d86ce70473`.
Cross-compiled ARM64 `runtime/capability` and `runtime/streaming` test binaries also
pass on Linux `6.12.0-202.76.4.1.el9uek.aarch64`; their checked SHA-256 values were
`f27ebbd6cf2e9ed161e8c8965ed0ece0483b5e3133d88bfbe8a1fa13673ddbc7` and
`fb90e9cdb1d52de89a01881a1643338422af2122b43dd486a58854b4682d9846`.

## Rejections deferred by design

v0 deliberately has no generic resource algebra, multi-resource set, coalescing,
determinism, arbitrary freshness mode, retry policy, exception transformation or
backend selector. Those fields would not answer the measured first question and would
invite unsupported claims. Missing metadata remains maximally conservative.
