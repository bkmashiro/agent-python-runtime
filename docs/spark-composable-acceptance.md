# Public Multi-Agent Composable Acceptance

Status: **3 checked-in public development scenarios × the `all` treatment passed on Linux/amd64 with real parent/child Guest spans, source ranges, and workspace path diffs.**

## Public development corpus

- Corpus: [`../research/composableacceptance/testdata/public-development-corpus.json`](../research/composableacceptance/testdata/public-development-corpus.json)
- Corpus schema: `pysolate.spark-scenario-corpus.v3`
- Corpus SHA-256: `sha256:f88e94b462dd39d094512f71f9b8a397e0627b745c217442ccee98dbaed4904a`
- Scenarios: `dev-ranking-report`, `dev-workspace-summary`, `dev-wait-resume-report`
- Treatment: `all`

Each scenario contains a public parent Guest program and two public child programs (`researcher` and `reviewer`). The children execute in fresh Wazero Guests over sibling-private workspace branches and write different public fixture paths. The Host selects one branch and discards the other. No private corpus is required.

Set `PYSOLATE_ACCEPTANCE_MATRIX=conformance` only for the separate 18-treatment mechanism regression matrix.

## Linux direct recording

Source commit `62538a0a61056056ef0e0aaaa1276f3b2a776b1c` ran all three scenarios in Slurm job `273895` on Linux/amd64 against Guest artifact `sha256:591978964aae541d0758404f325c482898aa2ba5386a721dd2a5dcf049dbe9fb`.

- 3/3 runs passed with treatment `all`;
- each run recorded 37 sequential raw events from `run.start` to `run.terminal`;
- total recorded events: 111;
- each run records four lanes: Host runtime, parent Guest, researcher Guest, and reviewer Guest;
- each run records two child `agent.execute` spans with `parent_agent_id=orchestrator` and `parent_span_id=orchestrator-python`;
- the two child spans overlap in recorded monotonic time;
- parent and child execution spans carry their actual public source IDs, files, and line ranges;
- each child span carries an actual workspace snapshot diff with its added output path, size, and content digest;
- test elapsed time: 114.41 seconds;
- process exit: 0;
- the remote staging directory was removed after artifact retrieval.

The canonical report is [`evidence/spark-composable-direct-report.json`](evidence/spark-composable-direct-report.json). Binary, Guest artifact, corpus, report, debugger dataset, log, exit, and identity checksums are bound in [`evidence/spark-composable-linux-run.json`](evidence/spark-composable-linux-run.json).

## Current Lab boundary

The former `pysolate.lab-web-debugger.v4` and `pysolate.agent-trajectory.v0` projections are
retired. Current Lab ingestion accepts only portable `pysolate.causal-evidence.v1` views: a
body-safe experiment projection and a strict production rollback/reconciliation subset from
one named real Guest trace. The historical acceptance report above remains valid for its
original bounded run but is not converted into current causal evidence.

## Evidence boundary

Source ranges identify the recorded Guest program span. They do **not** claim per-opcode or interpreter program-counter tracing. Workspace entries are actual snapshot differences from the public child branches; file contents are not published or reconstructed from digests. Credentials, Host absolute paths, private source, prompts, and model output remain excluded. Missing source/span metadata, duplicate or dangling spans, trace gaps, invalid workspace changes, duplicate run IDs, schema mismatch, or terminal mismatch fail closed.
