# Agent task-level evaluation

`examples/agent-eval` is a bounded validation harness for comparing two
orchestration surfaces, not a general benchmark registry:

- **direct**: the model receives the task's readonly domain tools plus
  `submit_answer`;
- **code**: the model receives `execute_python` plus `submit_answer`, and the
  same domain declarations (canonical name, keyword-only Python signature,
  schema, and return shape) in the prompt. Python runs in a fresh Pysolate
  Guest for every execution, on a warm Runner prepared once per case.

Both arms use the same synthetic read-only callbacks, integer-cent arithmetic,
per-episode Host-call limit (32, including failed calls), context/body bounds,
`max_tokens=4096`, feedback for tool errors, and task-specific structured
`submit_answer` schema. A schema-valid submission ends the episode immediately;
its exact value is graded afterward. A valid but incorrect submission is
`wrong_answer`, while malformed submissions or other protocol/shape failures
are `protocol_error` rather than a wrong computed answer.

## Pilot result: no automatic advantage from code

The frozen pilot completed all 16 planned episodes using 38 model requests.
Both arms produced **8/8 independently checked correct answers**. No episodes
were dropped, retried by the harness, or excluded from the scored cohort. The
separate two-episode smoke is retained but is not part of these totals.

The direct arm used **20,979 reported tokens and 18 model requests**; the code
arm used **28,139 tokens and 20 requests**. That is about **34.1% more tokens**
for this code-arm configuration. Both arms used the same DeepSeek endpoint,
`deepseek-flash`, capabilities, data and per-episode limits. Returned model IDs
were also `deepseek-flash`; the provider did not expose a more specific model
revision. These are API token counts, not a monetary bill or a claim about
uncached inference work.

| Task | Correct direct / code | Direct tokens, repeats 1 / 2 | Code tokens, repeats 1 / 2 | Model turns direct / code |
| --- | --- | --- | --- | --- |
| Lookup | 2/2 / 2/2 | 1,118 / 1,144 | 1,497 / 1,468 | 2,2 / 2,2 |
| Paginated sum | 2/2 / 2/2 | 4,999 / 5,348 | 8,687 / 2,174 | 2,2 / 3,2 |
| Join | 2/2 / 2/2 | 2,218 / 2,278 | 5,291 / 4,427 | 2,2 / 3,3 |
| Transient read | 2/2 / 2/2 | 1,860 / 2,014 | 2,819 / 1,776 | 3,3 / 3,2 |

### What the traces explain

- **Lookup:** Python adds orchestration/context overhead without reducing model
  turns. This negative control gives no reason to replace a simple tool call.
- **Pagination:** direct calls batched all six pages in one model turn. In code
  repeat 1, the model returned entire pages to its context, then fetched them
  again in a second execution. In repeat 2 it fetched, filtered and summed in
  one program and returned only observations/summary. The second run saved
  about 59% of tokens versus its paired direct run, but the first used more;
  reporting only the favorable run would be misleading.
- **Join:** both code runs inspected raw inputs before calculating, then fetched
  them again because Python variables do not persist across executions. Code
  made four domain calls versus two for direct. Fresh-Guest semantics are part
  of this experiment; no persistent Python heap or workspace cache was added.
- **Transient read:** one code run handled the retry inside Python and saved a
  model turn. Its wall time was nevertheless longer due to model/API wait.
  Fewer turns do not guarantee lower latency in such a small online sample.

Summed attempt wall time was 26.77 s for direct and 34.92 s for code. Of the
code total, 34.32 s was model round-trip time and 0.59 s was Pysolate execution
(including nested domain calls). Further runtime micro-optimization is not the
main opportunity indicated by this pilot. Timing is descriptive only: provider
load/cache behavior and sequential sampling were not controlled enough for a
latency ranking.

Runner preparation is excluded from these warm episode times and cost
2.91–3.00 s per case, 11.82 s total. A Runner is reused across the two code
repeats for its case; do not sum repeated `setup_ns` references as new setups.
This ran locally on macOS arm64 using prepared-copy execution, not the Linux
COW performance path. The synthetic domain callbacks have no real network
latency. Their workloads do not represent production task distributions.

### Decision and next hypothesis

