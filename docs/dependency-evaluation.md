# Dependency tasks with complete private recordings

## Scope

This v2 pilot tests whether code reduces model intervention when a later tool
argument depends on an earlier result. Direct tools and Python use the same
readonly tools, data, limits and DeepSeek `deepseek-flash` model. Direct calls
may still be batched; the new cursors/references simply are not known before
the preceding response. The code prompt was not changed into a mandatory
single-program/minimal-output policy, and final submission remains an outer
model tool call in both arms.

Three cases, two arms, two fresh conversation repeats: **12 scored episodes**.
All 12 answers were independently correct. No retries/replacements were made
to improve the scored results. All 12 were subsequently replayed offline.

The v1 results remain in [agent-evaluation.md](agent-evaluation.md). V2 uses
seeded recorded execution and complete capture; do not pool its timing with
v1's unrecorded runs or call this a pure before/after latency experiment.

## Results

Numbers below retain both repeats instead of hiding variation behind a mean.

| Task | Direct model turns | Python model turns | Direct tokens | Python tokens | Direct / Python domain calls |
| --- | --- | --- | --- | --- | --- |
| Opaque-cursor sum | 7, 7 | 5, 3 | 17,092; 17,712 | 10,425; 5,003 | 6,6 / 14,12 |
| Payment-due branch | 5, 5 | 6, 6 | 5,285; 5,210 | 8,016; 7,716 | 4,4 / 4,4 |
| Available-credit branch | 5, 5 | 5, 5 | 5,233; 5,205 | 6,584; 6,816 | 4,4 / 4,5 |

- **Cursor task:** the six page reads have genuine sequential dependencies.
  Code eventually uses a Python loop and reduces model round trips, although
  inspection/rechecking causes repeated reads. Fewer model interventions does
  not imply fewer underlying API calls.
- **Payment branch:** the model uses Python as a wrapper around one tool at a
  time, then makes a separate arithmetic execution. It does not realize the
  available composition benefit and needs one more model round trip.
- **Credit branch:** the model again breaks work across steps. Model rounds
  remain equal, with one extra underlying call in a repeat.

Across this specific case mix, direct used 34 model requests and 55,737 tokens;
code used 30 requests and 44,560 tokens. The reduction is driven by pagination,
not a universal benefit. Both arms scored 6/6 correct. With two repeats per
cell, these are mechanism observations rather than statistical rankings or
production success-rate estimates. Token usage is reported by the provider,
not converted into money. Synthetic callbacks do not model real API latency.

## Recording and replay proof

Before scoring, a two-episode real-provider smoke was recorded and replayed
with API credentials unset. The scored batch then captured **64 full provider
exchanges, 24 Python executions, 43 Python wire outcomes and 28 direct domain
outcomes**. Its private JSONL is 15,109,118 bytes and includes 344 snapshots and
markers. Every operation was complete and untruncated; the footer counted 12
episodes. Credential checks covered both strings and decoded byte fields.

The CLI re-executed the complete batch with no API key. All 12 episodes
matched, with no live provider or domain dispatcher available. Raw provider
requests/responses, reasoning, generated source and intermediate I/O remain
private under the local `pysolate-agent-eval-v2` artifact directory. They are
not committed to Git.

The repository contains reviewed score projections with final submission
arguments, [metadata](performance-data/dependency-evaluation-v2/metadata.json),
[summary](performance-data/dependency-evaluation-v2/summary.json) and
[recording checks](performance-data/dependency-evaluation-v2/recording-verification.json).
These projections are deliberately not substitutes for the private archive.

```sh
python3 tools/summarize-agent-eval.py \
  docs/performance-data/dependency-evaluation-v2/scores.jsonl --expected 12
```

## Reproduce a new run

With provider credentials configured, use a new private output path:

```sh
go run ./examples/agent-eval -base-url https://api.deepseek.com \
  -model deepseek-flash -api-key-env DEEPSEEK_API_KEY \
  -cases cursor_sum,dependent_due,dependent_credit -repeats 2 \
  -max-turns 12 -max-model-requests 96 \
  -guest dist/pysolate.wasm -out /private/dependency.jsonl
```

The `.private.jsonl` recording is default-on. Replay it using the command in
[development-recording.md](development-recording.md), not by querying the model
again. Existing output paths are refused. A new inference is a new experiment.

## Decision

Keep direct tools and Code Mode alongside each other. The next hypothesis is
whether an explicit compose-in-one-execution/return-summary policy makes the
model use the existing code capability consistently. It should be a separate
preregistered ablation with this baseline retained. No runtime optimization,
SDK integration or automatic routing policy is justified by these results.
