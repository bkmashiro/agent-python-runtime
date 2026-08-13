# Spark Composable Acceptance

Status: **all 54 direct Linux scenario/treatment rows are recorded with 603 body-free trace events.**

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

Host source `1cd96dccf38416dbec5a3593ad1b0d97d044a214` ran all three frozen scenarios
in Linux job `273812` against the fixed-memory Guest artifact
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

An earlier generic conformance artifact remains documented separately in
[`evidence/spark-composable-shared-conformance.json`](evidence/spark-composable-shared-conformance.json).
It is **not** part of the current per-run trace report and is never used to
synthesize missing scenario/treatment rows.

## Lab projection contract

`cmd/composable-acceptance-report` validates the corpus/report identity and
projects each recorded row as one `pysolate.lab-web-debugger.v2` run keyed by
`run_id`. The canonical report remains body-free. Every non-skipped run must carry
a real, sequential trace from `run.start` through the treatment operations to
`run.terminal`; missing traces, dangling parents, duplicate run IDs, or terminal
status mismatches fail closed. The Web dataset does not retain the old parallel
summary/record arrays and the UI never derives trace nodes from aggregate metrics.

The Web dataset publishes only reviewed scenario identity and shape metadata:
scenario ID, file/child counts, selected-child index, and presence flags for
repeated transformation, wait boundary, and observation. It never publishes
task, file, child-analysis, expected-artifact, or prohibited-output bodies.
Workspace and checkpoint bodies are not reconstructed; only identities captured
at actual operation sites are shown. The view also exposes runtime metrics,
terminal disposition, model/source/corpus/report identities, and typed refs.

A compatible corpus/report produced by another model uses the same UI. Any future
unrecorded treatment stays absent until Pysolate executes that frozen scenario
and records its trace. Shared conformance and front-end prose cannot create rows
or trace events.
