# Pysolate Lab Web

A static, read-only, human-first causal debugger for portable `pysolate.causal-evidence.v1` exports.

The Lab currently exposes two projections of the same named real-Guest trace:

- `experiment-full-public.json`: body-safe experiment metadata including M1 source binding and the three independent subagent context/runtime/workspace planes;
- `production-rollback.json`: the strict body-free production subset containing only execution/effect/terminal facts needed for rollback or reconciliation decisions.

Both projections share the same trace/header and retained event identities. The browser recomputes header, event and export SHA-256 identities, validates prior-only causal parents and typed source/subagent relations, and rejects unknown fields, body references, profile leakage and malformed payloads. It does not execute, retry, replay, schedule, grant capabilities or publish workspaces.

The default view groups canonical atoms into child preparation, fresh execution setup, source-bound capability, workspace terminal and run-completion tasks. Details remain reachable through truthful Overview/Input/Output/Code/Timeline/Workspace/Evidence/Raw tabs. Source and workspace panels show recorded ranges, availability, roots, counters and dispositions; when portable policy removed a body, Lab says so explicitly instead of reconstructing it.

## Data boundary

Complete experiment bodies and the append-only private JSONL log stay in the local `0700`/`0600` evidence directory and typed `research/labstore` objects. Portable Lab exports contain no body references. Body-bearing `model.body` and `runtime.observation` events are removed from the portable projection; body-free metadata remains separately identifiable so public causal links never depend on hidden nodes.

The checked-in fixtures come from the real CPython/WASI Guest gate in `integration/e2e/semantic_source_binding_test.go`, not a scripted trajectory generator. Capture identity and checksums are documented in `docs/research/dual-profile-causal-evidence-v1.md` and the Megagoal 2 plan.

## Development

```sh
npm install
npm test -- --run
npm run build
npm run test:e2e
npm run dev
```

The current deployment is static and read-only. Private Labstore body resolution, live tailing, resume, fork, replay and rollback machinery are outside this review surface.
