# ADR 0005: Read-only `fetch_many` capability

- Status: Accepted and verified for the V1 read-only slice
- Date: 2026-07-22

## Context

Generated Python needs one compound read primitive without receiving sockets, credentials, arbitrary endpoint authority, or Host policy controls. A guest-supplied URL allowlist is not authority separation, and redirects can escape an otherwise allowed origin.

## Decision

The first capability is named `fetch_many`. Its guest-visible arguments contain only an ordered list of:

```json
{
  "request_id": "weather-1",
  "target": "weather",
  "path": "/v1/current?city=Tokyo"
}
```

`target` is an opaque alias. The Host grant maps it to an origin and optional Host-owned headers. Generated code cannot supply a scheme, host, credential, header, timeout, or budget. Paths containing an absolute URL, network-path reference, user info, or fragment are rejected.

The Host grant bounds:

- capability calls per run;
- requests per call and per run;
- concurrent provider operations per wave;
- aggregate admitted response bytes;
- per-request timeout;
- exact target aliases and origins.

HTTPS is required except for explicitly granted loopback HTTP fixtures. The built-in HTTP transport performs GET only, injects Host headers, does not follow redirects, and streams through a byte limit.

A batch-level accepted call returns ordered structured items. Individual items may be `ok`, `denied`, `error`, or `timeout`; one failure does not erase other results. Invalid envelopes or exhausted call-level budgets fail before provider execution.

Provider operations execute in fixed input-order waves bounded by the Host grant's `max_concurrency` (hard ceiling 16). Workers only produce private candidates. After every wave joins, a single ordered reducer applies the aggregate byte budget, shapes results, and appends receipts in operation-index order. Completion order therefore cannot change which response bodies are admitted or the receipt sequence. Cancellation joins the active wave and prevents later waves from reaching the provider.

A provider call can temporarily hold at most the total response budget; wave execution therefore bounds retained candidate bodies to `max_concurrency × max_response_bytes` rather than launching the entire batch. This is a conservative V1 bound, not streaming multiplexing.

## Receipts

Every admitted or item-level denied operation emits bounded Host-authored evidence. Receipt identity binds:

```text
Host run identity
+ call ID
+ capability
+ operation index
+ target digest
```

Outcome and response bytes do not change operation identity. Raw target URLs, query strings, response bodies, and credentials are not stored in receipts; only SHA-256 request/response digests are stored. Guest JSON cannot inject receipts into the Host ledger.

## Verified implementation boundary

`runtime/capability` and `runtime/receipt` implement and test the Host broker, local HTTP transport, policy, budgets, partial failures, and receipt identity. `abi/v1/fetch-many-arguments.schema.json` and `fetch-many-result.schema.json` freeze the typed payloads.

The guest exposes `agent_runtime.tools.fetch_many` through the sole custom WASM import `agent_runtime_v1.host_call`. The wazero adapter supplies a fresh per-Run broker, validates guest memory ranges, and overwrites guest receipt and capability-call claims with Host-authored evidence. [Guest artifact run 29910838347](https://github.com/bkmashiro/agent-python-runtime/actions/runs/29910838347) built and verified the exact artifact and passed the real localhost provider, missing-grant, receipt, freshness, timeout-recovery, and ambient-authority E2E tests.

## Consequences

- The Host can execute multiple reads without exposing raw network authority.
- Redirect following cannot silently widen the target grant.
- Credential injection remains Host-side.
- Target aliases make fixtures and provider origins replaceable without changing generated-code authority.
- Arbitrary methods, bodies, vendor SDKs, nested model calls, writes, and runtime package installation remain out of scope.