Keep direct tools as the simple default for small tasks. This pilot does not
establish a task-success improvement or blanket token saving from Pysolate.
The concrete next hypothesis is a harness data-flow policy: finish retrieval
and computation within one execution, return only the requested summary, and
avoid re-reading inputs merely to inspect them. A future ablation should
predeclare that policy and retain this baseline, rather than rewrite these
results. The experiment also requires an outer `submit_answer` model call for
both arms; it does not evaluate code that can signal final completion inside
its execution. No new runtime feature or prompt retuning was applied after
seeing the scored results.

[Raw scored episodes](performance-data/agent-evaluation-v1/campaign.jsonl),
[separate smoke](performance-data/agent-evaluation-v1/smoke.jsonl),
[frozen metadata](performance-data/agent-evaluation-v1/campaign-metadata.json)
and [summary](performance-data/agent-evaluation-v1/summary.json) are retained.
The traces contain only synthetic task data, generated code and tool results;
no provider reasoning or credentials. Regrade the persisted answers with:

```sh
python3 tools/summarize-agent-eval.py docs/performance-data/agent-evaluation-v1/campaign.jsonl
```

## Frozen cases

The default campaign is 4 cases × 2 arms × 2 independent repeats = 16 scored
episode rows. Arm order alternates by repeat: direct/code for repeat 1 and
code/direct for repeat 2. The cases are:

1. `lookup`: one catalog price lookup, used as a negative-control-sized task;
2. `paginated_sum`: 120 ledger records over six pages, filtered by status and
   region, with integer-cent amounts;
3. `join`: 20 order rows joined to a four-SKU catalog and aggregated by
   category;
4. `transient`: one explicit transient error on the first readonly lookup per
   episode, with identical injection in both arms.

A separate one-case smoke can be run before the frozen campaign:

```sh
go run ./examples/agent-eval \
  -guest dist/pysolate.wasm \
  -cases lookup -repeats 1 \
  -out /tmp/agent-eval-smoke.jsonl
```

The full campaign is:

```sh
go run ./examples/agent-eval \
  -base-url https://api.deepseek.com/v1 \
  -model deepseek-flash \
  -api-key-env DEEPSEEK_API_KEY \
  -guest dist/pysolate.wasm \
  -out /tmp/agent-eval.jsonl \
  -repeats 2 \
  -max-turns 12 \
  -max-model-requests 96
```

`-api-key-env` accepts only `OPENAI_API_KEY` or `DEEPSEEK_API_KEY`; the key is
read from that environment variable and is never a CLI argument or persisted
in the output. The command performs no provider retries. Tests use a fake
provider and do not make paid or network model calls. The live command requires
an explicitly supplied key and endpoint.

`-cases` accepts a comma-separated subset of `lookup,paginated_sum,join,transient`.
`-max-model-requests` is a shared hard campaign cap and cannot exceed 96. If a
complete arm pair cannot receive at least one request per arm, both rows are
written as `skipped`; rows are never silently dropped. A request reservation
leaves one request for the paired arm when the cap is nearly exhausted.

## JSONL rows

Each episode is identified by `fixture_version`, `task`, `arm`, `repeat`,
and `sequence`. Fixtures and budgets are reproducible; live model responses
are not guaranteed to repeat. Rows include:

- `completion_status`: `completed`, `wrong_answer`, `protocol_error`,
  `provider_error`, `timeout`, `turn_limit`, `tool_limit`, `globalbudget`, or
  `skipped`;
- nullable `correctness` (`true`/`false` only after a schema-valid submission);
- model request/tool-call counts, domain-tool-call count, requested model, and
  returned provider model ids;
- nullable `provider_usage`. If any provider response lacks usage, the episode
  total is `null` and `usage_complete` is false; usage from provider errors is
  counted when the error response supplies it;
- Submitted `answer` and bounded tool traces, including generated Python and its output, for independent grading. Provider reasoning is not recorded.
- `total_ns`, `model_roundtrip_ns`, `pysolate_ns`, `domain_callback_ns`, and separate
  `setup_ns`. Setup is the per-case warm Runner preparation and is not included
  in per-execution Pysolate time. Direct rows have zero setup/Pysolate time;
- no reasoning content, authorization headers, API keys, or other credentials.

Output is created with `O_CREATE|O_EXCL` and mode `0600`; an existing
path is never overwritten. Numeric oracle checks use bounded exact rational
parsing: integral representations such as `3199.0` are accepted, fractional
values and numeric strings are not. No float rounding is used. Map key order
does not affect grading.

The rows support correctness and bounded engineering comparisons only. Small
repeats do not establish statistical significance, and the harness emits no
monetary cost estimate without provider pricing evidence.
