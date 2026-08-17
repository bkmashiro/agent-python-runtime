# Lab release-readiness experiment recording

Status: one public scripted multi-agent fixture recorded successfully with a real Wazero Guest on macOS.

## Scenario

`dev-release-readiness` assesses a Python runtime SDK release. The parent Guest produces the release decision; two fresh child Guests produce `dependency-review.md` and `release-checklist.md` in sibling-private workspace branches.

## Direct run

- source commit: `e6eb50fb35d53f90d70a478da1633d4d9de24d70`
- treatment: `all`
- events: 37
- Host/Guest lanes: 4
- elapsed: 14.891 s
- Guest artifact SHA-256: `a443042fb080d22f8e352aca0d0c8a5c87a7801e8afcc603e174d75fbe11c69b`
- corpus SHA-256: `41a06f50325e5e980cc295c3d4f3373cb85873cadcdbb11bdc13e2dbbea1a8a9`
- report SHA-256: `58cd7dc40aacb903ddf7eb55554a07cd0ad7f73a52ef09a5571545bdd28abd8d`
- body-capture SHA-256: `90cffbb02533fabd40a89f7b71dfb107eedd44aa6166c1aee6688356e03dedb2`

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
