# Pysolate Lab Web

A debugger-shaped product prototype for inspecting an Agent-generated Pysolate
workflow. The UI is deliberately organized like a dense developer tool rather
than a dashboard:

```text
Agent workflow trace | selected source / I/O / atomic details | filesystem checkpoint
```

## Current specimen

The checked-in specimen is bound at build time to the real runnable source:

```text
examples/controller-boundaries/04-workflow-with-workspace.py
```

That example has been exercised through the verified CPython/WASI Guest. Its
observable outcome is:

- two typed capability calls;
- two successful receipts;
- the expected ranking result;
- a final workspace with `reports/ranking.json`;
- two entries and 279 bytes in the Host-authored workspace receipt.

## Evidence honesty

The UI separates four evidence classes:

- **OBSERVED** — emitted by the current Runtime observation/workspace contract;
- **VERIFIED RUN** — the runnable example passed through the real verified Guest,
  but the UI does not claim every displayed subevent was captured;
- **SOURCE BOUND** — linked to exact source ranges in the runnable Python file;
- **PREVIEW** — the target instrumentation contract, not current Runtime evidence.

A typed Python tool call such as `sources.demo_catalog()` lowers through
`agent_runtime_v1.host_call`, Pysolate's custom Guest↔Host ABI. It is not a WASI
syscall. Ordinary Python filesystem operations lower through CPython/WASI into
WASI atoms such as `path_open`, `fd_write`, and `fd_close`. The UI distinguishes
these operation classes explicitly.

Current Runtime evidence does not include all Agent turns, decoded ABI memory,
per-WASI-call events, or intermediate workspace checkpoints. Those nodes and
checkpoints are marked **PREVIEW**. Initial/final workspace identities remain
**OBSERVED**.

## Technology stack

The frontend intentionally uses maintained libraries rather than hand-rolled UI
primitives:

- React + TypeScript + Vite;
- Mantine components;
- CodeMirror 6 Python/JSON inspectors;
- `react-resizable-panels` for the debugger panes;
- TanStack Virtual for long traces;
- `react-complex-tree` for the filesystem explorer;
- Lucide icons;
- Vitest and Playwright.

## Development

```sh
npm install
npm test
npm run build
npm run test:e2e
npm run dev
```

The Vite plugin in `vite.config.ts` reads the real example and its fixtures at
build time, so the displayed Python source cannot silently drift from the
runnable repository source.

## Product boundary

This deployment remains a static, read-only target-experience prototype. It has
no ingestion API, Runtime connection, private object-store access, execution
control, or mutation surface. The next backend contract should be shaped by this
UI: Agent workflow nodes, complete typed-call arguments/results, optional ABI and
WASI subevents, checkpoint identities, and authorized file bodies.
