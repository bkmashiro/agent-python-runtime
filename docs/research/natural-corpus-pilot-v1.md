# Natural Agent-Program Corpus Pilot v1

**Status:** bounded internal pilot, strict corpus contract and sharing-opportunity gate complete on 2026-08-16; not a benchmark or optimization claim.

## Question

Can a tiny slice of existing public agent datasets provide enough real agent-written Python to exercise the current Host-profile-bound CPython/WASI Guest without first collecting a private corpus or invoking an LLM labeler?

## Named inputs

Raw responses remain private under `~/.hermes/evidence/pysolate/m4-pilot-v1/` (`0700`; files `0600`). The checked-in census contains no source, conversation, patch or tool-result bodies.

- `xingyaoww/code-act`, config `default`, split `codeact`, rows `0..49`
  - dataset revision observed: `afba34367a8609a1d0044eded531548ab71a58cf`
  - dataset-card license: `apache-2.0`
  - response bytes: `269195`
  - SHA-256: `sha256:8bd4d898d1dcb5596207b5d09a044dc260e75129e0204b545b4972a27bfbb188`
- `nvidia/Open-SWE-Traces`, config `openhands`, split `minimax_m25`, rows `0..9`
  - dataset revision observed: `ad4805a5aa7de70d99cab0bb8f99b15304c76de0`
  - dataset-card license: `cc-by-4.0`
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

## Versioned corpus contract

`pysolate.natural-corpus-manifest.v1` binds every retained item to its dataset, source-record digest, action/trajectory index, source digest and byte count, provenance, collection adapter, oracle/privacy/authority classes, expected backend, terminal inclusion state and probe state. The validator uses closed enums, recomputes item identities, verifies source/denominator joins, bounds source and item counts, and rejects duplicate identity, digest mismatch, unknown class, private path/body leakage and denominator dropping.

The body-safe manifest is `docs/evidence/natural-corpus-manifest-v1.json`:

- manifest identity: `sha256:8ffde0e8882097320e61a0ec8606c1e2c9ee60d71c357f143ce546b720c65dcf`;
- file SHA-256: `eb73ca55193294ce5bbacacf7d836cb99c039eb61e3e9a856dc64d45e1c0154a`;
- denominator: 147 items = 137 CodeAct actions + 10 Open-SWE trajectories;
- states: 22 included, 125 rejected, 0 unclassifiable and 0 truncated;
- probe state: 8 completed, 139 not run.

Zero counts are explicit rather than omitted. In synthetic contract tests all four terminal states are exercised.

## Sharing-opportunity gate

The deterministic opportunity census found:

- CodeAct: 137 actions, 130 unique exact sources, seven duplicate instances, zero duplicate groups across records, four records with sequential duplicates and no overlap evidence;
- Open-SWE: 400 `execute_bash` calls, 338 unique exact commands, 62 duplicate instances, 59 sequential within-trajectory duplicates, one command repeated across trajectories, zero messages with multiple bash calls and zero parallel exact duplicates;
- authority equivalence: not recorded;
- workspace-base equivalence: not recorded.

The body-safe report is `docs/evidence/natural-corpus-opportunity-v1.json`, SHA-256 `dd1621c88dc07e6721711aab9f6599e9f54cac65a52178ab1f85f4d3eea6ced4`.

The gate result is `insufficient_evidence / do_not_implement_sharing_pass`. Sequential retries in mutable workspaces are not coalescing opportunities. The one cross-trajectory repeated command lacks overlap, source, authority and workspace-base equivalence.

## Claim boundary

This pilot supports only:

> A deterministic body-safe importer found eight natural CodeAct Python actions that completed both locally and in the named Host-profile-bound real Guest.

It does **not** establish original task correctness, output equivalence, source-bound tool decisions, workload frequency, latency improvement, physical sharing opportunity or a reason to implement another pass. Single-run timings are smoke observations only. Open-SWE trajectories were counted and summarized, not replayed in their repository environments.

The 99 CodeAct environment-dependent actions show that isolated action replay is often the wrong unit: many records rely on persistent earlier state. That is a corpus/harness reconstruction barrier, not evidence for continuation, snapshotting or a worker pool. The next evidence needed for physical sharing is a deliberately bounded multi-agent cohort with actual overlap plus Host-owned source, authority, prepared-image, immutable-input and workspace-base identities. No optimizer, scheduler or logical-agent sharing mechanism follows from the present data.
