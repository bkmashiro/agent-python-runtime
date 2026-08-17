# Lab release-readiness experiment recording

Status: one public scripted multi-agent fixture recorded successfully with a real Wazero Guest on macOS.

## Scenario

`dev-release-readiness` assesses a Python runtime SDK release. The parent Guest produces the release decision; two fresh child Guests produce `dependency-review.md` and `release-checklist.md` in sibling-private workspace branches.

## Direct run

- source commit: `d6d72c66c738f6f906a35bd78e9f885bd286ee75`
- treatment: `all`
- events: 37
- Host/Guest lanes: 4
- elapsed: 14.857 s
- Guest artifact SHA-256: `a443042fb080d22f8e352aca0d0c8a5c87a7801e8afcc603e174d75fbe11c69b`
- corpus SHA-256: `ed1bd1b525484d19ec46902801afb286261aa1deecc285bfc1d4dd8d2ab56584`
- report SHA-256: `ea2a1e6e4b8934f502a5a4cae50377ca0d9ec4950f8502453fc2e534e5b0041a`
- body-capture SHA-256: `4e51bf4c457e093f7384e559ee6467aa10bbcc46805149df4ce3da112fbf342e`

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
