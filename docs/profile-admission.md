# Execution profile admission

## Status

**Current for explicit compatibility manifests, Host-configured import policy, and artifact-bound profile identity.** This is a conservative declaration and distribution-identity check, not a complete Python source analyzer or per-import artifact inventory.

## Contract

A Run may add an untrusted compatibility declaration:

```json
{
  "run_id": "run-1",
  "code": "import json\nresult = json.dumps(inputs)",
  "inputs": {},
  "compatibility": {
    "profile": "base",
    "imports": ["json"]
  }
}
```

The only current profile IDs are the artifact-pipeline names:

```text
base
numpy-core
```

The Host independently freezes an `ExecutionProfile`:

```json
{
  "execution_profile": {
    "id": "base",
    "allowed_imports": ["collections", "json", "math", "statistics"]
  }
}
```

The request cannot add packages, change the artifact, select a Host path, grant a tool, authorize network, or widen the Host allowlist. A compatibility declaration only narrows admission.

For local CLI execution, Host policy and the distribution manifest are configured together:

```sh
apyrun \
  -artifact dist/agent-python-runtime.wasm \
  -manifest dist/manifest.json \
  -config host.json
```

If `execution_profile` is configured, `-manifest` is mandatory; `-manifest` without Host profile policy is rejected. The canonical verifier binds the selected bytes to `artifact_profile`, exact package set, artifact SHA-256, manifest SHA-256, repository commit, ABI, target, compiler target, and reactor execution model. `base` must carry only the CPython core package and no extension profile; `numpy-core` must carry CPython plus the selected-core NumPy package and its bounded extension-profile identity.

## Validation

Profile IDs use the fixed current vocabulary. Import names must be bounded Python-qualified module names, are unique, and are capped at 64 entries. The Host rejects a declaration when:

- no Host profile is bound;
- the requested profile differs from the Host profile;
- any declared import is absent from the Host allowlist;
- the declaration is `null`, incomplete, duplicated, malformed, or over the bound.

Artifact binding additionally rejects duplicate/unknown identity fields, invalid or trailing JSON, profile/package mismatch, wrong canonical filename, size/SHA drift, incomplete build identity, or invalid NumPy extension identity. Artifact and manifest digests are retained in the immutable `ExecutionProfile` and exposed through Runner properties.

Admission is enforced at three boundaries:

```text
CLI
  decode request + Host config
  -> requirements admission
  -> profile admission
  -> read artifact + manifest
  -> verify distribution identity and bind profile digests
  -> only then construct Factory

Hermes daemon / bridge
  read pinned regular artifact + manifest
  -> verify distribution identity
  -> optionally bind Host -profile-imports policy
  -> construct Runner and expose profile/digests in properties
  -> validate protocol
  -> requirements admission
  -> profile admission
  -> only then start trace / call Runner

Wazero Runner
  decode request
  -> requirements admission
  -> profile admission
  -> only then create Broker / acquire Guest execution
```

The Guest bootstrap accepts the manifest only as defense-in-depth request shape. It does not interpret the declaration as authority.

## Result semantics

A profile mismatch is not Hard escalation. The local CLI rejects the invocation with exit status `2`, no stdout payload, and a bounded diagnostic. The Hermes bridge returns:

```json
{
  "status": "error",
  "error": {
    "code": "profile_unsupported",
    "message": "execution profile unsupported"
  }
}
```

No execution trace, `ExecutionRef`, workspace admission, Broker, Guest checkout, or Guest execution starts. The caller may make another placement decision, but Pysolate does not select or invoke another backend.

The existing `runtime_unsupported` outcome remains reserved for the bounded explicit `requirements` vocabulary. Python syntax errors, `ImportError`, capability denial, timeout, OOM, provider failure, or invalid output remain ordinary failures and never become a profile-selection signal.

## What this proves

This Current slice proves:

- a bounded manifest can name one current artifact profile and declared imports;
- Host policy, not Guest input, fixes the admitted import set;
- Host policy can be bound to the exact artifact/profile/package/digest identity before Factory construction;
- the local CLI and pinned Hermes loader share one canonical distribution verifier;
- CLI, Hermes bridge, and Wazero enforce the same admission rule before Guest work;
- legacy requests without a compatibility declaration continue to use the existing path;
- a declaration cannot grant ambient authority.

## What this does not prove

It does **not** prove:

- that declared imports equal imports reachable from arbitrary Python source;
- that dynamic `__import__`, `eval`, `exec`, plugin loading, or data-dependent imports were discovered;
- that syntax-valid source will complete successfully;
- that the v2 distribution manifest enumerates every stdlib/builtin/import root present in the selected artifact;
- that each Host-allowlisted import was individually proven present and behaviorally qualified;
- that transitive package/native-extension requirements were inferred;
- that a representative workload corpus has high Pysolate admission or completion share.

A request can lie by omitting an import. Runtime availability and Guest restrictions remain fail-closed, but any resulting `ImportError` is an ordinary Guest failure, not backend escalation.

## Next qualification step

The remaining profile-depth step is an artifact-generated import/catalog inventory:

```text
verified artifact profile/digests/packages       [Current]
+ artifact-generated stdlib/builtin/import roots [Proposed]
+ selected native-module closure                 [Proposed]
+ frozen tool catalog identity                   [Proposed]
-> fuller Host-frozen profile evidence
```

The Current binding proves which profile/package distribution was loaded, but not that an arbitrary Host import allowlist exactly describes its import surface. Deeper source compatibility checks should remain conservative and versioned; lack of a detected problem is not proof that dynamic Python cannot access an undeclared path.
