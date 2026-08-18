# Pysolate Lab Web

Static mechanism debugger for the unified Saturday day-trip campaign.

- **Mechanisms** groups the source-prefix, Host request, exact-claim, fresh-Guest, workspace, sharing, retention, and fail-closed Runtime evidence.
- **Timeline** joins the complete recorded Harness trajectory to the Runtime causal ledger: Main planning, Brighton and Oxford candidate generation, Main selection, Main final response, candidate Guests, capsule resume, and the fresh Main Guest.
- **Harness trajectory** publishes all 14 recorded trace events, all four model context/body/output triplets, provider response metadata, token usage, recorded reasoning, parsed outputs, and the final Harness result. Credential material is neither recorded nor published.

The Runtime projection and Harness trajectory are independently content-addressed and build-pinned, then cross-validated against candidate source, selected branch, and final total.

```bash
npm run data:unified
PRIVATE_PROVIDER_DEBUG=/path/to/recorded-harness-trace.json npm run data:provider-summary
npm test
npm run build
npm run test:e2e
```

`provider-debug.json` remains a tombstone for the retired legacy URL. The complete trajectory is served through the content-addressed `provider-summary-<sha256>.json` asset referenced by `src/providerSummaryIdentity.ts`.
