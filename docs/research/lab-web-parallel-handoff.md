# Pysolate Lab Web v3 handoff

**Status:** current, latest-only, static and read-only.

## Contract

The browser consumes only two current, build-pinned projections:

```text
pysolate.lab-latest.v2
pysolate.lab-task.v2
```

The v2 Inspector deliberately restores the historical debugger information architecture from `38d13e6`: an execution-event column on the left and a selected-operation inspector on the right. It is not a decoder for the retired `debugger.json` schema; current data comes from the strict task projection. Timeline is a separate top-level surface.

## Generation authority

`research/labview.BuildLatestSnapshot` and `research/labview/cmd/project-latest` are the mechanism projection authority. `research/labview.BuildTaskSnapshot` and `research/labview/cmd/project-task` pin the dedicated public release-readiness corpus, its real Guest report, and the same-run body capture and select `dev-release-readiness`. Before publishing, `project-task` creates and validates a private `trajectory.ProfileExperimentFull` recording in `labstore`; the checked-in task snapshot contains only explicitly public fixture bodies whose content digests match recorded agent outputs and workspace changes. They write:

```text
apps/lab-web/public/lab-data/latest.json
apps/lab-web/src/latestIdentity.ts
docs/evidence/lab-release-readiness-body-capture.json
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

The Inspector renders one real `dev-release-readiness` run using the restored execution-list/selected-operation layout. It exposes the three Agent Python sources, Host/runtime events, full verified public output bodies, workspace artifacts, event metadata and task context. Timeline is a separate surface over the same event stream. Internal oracle checks remain validated but are presented only as the useful final workflow output, never as pass-count promotion.

## Safety boundary

Lab Web may render the body-safe mechanism snapshot and the explicit public release-readiness fixture projection. The underlying `experiment_full` recording remains private and content-addressed. The browser cannot:

- execute Python or replay a Run;
- dispatch a capability or external effect;
- grant authority or publish a workspace;
- resolve arbitrary private paths, prompts, provider bodies or workspace bodies;
- treat a corpus string as observed output unless its digest matches the recorded event and workspace change;
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
