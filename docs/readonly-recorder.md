# Unknown read-only recorder

**Status:** Current, bounded local recorder.

The package `verification/readonlyrecorder` accepts a Host-supplied JSON observation from a dynamic web or CLI surface, canonicalizes it, and derives an untrusted shape candidate for later review.

It deliberately has no browser, shell, credential, tool-registry, or commit API. Capture is therefore observation-only by construction.

## Capture pipeline

1. Require an explicit source kind (`web` or `cli`), locator, and JSON payload.
2. Bound payload size, nesting depth, and decoded node count.
3. Reject duplicate JSON object keys.
4. Reject normalized sensitive-key fragments—including prefixed forms such as `X-API-Key` and `aws_secret_access_key`—plus conservative secret-like string values and secret-bearing locator assignments/arguments.
5. Canonicalize the JSON with numbers preserved as JSON numbers.
6. Retain the canonical payload only in the local recording and bind it to SHA-256 digests.

The source locator is not retained; only a source-identity digest is stored.

## Inferred contract candidate

Inference verifies recording integrity and emits only:

- source kind and source-identity digest;
- sample payload digest;
- escaped JSON paths and observed JSON type sets;
- `trust: untrusted`;
- a fixed authority ceiling:

```json
{
  "read_only": true,
  "credentials": false,
  "tool_exposure": false,
  "commit": false
}
```

Sample values are not copied into the candidate. Array members are merged under a wildcard segment; literal `*` object keys use the private `~2` escape so they cannot collide with that wildcard.

`ContractCandidate.Validate` rejects missing root shape, malformed paths, sensitive paths, or any attempt to increase authority. The candidate cannot become a credential grant, tool definition, or commit policy merely because a new field was observed.

## Drift detection

`DetectDrift` compares candidates for the same source identity and reports:

- added paths;
- removed paths;
- changed observed type sets.

The drift report retains the same read-only authority ceiling. It is evidence for human/Host review, not permission to widen a capability surface.

## Evidence boundary

This fixture demonstrates:

- bounded capture and canonicalization;
- a conservative sensitive-field gate;
- value-free contract inference;
- structural drift detection;
- authority non-escalation.

It does **not** demonstrate:

- autonomous browser or CLI execution;
- complete secret detection;
- semantic correctness of inferred fields;
- automatic tool-schema publication;
- credential resolution or write authority.

Run the focused verifier with:

```bash
go test ./verification/readonlyrecorder -v
go test -race ./verification/readonlyrecorder
```
