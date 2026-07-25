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
  "prepared_capacity": 1,
  "transaction_journal_path": "/absolute/private/path/transactions.db",
  "fetch_many": {
    "max_calls": 2,
    "max_requests_per_call": 8,
    "max_total_requests": 16,
    "max_concurrency": 4,
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

`prepared_capacity` is an optional wazero-adapter optimization. `0` preserves synchronous fresh initialization. Values `1`–`4` preinitialize that many never-served instances; each checkout serves exactly one Run and is then closed, while a miss falls back to the synchronous fresh path. The exact current artifact starts at 128 MiB of guest memory per candidate, so capacity remains Host-owned and hard-capped. This is not snapshot/restore and does not change the reported `fresh-instance` safety mode.

`transaction_journal_path` is an optional clean absolute path to the Host-owned SQLite/WAL transaction journal. With it, the production CLI creates a workflow transaction before Guest execution, journals every admitted `fetch_many` call as an operation+attempt, finalizes successful Runs, and supports reopen inspection with `apyrun-ledger`. Without it, the same Registry/Coordinator path uses `MemoryLedger` for backward compatibility and must not be presented as durable evidence. The journal rejects symlinks and uses private file permissions.

The Host maps the opaque guest target `catalog` to the configured origin. A target base URL must be an HTTPS origin with no path, query, fragment, or user info. Plain HTTP is accepted only for explicit IP-loopback test fixtures. Redirects are not followed. The production CLI ignores ambient proxy settings, validates all DNS answers at dial time, rejects mixed or non-public answers, and dials a validated IP directly. Private-network targets and DNS names resolving to loopback are not supported. V1 performs GET only.

`fetch_many` limits are additionally constrained by compiled hard ceilings. `max_concurrency` is Host-owned, capped at 16, and limits each fixed input-order wave; byte admission and receipts remain ordered after every wave joins. The legacy Python call envelope is Host-adapted into a sealed builtin Registry entry, so Guest code cannot choose the catalog digest or handler version. Bound receipts retain per-request digests and add transaction, operation, attempt, catalog, handler, effect, and policy identity. The CLI rejects the entire operator config before compiling the guest if any resource or capability bound is invalid.

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

The response retains the normal strict execution envelope. Before committing the workflow, the Host strictly validates the envelope and the requested `output_schema`. Guest error, timeout/cancellation, invalid output, or failed transaction finalization triggers bounded Host abort; compensation is never automatic unless the Host configured the relevant adapter policy. If Host capabilities were used, Host-authored receipts and capability-call metrics overwrite any guest claims before stdout is written.

## Verification boundary

`go test ./cmd/apyrun` covers strict config decoding, authority separation, response bounds, stream behavior, lifecycle, SQLite reopen, and credential-safe diagnostics. `TestOperatorCLIWithRealGuestArtifact` builds the actual binary and exercises no-grant execution, Host-granted localhost fetch with a Host-owned credential, transaction-bound receipts, committed-journal reopen, and timeout against the exact artifact selected by `AGENT_RUNTIME_GUEST`.
