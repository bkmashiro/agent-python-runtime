# Pysolate Lab Web

A static debugger for recorded Pysolate runs.

```text
recorded run selector → causal trace → operation input/output/details → captured identities
```

## Recorded experiments

The checked-in `public/lab-data/debugger.json` uses `pysolate.lab-web-debugger.v2`. Every run contains, under one `run_id`:

- scenario identity and shape metadata;
- passed/rejected/skipped status and terminal disposition;
- lifecycle/cache/single-flight/workspace metrics;
- a sequential, causal trace from `run.start` to `run.terminal`;
- input/output and checkpoint digests captured at actual operation sites;
- artifact, invocation, execution, result, and workspace identities.

The app rejects the whole dataset if a run is missing a trace, has a sequence gap,
uses a dangling causal parent, disagrees with its terminal status, duplicates a
`run_id`, or contains an unsupported schema. It does not synthesize trace events
from summary metrics and does not fall back from an invalid recorded dataset to
fabricated experiment rows.

The historical 54-row conformance artifact remains intact, while the primary
selector deliberately shows only three `all`-treatment benchmark runs—one per
scenario. Trace events are grouped deterministically by mechanism; each group
expands to the original sequence, outcomes, metadata, and digests.

The Code tab shows the complete build-bound public `runScenarioAllExecution`
function. It is explicitly labelled as source-bound, not runtime-captured Guest
source.

## Evidence boundary

The canonical acceptance report and static Web dataset are body-free. The Web
projection contains scenario identity, counts, selected-child index, and mechanism
presence flags, but no task, file, child-analysis, expected-artifact, or prohibited-
output body. It contains no model prompt, raw model response, credential, or Host
absolute path. Workspace and checkpoint bodies are not reconstructed: the debugger
shows only identities that were actually captured.

## Technology

- React + TypeScript + Vite;
- Mantine;
- CodeMirror 6;
- TanStack Virtual;
- Vitest and Playwright.

## Development

```sh
npm install
npm test -- --run
npm run build
npm run test:e2e
npm run dev
```

## Product boundary

This deployment is static and read-only. It has no Runtime execution control,
private object-store access, ingestion API, or mutation surface.
