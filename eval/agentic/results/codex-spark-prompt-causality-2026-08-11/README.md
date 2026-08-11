# Codex Spark prompt-causality development diagnostic

This directory records a bounded, three-arm diagnostic over `rd-004` and `rd-006`, with six paired replicates per task and arm. Every trial used the same repository commit, Guest artifact, Codex CLI model, limits, and provider observation binding.

## Intervention arms

- `control-v4`: the published compact prebound Host SDK treatment.
- `exact-plan-v5`: adds a generic instruction to form the exact required Host-call sequence, include required directory changes, extract documented response fields, and not suppress Host errors.
- `initial-cwd-v6`: additionally exposes the authoritative initial Host working directory (`/alex`).

## Observed results

| Arm | Strict / outcome pass | Rate | Provider calls | Total tokens |
|---|---:|---:|---:|---:|
| control-v4 | 7/12 | 58.3% | 13 | 212,066 |
| exact-plan-v5 | 9/12 | 75.0% | 13 | 213,570 |
| initial-cwd-v6 | 10/12 | 83.3% | 12 | 202,019 |

The direction supports an interface/prompt contribution, but the sample is deliberately small and underpowered. Exact paired McNemar p-values are 0.6875 for control→exact-plan, 1.0 for exact-plan→initial-CWD, and 0.25 for control→initial-CWD. These are development diagnostics, not a model-quality or placement decision gate.

Private raw debug classified nine of ten failures as omission of the initial directory transition; one failure was an exact output-format mismatch after a safe zero-call syntax repair. Raw prompts, model code, and Host outputs remain in mode-0600 private files and are not committed. `failure-analysis.json` publishes only sanitized classes and SHA-256 bindings to those private records.

## Oracle decision

The strict trace oracle remains unchanged. Final-state equivalence alone cannot prove effect equivalence for real tools, and the current corpus does not carry an explicit dependency/commutativity graph. Existing formal metrics continue to report strict trace, outcome success, final-state correctness, and extra calls separately rather than silently accepting reordered or redundant effects.

`hashes.json` binds every formal artifact and report file. Repository tests validate all 36 trials and recompute the aggregate counts.
