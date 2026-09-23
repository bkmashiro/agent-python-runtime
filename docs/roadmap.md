# Pysolate task-level validation

**Complete.** Both arms scored 8/8 correct, but the code arm used 34.1% more reported tokens overall in this small frozen pilot. Raw answers, traces and independent regrading are retained in `docs/agent-evaluation.md`. No prompts were retuned after seeing results and no runtime changes were made.

## Question and arms

Does code-mediated use of the same approved tools improve task correctness, model round trips or model context use on a few controlled tasks? Compare direct model tool calls against Pysolate-executed Python using the same DeepSeek endpoint/model and domain capabilities. Do not attribute a general Code Mode benefit uniquely to this runtime.

## Contract before live runs

- Four synthetic, fully specified tasks: single lookup (negative control), paginated integer-cent aggregation, row/catalog join, and one explicitly retryable read failure.
- Same task data, answer schema, domain tool definitions/permissions, completion-token request and episode budgets in both arms. Direct calls may be batched within a model turn.
- The code arm can compute in Python but cannot access the model key, Host filesystem, network or extra domain tools. No real external side effects.
- Only schema-valid `submit_answer` ends a task; oracle grading occurs afterward and is never fed to the model. Shape/protocol failures and numerically wrong answers remain distinguishable.
- Independent oracle checks and fake-provider protocol tests precede live use. First run a separate lookup smoke in both arms, then freeze cases/prompts/budgets for scoring.
- Scored pilot: 4 tasks × 2 arms × 2 repeats = 16 planned episodes. Alternate arm order between repeats. Retain every failure and explicitly classify unrun rows if a shared budget stops the campaign.
- At most 12 model turns and 32 permitted domain calls per episode; at most 96 model requests for the scored campaign. No automatic transport retries. A failed smoke is diagnosed before another attempt; it is not quietly treated as scored success.
- Record actual nullable provider token usage, requested/returned model names, model requests, model-facing tool calls, underlying domain calls, total episode time, model wait, Guest execution and domain callback time. Nested durations are not additive; do not manufacture token or cost estimates.
- Use prepared execution with setup recorded separately. State host/artifact and warm lifecycle. Do not hide initialization cost or call this a cold-start comparison.
- Keep credentials and provider reasoning out of persisted/public traces. Fixtures are synthetic, but still review generated code/results before publishing evidence.

## Completion

[x] Harness/oracles verified; no new runtime authority.
[x] Live smoke exercises both actual paths.
[x] Frozen 16-episode cohort accounted for, or clearly reported budget/provider blocker.
[x] Results report correctness first, costs second, failures and sample limitations included.
[x] Relevant checks, signed commit/push and remote verification complete.

This is a pilot, not a statistical ranking or a production success-rate claim. No smolagents integration, release publication, scheduler rewrite or additional runtime optimization is authorized by this validation task.
