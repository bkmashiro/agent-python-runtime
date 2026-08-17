# Pysolate Lab Web v3 handoff

**Status:** current, latest-only, static and read-only.

## Contract

The browser consumes only two current, build-pinned projections:

```text
pysolate.lab-latest.v2
pysolate.lab-task.v1
```

There is no compatibility adapter for the former trajectory, campaign, debugger, or Lab v1 Web shapes. The task inspector is a new projection of one accepted composable run, not a decoder for the removed historical debugger schema.

## Generation authority

`research/labview.BuildLatestSnapshot` and `research/labview/cmd/project-latest` are the mechanism projection authority. `research/labview.BuildTaskSnapshot` and `research/labview/cmd/project-task` pin the public development corpus plus the accepted composable report and select the real `dev-workspace-summary` run. They write:

```text
apps/lab-web/public/lab-data/latest.json
apps/lab-web/src/latestIdentity.ts
apps/lab-web/public/lab-data/task.json
apps/lab-web/src/taskIdentity.ts
```

Regenerate through the repository script:

```sh
cd apps/lab-web
npm run data:latest
npm run data:task
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

Each mechanism view keeps only a semantic workspace-report program, two or three useful measurements, and an explicit `timeline` or `state_flow`. Correctness bookkeeping and evidence hashes remain in the validated projection but are not rendered. Low-noise `MEASURED`, `EXPERIMENTAL`, and `CONTROL` labels preserve evidence tier without a claim-boundary panel. The natural 36-event source-prefix census is no longer a Web input or visible card.

The separate Task Inspector renders one accepted `dev-workspace-summary` run: orchestrator, researcher and reviewer Python; Host/runtime events; a clickable timeline or trace tree; and Python, task, I/O and workspace panels. Oracle events remain validated but are not presented as UI accomplishments.

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
npm run data:task
npm test -- --run
npm run build
npm run test:e2e
```

Desktop and 390px viewport tests must cover all eight mechanisms and the Task Inspector. Visual review must confirm that short terminal events remain readable without leaving the shared time axis, state-flow arrows stay centered, and timeline/trace/inspector interactions remain usable.
