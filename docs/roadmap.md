# Pysolate development recording and dependency validation

## Current pointer

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
