# Pysolate Lab Web — canonical Lab v1 viewer

A zero-dependency, fixture-backed, read-only Web viewer for the canonical Lab v1 JSON produced by the Go Lab fixtures. It is designed for the mentor demo path:

> study overview → run detail → capability timeline → branch DAG → workspace diff → comparison

The UI explicitly says **Fixture-backed canonical Lab v1 viewer**, **READ ONLY**, and **not a live Runtime or service**. It does not execute work, mutate evidence, authenticate, call an API, request protected bodies, or claim Runtime/live integration.

## Run locally

From `apps/lab-web`:

```sh
npm test
npm run build
npm run serve
```

No install step is needed. The static Node server binds only to `127.0.0.1`, serves deterministic `dist/`, permits only `GET` and `HEAD`, and defaults to port `4173`:

```sh
npm run serve -- --port 4317
# or LAB_WEB_PORT=4317 npm run serve
```

Demo links use deterministic hash state:

```text
#fixture=ordinary&view=overview
#fixture=branched&view=lineage
#fixture=incomplete&view=timeline
#fixture=truncated&view=workspace
#fixture=private&view=runs
```

## Canonical boundary

`src/canonical/` contains the Go-produced JSON copied byte-for-byte from the
matching Runtime source revision under:

```text
research/labview/testdata/canonical/{ordinary,branched,incomplete,truncated,private}
```

The viewer consumes these canonical objects directly; it does not maintain a second draft-shaped envelope. `scripts/sync-canonical.mjs` deterministically emits an exact JavaScript data module from the tracked JSON so browsers do not depend on JSON-module support; tests and builds fail if that module drifts. The only additional metadata is an internal `__sha256` map used to verify that `lab-index.v1` links match the fixture file digests. The browser never contacts an API or external service.

`src/lib/canonical-adapter.mjs` is a strict, fail-closed adapter. It validates schema markers, exact object fields, digest/link consistency, source identity, enum vocabularies, pagination bounds, event ordering, branch relations, Guest-relative workspace paths, reference privacy/availability, and explicit incomplete/truncated/private states. Protected prompt/code/provider/workspace bodies and Host paths are not copied into fixtures.

The five fixture states are intentionally distinct:

- **ordinary** — complete run and portable object metadata;
- **branched** — parent/child branch edge plus capability/workspace comparison deltas;
- **incomplete** — failed task/oracle with `evidence_incomplete`;
- **truncated** — completed run with canonical page `truncated: true` and a next cursor;
- **private** — portable metadata plus private/unavailable result/workspace references and an explicitly empty timeline/diff.

Task status, oracle status, and evidence completeness are displayed as separate concepts. A digest is equality metadata only, never authority, correctness, authentication, or export permission.

## Build and server boundary

`scripts/build.mjs` copies an allowlisted deterministic source set and writes `dist/manifest.sha256`. `scripts/serve.mjs` is a loopback-only static server with path traversal protection, CSP, `nosniff`, and no external network dependency.

All generated output stays in ignored `dist/` or `.artifacts/` directories. The existing `test/` suite covers canonical adapter behavior, navigation state, responsive/accessibility hooks, dependency boundaries, deterministic build inputs, and server behavior.

## Known limits

- This is a static single-page viewer, not a live Runtime integration, Lab service, API client, execution console, or mutation surface.
- Canonical fixture pages are small and synthetic; they do not establish production scale, performance, determinism beyond the bounded evidence class, or model quality.
- The branch visualization is schematic; the accessible node/edge tables are authoritative.
- Private/unavailable objects remain metadata-only; this viewer cannot make them available.
- No browser automation is required to run the app. If local browser tooling is available, use it only for smoke verification; `npm test`, `npm run build`, and an HTTP probe are the minimum gates.
