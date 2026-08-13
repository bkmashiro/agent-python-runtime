# Spark Composable Acceptance

Status: **all 54 direct Linux scenario/treatment rows are recorded and body-free.**

## Frozen decision corpus

- Model: `gpt-5.3-codex-spark`
- Corpus schema: `pysolate.spark-scenario-corpus.v1`
- Corpus SHA-256: `5f55faca56080845110a20b226832f5d5c22c7e0ab10d6c63a0163063a30e454`
- Frozen source: `2451cc35cff566ad556c18c2f57064e233994675`
- Scenarios: 3

The private corpus contains the task, two child analyses, wait boundary,
observation refresh, deterministic repeated transformation, selected child, and
expected private artifact for each scenario. It is not published by Lab.

## Direct results

Host source `0dcbb33e131c68d8d2d747f6fcf80c5d5b85c6d8` ran all three frozen scenarios
against the fixed-memory Linux Guest artifact
`sha256:591978964aae541d0758404f325c482898aa2ba5386a721dd2a5dcf049dbe9fb`.
Exactly 54 direct rows were recorded:

| Treatment | Direct rows | Expected status |
|---|---:|---|
| fresh | 3 | passed |
| streaming | 3 | passed |
| fanout | 3 | passed |
| cache off | 3 | passed |
| cache on | 3 | passed |
| single-flight off | 3 | passed |
| single-flight on | 3 | passed |
| reevaluation off | 3 | rejected |
| reevaluation on | 3 | passed |
| prepared | 3 | passed |
| COW | 3 | passed |
| all bounded | 3 | passed |
| invalid parent | 3 | rejected |
| invalid child | 3 | rejected |
| changed observation | 3 | passed |
| branch conflict | 3 | rejected |
| cache corruption recovery | 3 | passed |
| cancellation recovery | 3 | passed |

The canonical body-free report is
[`evidence/spark-composable-direct-report.json`](evidence/spark-composable-direct-report.json).
No private expected artifact body appears in that file. The exact Linux binary,
artifact, corpus, report, exit status, and cleanup checksums are bound in
[`evidence/spark-composable-linux-run.json`](evidence/spark-composable-linux-run.json).

## Shared conformance is separate

The same Linux job also ran the generic north-star, feature matrix,
invalid-parent cleanup, Agent Function, workflow, streaming, subagent, and
workspace suites. Their checksum-bound statuses are stored in
[`evidence/spark-composable-shared-conformance.json`](evidence/spark-composable-shared-conformance.json).
They are **not** scenario results and are never used to synthesize missing
scenario/treatment rows.

## Lab projection contract

`cmd/composable-acceptance-report` validates the corpus/report identity and
projects only recorded rows into generic `research/labview` contracts. The
canonical report remains body-free. The Web dataset additionally publishes the
reviewed, credential-free frozen scenario fixture—task, files, child analyses,
wait/observation boundary, and expected artifact—so the Debugger can inspect the
experiment rather than showing result digests alone. Workspace tree bodies that
were not captured remain identity-only. The view also displays recorded status,
oracle status, metrics, terminal disposition, model/source/corpus/report
identities, and typed refs.

A compatible corpus/report produced by another model uses the same UI. The
projection remains fail-closed: any future unrecorded treatment stays absent
until Pysolate executes that frozen scenario and produces a direct row. Shared
conformance and front-end prose cannot create rows.
