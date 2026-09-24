# Pysolate development roadmap

## Current delivery: front-end cost investigation

Complete. A test-only Guest probe and existing measured-window CPU profiling
locate the isolated early-read cost primarily in first-use imports and display
formatting, rather than Guest creation. No caching, prewarming or output behavior
change was implemented. See [`early-read-frontend-cost.md`](early-read-frontend-cost.md)
for measurements, profiling limits and the next optimization candidate.

## Previous delivery: contention validation (tests only)

Complete. The existing benchmark ran 54 fresh Linux processes across default COW
and COW data-image. An independent HTTP-process test covered cooperative and
non-cooperative remote work under manual cancellation and deadlines, including
race checks. Full local checks passed. The results retain the short-task latency
regression and the distinction between Guest cancellation and remote completion.
No runtime functionality, interface, default or scheduling rule changed. See
[`early-read-validation.md`](early-read-validation.md).

## Previous delivery: shared-tool contention

Complete. A small real-Guest open-loop harness compares sequential execution,
early reads and an experimental host-side spare-capacity rule. Full local traces
account for scheduled arrivals, rejection, deadlines and tool lifecycle. The
three-arm macOS/copy pilot shows low-load latency gains and busy-backend costs;
the simple rule mostly returns to sequential behavior under pressure. No runtime
default, public scheduling API or eviction mechanism was changed. See
[`early-read-contention.md`](early-read-contention.md) for results and limits.

## Previous delivery: development recording and dependency validation

Complete. Default-on full private capture passed a real-provider smoke and no-key offline replay. All 12 dependency episodes were correct and subsequently replayed with no live provider/tool dispatch. Full archives stay local; see development-recording.md and dependency-evaluation.md. Prior v1 results remain unchanged and are not full recordings.

## Standing development policy

See `AGENTS.md`: full private recording is default for model-driven experiments. Keep provider-returned fields, actual request/response bytes, generated and executed code, ordered domain and wire outcomes, seed/artifact/tool bindings, Guest output/errors, failed attempts and partial captures. Strip transport credentials and configured keys, not debugging evidence. Public summaries are separate reviewed projections.

## Scope

- [x] Default-on private recording and strict offline replay for the no-workspace `agent-eval` path. No new database, UI, provider calls during replay or live-tool fallback.
- [x] Deterministic fixtures for opaque cursor pagination and dependency/branch chains. Cursor state matches the prior 120-row ledger; both overdue and credit branches have checked answers.
- [x] Real smoke proves recording completeness, provider playback and seeded execution replay before scoring.
- [x] New independent dependency cohort: `cursor_sum`, `dependent_due`, `dependent_credit`, direct/code, two repeats each = 12 planned episodes. Same DeepSeek model, capabilities and limits; alternate arm order. Keep raw private records and all failures.
- [x] Compare correctness and model intervention counts first, then domain calls, token usage and elapsed time. Keep v1 as a separate baseline; do not rewrite its prompts or results.
- [x] Relevant checks, documentation, signed commits/push and verified clean worktree.

This remains development validation. No smolagents integration, release publication, arbitrary Host callbacks, workspace checkpointing, new runtime authority or automatic routing policy. Partial or unsupported recordings must fail visibly rather than masquerade as complete replay. Per-run limits remain enabled, with truncation/error boundaries recorded.
