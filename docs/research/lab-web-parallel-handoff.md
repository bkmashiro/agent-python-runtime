# Pysolate Lab Web v3 handoff

**Status:** current, latest-only, static and read-only.

## Contract

The browser consumes only:

```text
pysolate.lab-latest.v2
```

There is no compatibility adapter for the former trajectory, campaign, or Lab v1 Web shapes. Historical Go research contracts remain in the repository but are not browser inputs.

## Generation authority

`research/labview.BuildLatestSnapshot` and `research/labview/cmd/project-latest` are the projection authority. They pin accepted evidence for source-prefix overlap, the transparent campaign, semantic pre-dispatch, whole-Run reuse, growable COW, cold-I/O continuation and composable re-evaluation. They validate canonical source, logical/physical joins and terminal outcomes before writing:

```text
apps/lab-web/public/lab-data/latest.json
apps/lab-web/src/latestIdentity.ts
```

Regenerate through the repository script:

```sh
cd apps/lab-web
npm run data:latest
```

Do not hand-edit the snapshot.

## Current views

The UI contains eight mechanism examples:

1. reach-gated source-prefix overlap;
2. semantic pre-dispatch;
3. exact concurrent request sharing;
4. whole-Run retention and single-flight;
5. single-use growable COW memory;
6. cold-I/O pageout with continuation;
7. fresh re-evaluation after a Host wait;
8. a source-mismatch fail-closed control.

Each view is reduced to code, three useful measurements, an explicit `timeline`
or `state_flow` execution view, and three runtime facts. Low-noise `MEASURED`,
`EXPERIMENTAL`, and `CONTROL` labels preserve evidence tier without restoring a
claim-boundary panel. Evidence hashes remain in the snapshot and browser
verification path but are not rendered. The natural 36-event source-prefix
census is no longer a Web input or visible card.

## Safety boundary

Lab Web may render only the body-safe snapshot. It cannot:

- execute Python or replay a Run;
- dispatch a capability or external effect;
- grant authority or publish a workspace;
- resolve private paths, prompts, provider bodies or workspace bodies;
- infer missing durations, source, sharing decisions or causal relations.

Unknown fields and malformed enums fail closed in both the Go projector and TypeScript decoder.

## Verification

```sh
cd apps/lab-web
npm run data:latest
npm test -- --run
npm run build
npm run test:e2e
```

Desktop and 390px viewport tests must cover all eight mechanisms. Visual review must confirm that overlapping generation/effect bars remain separately visible and that sharing/fallback physical identities are explicit.
