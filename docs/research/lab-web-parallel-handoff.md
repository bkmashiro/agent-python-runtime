# Pysolate Lab Web v3 handoff

**Status:** current, latest-only, static and read-only.

## Contract

The browser consumes only:

```text
pysolate.lab-latest.v1
```

There is no compatibility adapter for the former trajectory, campaign, or Lab v1 Web shapes. Historical Go research contracts remain in the repository but are not browser inputs.

## Generation authority

`research/labview.BuildLatestSnapshot` and `research/labview/cmd/project-latest` are the projection authority. They pin the accepted evidence/manifest/projection SHA-256 values and validate canonical authored Python, paired runs, logical/physical owner joins, terminal uniqueness, source mismatch, natural-cohort boundaries and claim limits before writing:

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

The UI intentionally contains three obvious mechanism examples:

1. reach-gated source-prefix overlap, with generate-first and streaming timelines;
2. exact request sharing, with two logical agents bound to one physical Guest;
3. a source-mismatch control, with independent physical executions and zero unsafe reuse.

Each view shows authored source, recorded metrics/timelines, authoritative facts, a claim boundary and evidence identities. The natural 36-event cohort is displayed only as an ineligible boundary, not performance evidence.

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

Desktop and 390px viewport tests must cover all three demos. Visual review must confirm that overlapping generation/effect bars remain separately visible and that sharing/fallback physical identities are explicit.
