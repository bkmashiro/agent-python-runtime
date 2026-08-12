# Playback Bundles

Status: capture and strict offline consumption are **Current** for capturable
curated-source calls. Capability-boundary branching is **Experimental**. A
general effect replay system and automatic cross-version migration are
**Proposed**, not Current.

## Purpose

A Playback Bundle is a Host-protected, canonical artifact containing only the validated capability transcript and the identities required to reject incompatible playback. It is not a Run archive.

The v1 schema is `pysolate.playback-bundle.v1`. Its content identity is a SHA-256 over the canonical payload excluding the identity field itself. That self-identity detects corruption but is not authentication by itself. Offline Host config must independently provide the capture-issued `expected_bundle_sha256`; playback rejects a validly re-authored bundle before Guest startup. The encoded file is canonical JSON and is independently decoded and validated before publication.

## Captured fields

- sealed capability-plan identity and sorted opaque grant identities;
- Host request digest;
- Guest artifact digest and Host execution-profile binding digest;
- initial and final workspace identity digests when a mounted workspace exists;
- expected Host response status and canonical Agent-result digest;
- ordered operation index, capability name, canonical arguments and result payloads plus their digests;
- bounded non-secret transport evidence: adapter kind, accepted status, media type, raw body byte count and body digest.

The result payload in each operation is the schema-validated capability result required for offline playback. The bundle deliberately does **not** contain:

- Agent source, instruction or prompt;
- provider response or credentials;
- endpoint URL, headers or Host source policy;
- final Agent-result body;
- workspace file bodies, mounted Host paths or Capsule payloads.

## Capture publication

Configure capture with an absolute output path:

```json
{
  "playback": {
    "mode": "capture",
    "output_bundle": "/absolute/protected/run.playback.json"
  }
}
```

Capture requires a curated information source. Workspace Capsule output and Playback Bundle output are mutually exclusive, avoiding partially published multi-artifact outcomes.

Offline playback uses the same Host source policy but never constructs the HTTP handler:

```json
{
  "playback": {
    "mode": "playback",
    "input_bundle": "/absolute/protected/run.playback.json",
    "expected_bundle_sha256": "sha256:<capture-issued bundle identity>"
  }
}
```

Before Guest startup, the Host checks the independently supplied bundle identity, plan, grants, request, artifact, execution profile and initial workspace against the bundle. The Broker then matches each operation by index, capability and canonical arguments, revalidates the recorded result against the sealed output schema, produces ordinary receipts and refuses live-handler execution. Missing, extra, reordered, mismatched or unused records make the Run fail. After the fresh Guest exits, the Host verifies response status, canonical Agent-result digest and final workspace identity.

The two Current captured source Specs are `sources.demo_catalog()` and
`sources.benchmark_manifest()`. Offline playback installs the same Spec/Grant
definitions with non-network handlers; it does not turn either source into a
generic JSON or HTTP interface.

Publication occurs only after:

1. the bounded Guest response has been decoded and Host evidence projected;
2. the runner has closed successfully;
3. final workspace identity has been inspected when present;
4. the complete bundle has been authored, canonicalized, validated and staged in the destination directory with mode `0600`;
5. the final response still fits its configured bound.

The Host syncs the staged file, atomically publishes it with a same-directory no-overwrite hard link, syncs the directory, then removes the staging name. Any earlier failure removes the stage and leaves no output bundle.

## Integrity behavior

Decoding rejects invalid UTF-8, duplicate keys, trailing data, unknown fields, non-canonical JSON, malformed identities, duplicate operation indices, invalid transport evidence, request/result digest mismatches and bundle-identity mismatch. Offline admission additionally anchors the decoded identity in Host config, so recomputing the bundle's self-hash cannot authorize rewritten fields. Plan, grant, Host request, response status and final result/workspace relations are checked against the live Host context.

## Experimental capability-boundary branches

The branch schema is `pysolate.playback-branch.v1`. A canonical protected
manifest contains:

- the parent Bundle identity;
- a zero-based `fork_operation` naming the first child suffix operation;
- a digest of the exact parent prefix `[0, fork_operation)`;
- the parent's request, artifact, execution-profile and initial-workspace
  identities;
- the complete sealed child capability-Plan identity and sorted Grant
  bindings;
- one suffix mode and its canonical suffix records where applicable.

