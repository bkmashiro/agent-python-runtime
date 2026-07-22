# Local operator CLI

`apyrun` is the single local/development entry point for the verified Agent Python Runtime artifact. It is not a hosted service, a generic Agent harness, or evidence of production sandboxing.

## Build and invoke

```bash
go build -o /tmp/apyrun ./cmd/apyrun
/tmp/apyrun \
  -artifact /path/to/verified/agent-python-runtime.wasm \
  -config ./operator.json \
  < run-request.json
```

`-artifact` is required. `-config` is optional; omitting it uses bounded defaults and grants no capability.

A successful invocation writes exactly one JSON response and a newline to stdout. Diagnostics go to stderr. Exit status `2` means invocation, config, artifact, or RunRequest input was rejected; exit status `1` means initialization, execution, bounds enforcement, identity generation, or output failed.

## Authority boundary

`run-request.json` is untrusted guest/model data and follows `abi/v1/run-request.schema.json`. It may contain only `run_id`, generated `code`, `inputs`, and an optional output schema. It cannot carry targets, URLs, headers, credentials, grants, timeout, memory limits, request/response budgets, or receipt identity.

`operator.json` is separate Host-owned policy. The CLI generates a cryptographically random Host run identity for receipts; the request's `run_id` is only an untrusted label and cannot become receipt identity.

Do not commit an operator config containing credentials. The CLI does not print config contents or header values in diagnostics.

## Operator config

All fields are optional unless `fetch_many` is present. Unknown fields and trailing JSON are rejected.

```json
{
  "timeout_ms": 20000,
  "max_request_bytes": 1048576,
  "max_response_bytes": 1048576,
  "memory_limit_pages": 8192,
  "fetch_many": {
    "max_calls": 2,
    "max_requests_per_call": 8,
    "max_total_requests": 16,
    "max_response_bytes": 1048576,
    "per_request_timeout_ms": 5000,
    "targets": {
      "catalog": {
        "base_url": "https://catalog.example",
        "headers": {
          "Authorization": "Bearer <HOST-OWNED-CREDENTIAL>"
        }
      }
    }
  }
}
```

The Host maps the opaque guest target `catalog` to the configured origin. A target base URL must be an HTTPS origin with no path, query, fragment, or user info. Plain HTTP is accepted only for explicit IP-loopback test fixtures. Redirects are not followed. V1 performs GET only.

`fetch_many` limits are additionally constrained by compiled hard ceilings. The CLI rejects the entire operator config before compiling the guest if any resource or capability bound is invalid.

## Minimal request

```json
{
  "run_id": "untrusted-label",
  "code": "result = inputs['left'] + inputs['right']",
  "inputs": {
    "left": 19,
    "right": 23
  }
}
```

The response retains the normal strict execution envelope. If Host capabilities were used, Host-authored receipts and capability-call metrics overwrite any guest claims before stdout is written.

## Verification boundary

`go test ./cmd/apyrun` covers strict config decoding, authority separation, response bounds, stream behavior, lifecycle, and credential-safe diagnostics. `TestOperatorCLIWithRealGuestArtifact` builds the actual binary and exercises no-grant execution, Host-granted localhost fetch with a Host-owned credential, Host receipt identity, and timeout against the exact artifact selected by `AGENT_RUNTIME_GUEST`.
