# Execution profile admission

## Status

**Current for explicit compatibility manifests and Host-configured profile policy.** This is a conservative declaration check, not a complete Python source analyzer and not yet an artifact-manifest proof.

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

## Validation

Profile IDs use the fixed current vocabulary. Import names must be bounded Python-qualified module names, are unique, and are capped at 64 entries. The Host rejects a declaration when:

- no Host profile is bound;
- the requested profile differs from the Host profile;
- any declared import is absent from the Host allowlist;
- the declaration is `null`, incomplete, duplicated, malformed, or over the bound.

Admission is enforced at three boundaries:

```text
CLI
  decode request + Host config
  -> requirements admission
  -> profile admission
  -> only then read artifact / construct Factory

Hermes bridge
  validate protocol
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
- CLI, Hermes bridge, and Wazero enforce the same admission rule before Guest work;
- legacy requests without a compatibility declaration continue to use the existing path;
- a declaration cannot grant ambient authority.

## What this does not prove

It does **not** prove:

- that declared imports equal imports reachable from arbitrary Python source;
- that dynamic `__import__`, `eval`, `exec`, plugin loading, or data-dependent imports were discovered;
- that syntax-valid source will complete successfully;
- that the Host profile was derived from or cryptographically bound to the selected WASM artifact;
- that an allowlisted module is present and behaves identically in every artifact build;
- that transitive package/native-extension requirements were inferred;
- that a representative workload corpus has high Pysolate admission or completion share.

A request can lie by omitting an import. Runtime availability and Guest restrictions remain fail-closed, but any resulting `ImportError` is an ordinary Guest failure, not backend escalation.

## Next qualification step

The next profile-admission step is artifact-bound verification:

```text
verified artifact manifest
+ artifact SHA-256
+ profile ID
+ Python version
+ selected package/native-module closure
+ allowed import roots
+ catalog identity
-> Host-frozen profile evidence
```

Only after that binding exists should documentation claim that a Host profile describes the exact loaded artifact. Deeper source compatibility checks should remain conservative and versioned; lack of a detected problem is not a proof that arbitrary dynamic Python cannot access an undeclared path.
