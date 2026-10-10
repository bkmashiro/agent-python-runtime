# Partial-effect information pilot

## Result: the simple baseline already succeeds

DeepSeek `deepseek-flash` completed **12/12** scored episodes correctly, with
zero over-credited accounts. Every episode applied A/B/C once, received B's
`not_applied`, then applied only B. All final balances were 100/100/100.
The two smoke episodes also succeeded, but are excluded from the cohort.

- Ordinary documentation: 4/4 correct, 20 model requests, 26,829 tokens.
- Structured upfront contract: 4/4 correct, 17 requests, 27,137 tokens.
- On-demand contract: 4/4 correct, 19 requests, 25,155 tokens.

Totals: 56 scored requests and 79,121 provider-reported tokens. Including smoke:
67 requests and 93,912 tokens. Each group includes two direct and two code
episodes. These tiny counts do not establish a format or cost ranking.

In 10/12 scored episodes the model queried the contract, including all four
ordinary-documentation episodes where it was already supplied. Five of six
code episodes returned to the model between the partial response and retry;
one generated a Python retry loop. This is a strategy observation, not evidence
of a semantic failure or justification for a new runtime policy.

**Decision:** stop expanding this fixture. Clear docs and explicit per-item
receipts suffice for this model on this task. There is no demonstrated
correctness benefit from the structured contract. The broader information
interface question remains open, but this experiment supplies no reason to
build a contract framework or manufacture harder failures.

All 14 episodes were replayed offline with API keys unset. The first smoke
replay exposed an existing direct-tool replay bug: decoding a recorded JSON
object into a map changed member order in the next model message. A regression
test reproduced it; retaining the recorded JSON representation fixed replay.
The same smoke was replayed, not rerun against the model. Failed replay outputs
remain private, and the paid cohort only began after the replay gate passed.

Local verification: `go test ./examples/agent-eval -count=1`,
`go vet ./examples/agent-eval`, and `git diff --check` passed.
Reviewed [score projections](performance-data/partial-effects-v1/scores.jsonl)
and [summary](performance-data/partial-effects-v1/summary.json) are retained.
Raw prompts, reasoning and tool/code recordings remain private. Timings are
diagnostic only. No monetary-cost estimate or new performance claim is made.

## Question and pre-run decision

With identical capabilities and service behavior, does contract presentation
change an agent's ability to complete a partially successful operation without
duplicating effects? This is a small synthetic probe, not a benchmark release or
a claim of novelty. Do not expand it if ordinary clear documentation suffices.

Three task IDs in `examples/agent-eval`:

- `partial_docs`: full ordinary-language effect/response/retry documentation.
- `partial_structured`: equivalent upfront contract as JSON in the description.
- `partial_query`: contract available by an explicitly advertised query.

All groups have the same apply, balance-query and contract-query tools. The
contract-query response and actual operation responses are identical across
groups. The query group tests disclosure timing/cost, not an equal-token format
comparison. Natural language and JSON encode the same intended facts, but do
not have equal token counts. No business-specific runtime summarizer is added.

## Workload and oracle

Credit A, B and C by exactly 100 cents each, starting from zero. The service
rejects B's first valid credit attempt with a truthful `not_applied` item; other
items commit independently. Subsequent valid attempts succeed. The agent does
not see the fault schedule. Retrying committed items increments again. No undo
or idempotency key is available. Invalid arguments are rejected before effects.
Single-item requests are permitted; batching is not forced.

A fresh service state and conversation are created per episode. Service state
persists across Python executions, while each Guest is fresh. Success requires
`done:true` AND authoritative balances exactly 100/100/100. Since increments
are positive and there is no undo, over-credit cannot be hidden by repair.
Skipping/refusing/submitting early is not success. This probe does not model
lost acknowledgements, unknown outcomes, concurrency or real payment APIs.

The live oracle reads private fixture state. Offline regrading separately sums
confirmed receipts; it never invokes service callbacks. This equivalence is
specific to the fixture's complete receipts, not a general remote-effect claim.

## Pre-run protocol

1. Unit tests: partial completion, selective retry, duplicate effects, invalid
   arguments, state reset. Real Guest recording/replay tests cover success and
   incorrect blanket retry with a local scripted HTTP provider, then disconnect
   that provider before offline replay.
2. DeepSeek smoke: `partial_docs`, one repeat, direct and code (2 episodes).
   Read full recordings and replay offline before the scored cohort.
3. Cohort: all three IDs, two repeats, direct and code (12 episodes). Alternate
   direct/code order across repeats as in the existing harness. Task order is
   fixed docs/structured/query; this small probe does not estimate causal effect
   sizes or eliminate temporal variation. Smoke is reported separately.
4. Per episode: 8 model turns, 32 domain calls, 2-minute deadline; each request
   retains the existing 4096-token output cap. Smoke cap 16 model requests;
   cohort cap 96. No replacement runs to improve scores.
5. Inspect final state, duplicate credits, contract queries, balance queries,
   model turns, reported tokens and generated strategy. Preserve failures.
   Report tokens rather than unverified monetary prices. Timings include synced
   private recording overhead and are not a Wasm/native performance comparison.

Use the default DeepSeek endpoint/model already configured by agent-eval.
Raw provider bodies, reasoning, source and tool outcomes remain in private
local recordings, never automatically in Git. Credentials are not recorded.

## Commands

```sh
go test ./examples/agent-eval -count=1
go run ./examples/agent-eval -cases partial_docs -repeats 1 \
  -max-turns 8 -max-model-requests 16 -out "$PRIVATE/smoke.jsonl"
go run ./examples/agent-eval -replay "$PRIVATE/smoke.private.jsonl" \
  -out "$PRIVATE/smoke-replay.jsonl"
go run ./examples/agent-eval \
  -cases partial_docs,partial_structured,partial_query -repeats 2 \
  -max-turns 8 -max-model-requests 96 -out "$PRIVATE/cohort.jsonl"
go run ./examples/agent-eval -replay "$PRIVATE/cohort.private.jsonl" \
  -out "$PRIVATE/cohort-replay.jsonl"
```

`PRIVATE` must be an existing private directory. Live runs require
`DEEPSEEK_API_KEY`; replay does not. Outputs refuse overwrites. CLI defaults
still select only the original readonly tasks. No Spine or runtime-core changes.