The fork must be within the parent's transcript. Branches at arbitrary source
lines or after the final transcript entry are not v1 semantics.

| Suffix mode | Behavior |
|---|---|
| `override` | The Host supplies one or more consecutive canonical alternative results beginning at the fork. The Broker validates arguments, evidence, byte bounds and the selected capability output schema before returning each result. |
| `recorded_suffix` | The Host supplies a complete consecutive alternate captured tape beginning at the fork. Ordinary playback validation still applies. |
| `live_suffix` | The manifest contains no recorded suffix. Calls at and after the fork go only through handlers already sealed into the child Plan, and only Specs declared `external_read` plus `captured` are branch-compatible. |

For every mode, the Broker strictly replays the parent prefix first. A missing,
extra, reordered, duplicate, argument-mismatched or unused record poisons the
branch and makes finalization fail. At least one suffix operation must be
observed. Agent arguments never select the fork, override bytes, endpoint,
mode, Plan or Grant.

The `apyrun` Host config can execute a protected branch:

```json
{
  "playback": {
    "mode": "branch",
    "input_bundle": "/absolute/protected/parent.playback.json",
    "expected_bundle_sha256": "sha256:<trusted parent identity>",
    "input_branch_manifest": "/absolute/protected/child.branch.json",
    "expected_branch_sha256": "sha256:<trusted manifest identity>",
    "output_bundle": "/absolute/protected/child.playback.json"
  }
}
```

The same original request, exact artifact/profile and initial workspace must be
provided. The complete child Plan/Grants are sealed before Guest startup and
must match the manifest. The child starts a new Guest and re-executes the
request from initial state; it does not restore Python locals, heap, module
globals, file descriptors or WebAssembly memory. Its result and final
workspace may intentionally diverge from the parent, so they are captured into
a new child Bundle rather than checked against the parent outcome.

The manifest plus parent/child identities forms the lineage relation. Playback
Bundle v1 itself remains unchanged and does not embed a parent pointer. The
Experimental `research/operator` API can run a fresh child and export bounded
DAG nodes/edges; the local research CLI can author a protected branch manifest
and render caller-supplied manifest/child relations as that DAG. DAG rendering
validates the child's admission identities and Grants, exact parent prefix,
and complete suffix tape for override and recorded-suffix branches. A live
suffix has no sealed result tape, so only its admission bindings and prefix can
be checked. This validates a proposed lineage relation, but is not independent
proof that the child's result was produced by executing the manifest. Branch
execution is not a `pysolate-research` CLI subcommand yet.

Branch decoding applies the same fail-closed principles as Bundle decoding:
invalid UTF-8, duplicate/folded/unknown keys, trailing or non-canonical JSON,
self-identity mismatch, stale parent/prefix/request/artifact/profile/workspace,
invalid fork/suffix indices, malformed Grant bindings and invalid override
evidence are rejected. The independently protected expected parent and branch
identities remain necessary; neither self-hash authenticates its candidate.

## Compatibility and upgrade policy

Bundle v1, branch v1 and Runtime observation v1 are separate exact schemas.
Their common policy is:

1. Readers accept only the named version, exact keys, canonical bytes and the
   documented bounds. Unknown versions or fields are rejected, not ignored or
   case-folded.
2. Adding/removing a field, changing a field's meaning or bound, changing
   canonicalization, or changing transcript/fork semantics requires a new
   schema version and new content identity.
3. Existing protected bytes are never rewritten in place. An upgrade tool must
   decode with the original validator, author a new-version artifact with a new
   identity, retain the original according to policy, and record an explicit
   derivation relation outside the old schema.
4. A trusted expected identity is carried forward only as provenance; it is
   never replaced by an identity calculated from the untrusted upgrade
   candidate during admission.
5. Branch v1 is compatible only with a parent Bundle whose exact v1 transcript
   validates under the semantics used to compute its prefix. A future Bundle
   version requires either a matching branch version or an explicit validated
   conversion. There is no implicit down-conversion or mixed-version prefix.
6. Observation events do not become a Playback tape. A future Lab may relate
   their identities, but migrating one schema never silently upgrades the
   others.

See [research/runtime-observation.md](research/runtime-observation.md) for the
observation-specific upgrade rule and
[research/lab-boundary.md](research/lab-boundary.md) for ownership of future
migration/index services.
