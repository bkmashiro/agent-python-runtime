# Public Development Composable Acceptance

Status: **3 checked-in public development scenarios × the `all` treatment passed on Linux/amd64 with 120 recorded events.**

## Public development corpus

- Corpus: [`../research/composableacceptance/testdata/public-development-corpus.json`](../research/composableacceptance/testdata/public-development-corpus.json)
- Corpus schema: `pysolate.spark-scenario-corpus.v2`
- Corpus SHA-256: `sha256:685bc6ca31de7f0218acc682c24d472a0671ec522b53c821fb3151570103dd9c`
- Scenarios: `dev-ranking-report`, `dev-workspace-summary`, `dev-wait-resume-report`
- Treatment: `all`

The corpus is normal checked-in development data. Its task descriptions, filenames,
child-analysis labels, observations, expected artifacts, and complete Guest Python
sources are public. No private frozen corpus is required by the default benchmark.

Set `PYSOLATE_ACCEPTANCE_MATRIX=conformance` only when running the separate
18-treatment mechanism regression matrix.

## Linux direct recording

Host source `71066f975deb044b484df7f84357f161fcea2238` ran all three public scenarios
in Linux job `273851` against Guest artifact
`sha256:591978964aae541d0758404f325c482898aa2ba5386a721dd2a5dcf049dbe9fb`.

- 3/3 runs passed;
- every run used treatment `all`;
- every run recorded 40 sequential events from `run.start` to `run.terminal`;
- total recorded events: 120;
- the Python source published by Lab is byte-for-byte the scenario `guest_source`
  used by the streaming Guest execution;
- test elapsed time: 110.26 seconds;
- process exit: 0;
- remote stage was removed after artifact retrieval.

The canonical report is
[`evidence/spark-composable-direct-report.json`](evidence/spark-composable-direct-report.json).
The binary, Guest artifact, corpus, report, debugger dataset, log, exit, and identity
checksums are bound in
[`evidence/spark-composable-linux-run.json`](evidence/spark-composable-linux-run.json).

## Lab projection contract

`cmd/composable-acceptance-report` validates corpus/report identity and projects each
row as one `pysolate.lab-web-debugger.v3` run keyed by `run_id`.

Each run contains:

- complete public `guest_source` executed by that Guest run;
- a real sequential trace and terminal disposition;
- scenario shape metadata;
- runtime metrics, captured digests, checkpoint identities, and typed refs.

The UI presents `Guest Python` as the default code view. The public Go
`runScenarioAllExecution` harness is available separately as `Host recorder`; it is
never labelled as Guest Python. Missing source, trace gaps, dangling parents,
duplicate run IDs, schema mismatch, or terminal mismatch fail closed.

Workspace/checkpoint bodies are not reconstructed. Credentials and Host absolute
paths remain forbidden from the static dataset.
