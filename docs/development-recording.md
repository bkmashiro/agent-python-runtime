# Full private development recording

Pysolate model experiments record fully by default, as required by `AGENTS.md`.
Metrics and public reports are separate projections, not replacements for the
private development record.

## Entry point and contents

`examples/agent-eval` always creates a private JSONL file alongside `-out`, or
at `-private-record`. Files use mode 0600 and reject existing paths, including
unfinished captures. Keep the parent directory private. The old `agent-loop
-trace` walkthrough is a narrower diagnostic, not this recording entry point.

Recorded contents include:

- fixture, model, credential-free endpoint, runtime, limits and artifact identity;
- full provider request/response bodies, prompts, returned reasoning and usage;
- HTTP status, transport/read errors and explicit truncation markers;
- model tool calls, generated and executed Python source, seed and inputs;
- raw execution value, stdout, stderr, transformed source and errors;
- each direct tool argument/result and each Python tool's exact wire outcome;
- final score, completion state and a checked episode-count footer.

Bodies and Guest I/O use byte fields, base64-encoded in JSON, rather than
re-encoded JSON fragments. Request intents are synced before sending. Provider
results, execution starts, tool starts/outcomes and execution results are
append-synced at their boundaries. A crash prefix is retained for inspection;
without a valid completion footer it cannot pass as a complete replay capture.

Authorization/cookie headers are not stored. Known configured keys are filtered
from strings and byte fields. This is not general anonymization: task data and
model output can still be sensitive. Full records, including reasoning, stay
local. Public evidence must be reviewed separately. Capture limits remain in
force; reaching them aborts recording instead of silently losing data.

## Offline replay

Build once, then replay without provider credentials:

```sh
go build -o /tmp/pysolate-agent-eval ./examples/agent-eval
/tmp/pysolate-agent-eval -replay /private/campaign.private.jsonl \
  -guest dist/pysolate.wasm -out /private/campaign.replayed.jsonl
```

The player uses saved provider responses and checks raw bodies against parsed
continuation data. It actually re-executes Python in fresh recorded Guests,
using saved seeds and strict recorded tool outcomes. Live callbacks are replaced
with rejecting stubs; empty journals cannot fall back to dispatch. Execution
I/O and final rows must match. Output rows have `replayed: true` and must not be
counted as additional live observations.

This does not call DeepSeek or real tools again, nor reproduce wall time. A new
model inference is a new experiment.

Wrong artifacts, changed bindings/source/arguments/results, unknown tasks,
unsupported limits, incomplete captures, truncated responses and unused calls
fail closed. Wall-clock timeout episodes are retained for diagnosis but not
currently accepted for deterministic replay, since their outcome depends on
host timing. Ordinary Python exceptions and recorded tool errors are supported.
Records are not cryptographically signed authenticity proofs.

This evaluator grants no workspace. It does not snapshot Python heaps or
arbitrary filesystems. Legacy v1 score logs lack full provider bodies and remain
partial historical evidence; omitted data is never reconstructed as observed.

## Verification gate

Before each paid scored cohort, capture a small real-provider smoke and replay
it with credentials unset. Verify completeness, matching I/O and absence of
credentials. Tests additionally close fake providers, reject live dispatch,
preserve non-UTF-8 output and inspect interrupted prefixes.

Boundary recording contributes to episode timing; final episode/footer writes
are outside that timer. Do not pool these times with old unrecorded experiments.
Correctness and model round trips are the dependency study's primary metrics.
