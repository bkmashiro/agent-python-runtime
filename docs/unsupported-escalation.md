# Structured unsupported and escalation outcome

## Status

**Current for explicit preflight requirements.** Pysolate does not inspect Python exception text to infer compatibility needs and does not launch or select a fallback backend.

## Contract

`RunRequest.requirements` is an optional, untrusted compatibility declaration. It is not a capability grant and cannot authorize network, credentials, filesystems, processes, or any Host resource.

The separate optional `RunRequest.compatibility` object declares a named artifact profile and import roots. A profile mismatch returns `profile_unsupported`, not this escalation outcome. See [Execution profile admission](profile-admission.md).

Accepted values are:

- `browser_runtime` (page rendering, DOM/JavaScript, browser session state, and UI automation);
- `daemon`;
- `dynamic_package_install`;
- `native_extension`;
- `native_threads`;
- `posix`;
- `shell`;
- `subprocess`.

The list is bounded to eight unique values. Unknown values, duplicate values, `null`, duplicate JSON keys, and authority-bearing fields fail request validation.

Current Pysolate intentionally supports none of these ambient/native feature classes. A non-empty valid list is rejected before:

1. artifact read and Factory construction in the local CLI;
2. required execution trace start in the Hermes bridge;
3. workspace Run admission;
4. per-Run capability Broker creation;
5. Guest checkout or execution.

The Wazero Runner repeats the admission check so callers that bypass the CLI or Hermes bridge cannot silently execute a request whose declared requirements were not handled.

## Host-authored outcome

A rejected request produces `execution-outcome.schema.json`:

```json
{
  "schema_version": 1,
  "kind": "runtime_unsupported",
  "escalation_required": true,
  "escalation_reason": "required_features_unsupported",
  "required_features": ["browser_runtime", "posix"],
  "workspace_disposition": "not_started",
  "effect_disposition": "not_started",
  "evidence": {
    "request_sha256": "sha256:..."
  }
}
```

Required features are sorted before emission. The SHA-256 digest binds the exact internal `RunRequest` bytes admitted by that Host interface. The outcome contains no VM target, route directive, credential, Host path, retry command, or claim about an external backend.

`web_search` and bounded `web_fetch` are Host semantic capabilities, not runtime requirements. If the Host grants one through the per-Run Broker, the request remains an L1 Pysolate workload. They do not provide rendering, DOM/JavaScript, browser session state, or ambient network. See [Semantic web capabilities](semantic-web-capabilities.md).

The local CLI writes this JSON to stdout and exits with status `3`. The Hermes bridge returns an error response with `error.code = "runtime_unsupported"` and the same typed object in `outcome`. Neither path starts Guest execution.

## Classification firewall

Only the typed Host admission error can create this outcome. None of the following are compatibility escalation:

- Python syntax or runtime exception;
- `ImportError` text produced by Guest code;
- capability denial;
- approval pending;
- timeout, cancellation, OOM, or resource exhaustion;
- invalid output;
- provider/tool failure;
- `reconciliation_required` or any ambiguous effect.

The runtime never parses error strings for words such as “POSIX required.” This prevents generated code or a provider message from manufacturing an escalation signal.

## Upper-layer boundary

The outcome means only:

> Pysolate did not start this request because its declared requirements are outside the bounded runtime contract.

The legacy ABI field `escalation_required` means that the caller must select another execution profile before starting work. It does not mean Hard escalation, whole-program VM replay, workspace transfer, cross-runtime continuation, or automatic retry. Once a Run starts, Pysolate does not emit a backend-escalation outcome.

An upper layer may independently choose a compatibility backend. Pysolate and Vinculum do not select, start, configure, test, migrate to, or audit that backend. Its availability and authority contract are outside Pysolate implementation and evidence claims.

## Verification

Coverage includes:

- strict request and outcome decoding;
- schema-valid and schema-invalid fixtures;
- deterministic feature sorting and request digest binding;
- ordinary-error non-escalation;
- Wazero admission before Broker creation and Guest execution;
- CLI admission before artifact read and Factory construction;
- Hermes bridge admission before trace start and Runner invocation;
- Guest defense-in-depth rejection if a non-empty requirements list bypasses Host admission.
