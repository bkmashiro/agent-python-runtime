# Spark Composable Acceptance

Status: **direct Linux replay recorded for fresh, prepared, and COW; remaining treatments are not yet scenario-level claims.**

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

Host source `2732db4c45def69ffdf8751b046c269f2752ffcf` ran all three frozen scenarios
against the fixed-memory Linux Guest artifact
`sha256:591978964aae541d0758404f325c482898aa2ba5386a721dd2a5dcf049dbe9fb`.
Exactly nine direct rows were recorded:

| Treatment | Direct rows | Passed |
|---|---:|---:|
| fresh | 3 | 3 |
| prepared | 3 | 3 |
| COW | 3 | 3 |

The canonical body-free report is
[`evidence/spark-composable-direct-report.json`](evidence/spark-composable-direct-report.json).
No private expected artifact body appears in that file.

## Shared conformance is separate

The same Linux job also ran the generic north-star, feature matrix,
invalid-parent cleanup, Agent Function, workflow, streaming, subagent, and
workspace suites. Their checksum-bound statuses are stored in
[`evidence/spark-composable-shared-conformance.json`](evidence/spark-composable-shared-conformance.json).
They are **not** scenario results and are never used to synthesize missing
scenario/treatment rows.

## Lab projection contract

`cmd/composable-acceptance-report` validates the corpus/report identity and
projects only recorded rows into generic `research/labview` contracts. The web
view loads `pysolate.lab-web-experiments.v1` data and displays recorded status,
oracle status, evidence class/completeness, model/source/corpus/report identities,
and typed refs. It contains no Spark-specific interpretation. A compatible
report produced by another model or corpus uses the same fields and UI.

Missing treatments remain absent until Pysolate executes the frozen scenario
through that treatment and produces a direct row. They must not be inferred from
shared conformance or added by front-end prose.
