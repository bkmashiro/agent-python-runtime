# Pysolate Lab Web

A static multi-agent debugger for recorded Pysolate development runs.

```text
recorded run → pipeline + agent swimlanes → Python source span → workspace diff → raw evidence
```

## Recorded development runs

The checked-in `public/lab-data/debugger.json` uses `pysolate.lab-web-debugger.v4`. Every run carries one evidence-linked model under its `run_id`:

- the complete public orchestrator and child-agent Python sources actually executed;
- stable `span_id`, `parent_span_id`, `agent_id`, `parent_agent_id`, and role metadata;
- recorded start/end times for the orchestrator, two concurrent child Guests, and Host runtime operations;
- a source file and line range for every recorded Python execution span;
- per-child workspace IDs and actual added/modified/deleted path evidence with content digests;
- sequential raw event identity from `run.start` to `run.terminal`;
- input/output, checkpoint, artifact, invocation, execution, result, and workspace identities.

The UI offers two deterministic projections of those recorded fields:

1. **Timeline** shows each agent's recorded lifetime, child overlap, fan-out/join boundaries, and readable Host phase clusters;
2. **Trace tree** preserves the original sequence-first reading mode and adds causal indentation for child spans;
3. selecting an item in either view opens the matching agent source and highlights its recorded line range;
4. the filesystem panel shows the matching workspace path changes;
5. the Inspector explains the event in plain language before exposing raw action/span metadata.

The Host Go acceptance recorder is intentionally not part of the product surface.

## Validation and evidence boundary

The app rejects the whole dataset if a run has a sequence gap, duplicate or dangling span, invalid source range, invalid workspace change, inconsistent terminal status, duplicate `run_id`, or unsupported schema. It does not synthesize events from summary metrics and does not fall back to fabricated data.

The selector contains three checked-in public development scenarios, each recorded with the `all` treatment. Each scenario runs a public orchestrator Guest and two fresh child Guests over sibling-private workspace branches; one branch is selected and the other discarded.

Source locations are recorded execution spans, not a Python interpreter program-counter trace. Filesystem entries are actual recorder snapshots/diffs for public fixture paths; they are not reconstructed from digests. Credentials, Host absolute paths, private source, prompts, and model output remain excluded.

## Technology

- React + TypeScript + Vite;
- Mantine;
- CodeMirror 6;
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

This deployment is static and read-only. It has no Runtime execution control, private object-store access, ingestion API, or mutation surface.
