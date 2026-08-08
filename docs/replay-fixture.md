# Controlled deterministic replay fixture

**Status:** Current, bounded verification fixture.

The package `verification/replayfixture` provides a fully local state-machine fixture for qualifying replay evidence without contacting a provider, filesystem, network service, or Runtime engine. It belongs to the Host-side Verification plane and does not expand Pysolate Runtime authority.

## Frozen inputs

A recording owns and hashes all values that can influence the fixture:

- exact declarative fixture artifact identity;
- authoritative initial-state snapshot;
- ordered typed inputs;
- ordered UTC clock tape;
- ordered random-value tape;
- resulting receipts and final state;
- transcript and recording digests.

The tape must contain exactly one clock and random value per input. Missing entries, duplicate step identities, unsupported operations, non-UTC or decreasing clock values, and digest mismatches fail closed.

## Qualified levels

### R1 — input-injection replay

`ReplayInputInjection` first validates the fixture artifact identity and complete recording digest, then restores the recorded initial state and injects the recorded input, clock, and random tapes. It re-executes the pure in-memory state machine and requires exact receipt and final-state equality with the recording.

The report exposes recording, transcript, and initial-state digests plus explicit input/clock/random/restoration checks.

### R2 — state-equivalent replay

`ReplayStateEquivalent` includes all R1 checks and additionally compares the observed final state with a separately supplied authoritative final-state oracle. A matching replay returns `state-equivalent`; a mismatch returns `ErrStateMismatch`.

## Evidence ceiling

This fixture establishes R1/R2 only for its controlled deterministic state machine. It does **not** qualify:

- arbitrary Pysolate executions;
- provider or model replay;
- real external effects;
- authenticity of an artifact supplied by an untrusted producer;
- metadata-only `agenttrace.Playback`, which remains R0 structural-only.

External-effect replay requires adapter-specific idempotency, readback, and reconciliation evidence.

## Verification

```bash
go test ./verification/replayfixture -v
go test -race ./verification/replayfixture
go vet ./verification/replayfixture
```
