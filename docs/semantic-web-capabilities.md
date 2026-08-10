# Semantic web capabilities

## Boundary

`browser_runtime` means a full interactive browser environment: page rendering, DOM access, JavaScript execution, browser/session state, downloads, and UI automation. Pysolate does not provide this runtime and reports it as an unsupported requirement.

`web_search` and bounded `web_fetch` are different. They are Host-owned typed capabilities projected into a Run through the per-Run Broker. They do not grant Guest Python a socket, URL opener, DNS API, browser session, cookie jar, credential, or ambient network.

```text
Guest Python
  -> typed web_search / web_fetch call
  -> frozen per-Run Broker grant
  -> Host policy and budget
  -> qualified provider adapter
  -> bounded result plus Host evidence
```

These tool names do not belong in `RunRequest.requirements`. A task that can be completed with a granted semantic web tool remains an L1 Pysolate workload. A task that requires rendering, DOM/JavaScript, login/session UI, or interactive browser automation declares `browser_runtime` and is reported for upper-layer escalation.

## Current fetch foundation

The existing `fetch_many` capability is a bounded Host-mediated fetch primitive for Host-configured opaque targets. Guest code supplies a target name and relative path, not a credential or unrestricted network endpoint. The production CLI:

- maps the opaque target to a Host-configured HTTPS origin;
- does not follow redirects;
- ignores ambient proxy settings;
- validates DNS answers at dial time and rejects private/non-public destinations;
- dials the validated address directly;
- bounds calls, concurrency, request count, response bytes, and timeout;
- keeps Host headers and credentials outside Guest memory;
- emits Host receipts.

This is evidence for the mediation pattern, not a claim that arbitrary public-web fetch or a search provider is already available.

## `web_search` qualification contract

A production `web_search` adapter must freeze at least:

- provider and handler version;
- query schema and maximum query length;
- source/domain/language/time scope allowed by Host policy;
- maximum result count and total response bytes;
- normalized title, URL, snippet, source, and publication/observation time fields;
- provider provenance and per-result citation/readback fields;
- call/time/cost budgets;
- timeout, cancellation, provider error, and partial-result semantics;
- audit retention and secret-redaction rules.

Search results are untrusted content. Provenance says where a result came from; it does not make result text trusted instructions.

## `web_fetch` qualification contract

A public-web `web_fetch` adapter must freeze at least:

- destination scope: opaque target allowlist or explicit URL policy;
- allowed schemes, methods, ports, headers, and request-body size;
- redirect count and cross-origin redirect policy;
- DNS rebinding, loopback, link-local, private-address, and metadata-service denial;
- response compressed/decompressed byte limits and accepted MIME types;
- timeout, cancellation, truncation, and partial-response semantics;
- cache and credential policy;
- request identity, receipt, and source URL provenance.

A nominal GET is not automatically harmless: it can trigger metering, tracking, cache changes, or server-side actions. Effect classification and commit policy remain Host-owned and adapter-specific.

## Non-goals

Semantic web tools do not imply:

- ambient Guest networking;
- arbitrary URL access;
- browser rendering or DOM/JavaScript execution;
- cookie/session inheritance;
- bypass of capability grants or approval policy;
- fallback routing or VM execution.
