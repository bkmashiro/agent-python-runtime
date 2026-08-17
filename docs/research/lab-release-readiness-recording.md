# Lab release-readiness experiment recording

Status: one public scripted multi-agent fixture recorded successfully with a real Wazero Guest on macOS.

## Scenario

`dev-release-readiness` assesses a Python runtime SDK release. The parent Guest produces the release decision; two fresh child Guests produce `dependency-review.md` and `release-checklist.md` in sibling-private workspace branches.

## Direct run

- source commit: `aa426388df958e002444b64fe40f6c771704f6f0`
- treatment: `all`
- events: 37
- Host/Guest lanes: 4
- elapsed: 14.775 s
- Guest artifact SHA-256: `a443042fb080d22f8e352aca0d0c8a5c87a7801e8afcc603e174d75fbe11c69b`
- corpus SHA-256: `18474583bac875d94cb40e585f6b444e4bb16d931f6d4cc0e6cf23fc519b4606`
- report SHA-256: `157cdb0b9885ba8c778bd81836258cf866a309fe44a08f6f3bc152e00c688ad0`
- body-capture SHA-256: `aa747974a94b42225fbdabc21f0b9f18d0c1a1c0cdb986926babe8c426b34f95`

The direct test passed with real parent/child Guest execution and two recorded workspace changes.

## Body-complete research mode

The Harness configures the normally-nil `FreshRunnerExecutor.Observer` only for this research capture. Parent text comes from the real parent Guest response; child text comes from validated child Guest response defensive copies; ...[truncated]

- task context body;
- all 37 Host/runtime event envelopes with original parent-sequence lineage;
- all three Agent Python source bodies;
- parent workflow result body;
- dependency-reviewer result body;
- release-reviewer result body.

Each published output body must match the recorded output digest. Child output bodies must additionally match their workspace-change path, digest, and byte size. The scripted fixture records `model.output` as `not_recorded`; it does not impersonate a live provider conversation.

Passing `--recording-root /absolute/path` retains the private content-addressed store and append-only trace. Without the flag, generation uses a temporary private recording and removes it after validation. The checked-in Lab projection is an explicit public-fixture export, not a general private-body export path.

`AGENT_RUNTIME_GUEST=/absolute/pinned-guest.wasm scripts/record-lab-release-readiness.sh` records a fresh candidate under `~/.hermes/evidence/pysolate/lab-release-readiness-candidate` with `0700`/`0600` permissions. It deliberately does not overwrite pinned public evidence or projector anchors; promotion remains a reviewed step.
