# Natural Agent-Program Corpus Pilot v1

**Status:** bounded internal pilot complete on 2026-08-16; not a benchmark or optimization claim.

## Question

Can a tiny slice of existing public agent datasets provide enough real agent-written Python to exercise the current Host-profile-bound CPython/WASI Guest without first collecting a private corpus or invoking an LLM labeler?

## Named inputs

Raw responses remain private under `~/.hermes/evidence/pysolate/m4-pilot-v1/` (`0700`; files `0600`). The checked-in census contains no source, conversation, patch or tool-result bodies.

- `xingyaoww/code-act`, config `default`, split `codeact`, rows `0..49`
  - response bytes: `269195`
  - SHA-256: `sha256:8bd4d898d1dcb5596207b5d09a044dc260e75129e0204b545b4972a27bfbb188`
- `nvidia/Open-SWE-Traces`, config `openhands`, split `minimax_m25`, rows `0..9`
  - response bytes: `2542745`
  - SHA-256: `sha256:4b91b39f54849bac8323a468b67fbed6065535dba004731c99ea21cb6345de1e`

The Open-SWE slice is intentionally the first ten rows, not a post-hoc Python-only selection: 2 Python, 5 Go, 1 Java, 1 TypeScript and 1 PHP. Outcomes are one resolved, six unresolved and three `-1`; 675 structured tool calls are present. This mixed denominator corrects the initial assumption that the first ten records were all Python.

## Deterministic census

`scripts/natural-corpus.py` extracts only complete `<execute>...</execute>` actions and applies bounded AST screening without model judgement.

From 50 CodeAct records:

- 137 executable-code actions were retained in the denominator;
- 20 passed the static compatibility screen after recognizing named standard-library imports;
- 99 required prior interactive environment state;
- 12 required third-party packages;
- 6 crossed the pilot file/dynamic/network boundary;
- eight actions with real top-level computation and no imports were selected deterministically for the smoke probe.

The checked-in body-safe census is `docs/evidence/natural-corpus-pilot-v1.json`, SHA-256 `8c68852e1a57d3bffccdbd1a43d4d21fec82eb85d850f9f4c9bd170bc376e048`.

## Bounded execution probe

The runner was built from source commit `31b58661e26b8a1f826af5f2bc99cf7918ac32b4`. Each selected action ran once under:

1. local isolated CPython completion baseline; and
2. the exact CPython/WASI artifact `sha256:664077c1d63445ec267b1b30e30ce31c72e7038d62a08fe1682c675a64cff257`, with a Host-bound `base` execution profile and static allowed-import set.

Observed completion:

- baseline: 8/8 `ok`;
- real Guest: 8/8 `ok`;
- matched completion: 8/8.

The body-safe probe is `docs/evidence/natural-corpus-probe-v1.json`, SHA-256 `33cf080ffd1d9a5a297453c27fcfaa6e5d4fc73a615bce782e5adffc1fc6c141`. Full code/stdout/stderr/responses remain private in `private-probe-v1.json`.

## Claim boundary

This pilot supports only:

> A deterministic body-safe importer found eight natural CodeAct Python actions that completed both locally and in the named Host-profile-bound real Guest.

It does **not** establish original task correctness, output equivalence, source-bound tool decisions, workload frequency, latency improvement, physical sharing opportunity or a reason to implement another pass. Single-run timings are smoke observations only. Open-SWE trajectories were counted and summarized, not replayed in their repository environments.

The next decision should be whether to add a small oracle-bearing cohort or stop; no optimizer, scheduler or logical-agent sharing mechanism follows from this pilot.
