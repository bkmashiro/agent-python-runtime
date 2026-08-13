# Pysolate Lab Web

A static debugger for recorded Pysolate runs.

```text
recorded run selector → causal trace → operation input/output/details → captured identities
```

## Recorded experiments

The checked-in `public/lab-data/debugger.json` uses `pysolate.lab-web-debugger.v3`. Every run contains, under one `run_id`:

- the complete public Python source executed by the selected Guest run;
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

The selector contains three checked-in public development scenarios, each recorded
with the `all` treatment. Trace events are grouped deterministically by mechanism;
each group expands to the original sequence, outcomes, metadata, and digests.

`Guest Python` is the complete source actually sent through the streaming Guest
execution. `Host recorder` separately shows the complete build-bound public
`runScenarioAllExecution` function, clearly labelled as Host Go rather than Guest
Python.

## Evidence boundary

The development corpus, Guest Python, task descriptions, filenames, child-analysis
labels, observations, and expected artifacts are all checked-in public fixtures.
The static Web projection intentionally includes `guest_source`. It still contains
no credential or Host absolute path, and workspace/checkpoint bodies are not
reconstructed: the debugger shows only identities actually captured.

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
