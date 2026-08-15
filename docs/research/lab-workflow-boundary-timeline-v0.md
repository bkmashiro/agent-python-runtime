# Lab workflow-boundary timeline v0

Status: **Sealed read-only workflow experiment surface**
Date: 2026-08-15

The existing `apps/lab-web` now opens on a paired-experiment surface backed by
`pysolate.workflow-benchmark-evidence.v0`; the prior development debugger remains available
as a separate read-only tab.

The page validates the closed JSON shape, manifest seal, every nested observation seal,
the outer evidence seal, exact task/report correspondence, reverse logical/physical
indexes, producer/consumer and sharing-decision coverage, same-workflow parallel scope,
aggregate physical-call counts, zero observable divergence, body-free keys and
`consumer_admitted=false` before rendering. A failure leaves an explicit
rejected/unavailable state and never falls back to inferred data.

The surface shows:

- fixed-seed shuffled arrivals and prepared task class;
- baseline/all-off and optimized measured timelines side by side;
- separate model fixture, logical request, Guest WASM and typed Host tool lanes;
- replayed labels on deterministic model invocation/output intervals;
- admitted and rejected preissue/parallel/coalescing/reuse decisions;
- logical claimant → physical producer → all consumers;
- measured physical counts and critical paths from the paired run;
- explicit body omission and lack of Lab execution authority.

The Lab does not execute, schedule, retry, preissue or claim capabilities. It contains no
prompt, model-output body, Python source, tool result body, workspace path, credential or
private chain-of-thought. The benchmark uses “model invocation/output fixture,” not “model
thought.”

## Verification

`workflowData.test.ts` checks the canonical public fixture plus mutation, body-leak,
orphan-consumer and authority rejection. Playwright verifies desktop and 390 px layouts,
near-match rejection, coalesced/reused provenance, console cleanliness and no page-level
horizontal overflow. Production TypeScript/Vite build and visual inspection cover the
overview, lower timeline/provenance and narrow layout.
