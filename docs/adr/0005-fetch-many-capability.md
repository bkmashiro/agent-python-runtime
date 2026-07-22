# ADR 0005: Read-only `fetch_many` capability

- Status: Accepted direction; Host broker core implemented
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
- aggregate admitted response bytes;
- per-request timeout;
- exact target aliases and origins.

HTTPS is required except for explicitly granted loopback HTTP fixtures. The built-in HTTP transport performs GET only, injects Host headers, does not follow redirects, and streams through a byte limit.

A batch-level accepted call returns ordered structured items. Individual items may be `ok`, `denied`, `error`, or `timeout`; one failure does not erase other results. Invalid envelopes or exhausted call-level budgets fail before provider execution.

## Receipts

Every admitted or item-level denied operation emits bounded Host-authored evidence. Receipt identity binds:

```text
Host run identity
+ call ID
+ capability
+ operation index
+ target digest
```

Outcome and response size do not change operation identity. Raw target URLs, query strings, and credentials are not stored in receipts; only a SHA-256 target digest is stored. Guest JSON cannot inject receipts into the Host ledger.

## Current implementation boundary

`runtime/capability` and `runtime/receipt` implement and test the Host broker, local HTTP transport, policy, budgets, partial failures, and receipt identity. `abi/v1/fetch-many-arguments.schema.json` and `fetch-many-result.schema.json` freeze the typed payloads.

The guest-to-Host import and Python `agent_runtime.tools.fetch_many` bridge are not covered by this ADR's implemented-status claim until a real artifact E2E passes.

## Consequences

- The Host can execute multiple reads without exposing raw network authority.
- Redirect following cannot silently widen the target grant.
- Credential injection remains Host-side.
- Target aliases make fixtures and provider origins replaceable without changing generated-code authority.
- Arbitrary methods, bodies, vendor SDKs, nested model calls, writes, and runtime package installation remain out of scope.
