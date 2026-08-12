# Playback Bundles

Status: capture is Current; offline consumption is enabled by the subsequent playback stage.

## Purpose

A Playback Bundle is a Host-protected, canonical artifact containing only the validated capability transcript and the identities required to reject incompatible playback. It is not a Run archive.

The v1 schema is `pysolate.playback-bundle.v1`. Its content identity is a SHA-256 over the canonical payload excluding the identity field itself. The encoded file is canonical JSON and is independently decoded and validated before publication.

## Captured fields

- sealed capability-plan identity and sorted opaque grant identities;
- Host request digest;
- Guest artifact digest and Host execution-profile binding digest;
- initial and final workspace identity digests when a mounted workspace exists;
- expected canonical Agent-result digest;
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

Publication occurs only after:

1. the bounded Guest response has been decoded and Host evidence projected;
2. the runner has closed successfully;
3. final workspace identity has been inspected when present;
4. the complete bundle has been authored, canonicalized, validated and staged in the destination directory with mode `0600`;
5. the final response still fits its configured bound.

The Host syncs the staged file, atomically publishes it with a same-directory no-overwrite hard link, syncs the directory, then removes the staging name. Any earlier failure removes the stage and leaves no output bundle.

## Integrity behavior

Decoding rejects invalid UTF-8, duplicate keys, trailing data, unknown fields, non-canonical JSON, malformed identities, duplicate operation indices, invalid transport evidence, request/result digest mismatches and bundle-identity mismatch. Plan, grant, Host request and final result/workspace relations are additionally checked against the live Host context when offline playback is admitted.
