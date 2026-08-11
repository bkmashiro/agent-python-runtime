# Placement scripted canary stop record (2026-08-11)

## Decision

Stop the six-task scripted-parity lane after the single pre-registered class-wide infrastructure repair did not close the Computer Worker JavaScript compiler surface.

No model phase was started. No sealed decision task was exposed.

## Formal evidence

The initial runner at `e7efe3f849687a23d8d70e91e7582bcb24ef6c76` produced two complete, identity-valid cells for `pl-dev_simple_read_001` before the Computer cell failed:

- Direct: PASS, exact `fixture.get` semantic call and canonical final result;
- profile-qualified Pysolate: PASS in the real WASI Guest, exact call, valid request/response/result digests and canonical final result.

The Computer request was rejected by the adapter before execution because fixture-only tasks encoded `output_files` as JSON `null` rather than an array. The adapter failed closed. No Computer trial artifact was accepted.

## One class-wide repair

Commit `389ffeaf2684d8c31256aa15009319a6756af361` initialized empty output collections as concrete arrays and added a regression test. This was the one repair allowed by the pre-registration.

The rerun progressed through the first task and reached the second Computer workspace task, where Worker JavaScript returned HTTP 500. An isolated local diagnostic against the same pinned checkout and harness showed the authoritative reason:

```text
Module "node:path" is not configured for the worker-javascript backend.
```

The scripted JavaScript compiler imported `node:path` only to compute a parent directory before writing a workspace file. This is a compiler/harness compatibility defect, not a task, model, Cloudflare production, or Pysolate failure.

The repair-rerun command was also supplied an incorrect 40-character `source_commit` value by the operator. Its generated cells are therefore identity-invalid and none are accepted. The runner now verifies the caller-provided source commit against Go VCS build metadata so this cannot recur silently.

## Stop-rule application

The pre-registered plan permits one class-wide infrastructure repair and one rerun. That budget is exhausted. Fixing the JavaScript compiler and rerunning again would require a new pre-registration or an explicit decision to reset the repair budget; doing it silently would invalidate the canary's fail-closed claim.

The obvious future implementation is bounded: remove the `node:path` dependency and derive flat/contained parent paths without expanding authority. It is intentionally not applied in this cohort.

## Evidence boundary

- accepted formal cells: 2 / 18, both PASS;
- accepted Computer cells: 0;
- model/provider calls: 0;
- CLI model tokens: 0;
- sealed decision exposure: none;
- Direct/Pysolate/Computer performance comparison: not available;
- default-placement decision: not available;
- Cloudflare production claims: not supported.

Raw Wrangler logs and temporary requests remain outside Git. The published summary contains only source/artifact digests and sanitized failure classifications.
