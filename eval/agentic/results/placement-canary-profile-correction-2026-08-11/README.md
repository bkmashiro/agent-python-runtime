# Placement canary profile correction (2026-08-11)

## Correction

The earlier stop artifact remains an accurate record of what was run at that time, but its rationale is superseded.

The first proposal's `csv` rejection was not evidence that the Guest lacked CSV support and was not a model-program failure. The pinned Guest manifest already listed `csv` as discoverable and qualified through a real roundtrip probe. The canary alone narrowed the Host profile to `json`, creating an unintuitive, undisclosed harness restriction.

The later import-free proposal did omit the requested directory change and read from the wrong workspace location. That remains a valid model-program failure. A generated-program failure is an expected benchmark outcome: it must be counted, preserved as a complete terminal trial result, and must not by itself stop a multi-task cohort.

## Implemented treatment correction

Commit `01e95272ebce599e9e2b5513c38f3fa4c6878885` introduces the identity-bound `stdlib-workspace-v1` placement policy:

- the Agent submits code only;
- the Host derives static import roots from the source preamble;
- the model no longer authors admission metadata;
- the current pinned Guest's safe, operation-qualified stdlib roots, including `csv`, are available normally;
- workspace access remains typed-Host-tool-only;
- outbound network, processes, package installation, arbitrary native loading, dynamic imports, and Host filesystem access remain unavailable;
- Python exceptions and Host-tool mistakes become scored terminal model-program failures rather than aborting artifact creation.

A real WASI Guest integration imported and used `csv`, completed the `rd-003` workspace transform, retained the Host-authored `base` compatibility declaration with `imports=["csv"]`, carried a valid RunPlan/import receipts, and passed exact trace plus final-state scoring.

## Evidence boundary

This correction changes the development treatment identity from `codex-jsonl-function-proposals-v1` to `codex-jsonl-code-proposal-v2`. The identity lock is now `placement-identity-lock/v2` with status `development_refrozen_pre_decision`. No sealed decision task was exposed and no prior trial score was reclassified as a pass.

The current artifact qualifies a curated safe subset, not every CPython standard-library module. Extending normal use to additional roots such as `typing`, `random`, or `heapq` requires adding deterministic Guest qualification probes and rebuilding/rebinding the artifact; it cannot be claimed by prompt text alone.

Historical record retained: `../placement-canary-stop-2026-08-11/`. Its no-three-arm-data boundary remains true; only its claim that one normal generated-program failure justified stopping the cohort is superseded.
