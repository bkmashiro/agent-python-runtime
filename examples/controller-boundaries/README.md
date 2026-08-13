# Controller-boundary examples

The first three small programs are teaching artifacts for the evaluation-v2 paper
story. They show where Pysolate changes orchestration placement without turning
the examples into another benchmark suite.

## Mechanism cases

| Example | Guest capability calls | Direct controller boundaries | Guest controller boundaries | Point |
|---|---:|---:|---:|---|
| `01-local-transform.py` | 0 | 0 | 1 | Pure computation does not need Host authority. Pysolate supplies an execution/evidence boundary, not fewer controller calls. |
| `02-one-source.py` | 1 | 1 | 1 | One Host read followed by filtering and sorting is a tie. More local computation does not itself reduce controller boundaries. |
| `03-two-sources.py` | 2 | 2 | 1 | One Guest Run obtains two separately authorised sources and joins them internally. The outer controller submits one Run while both typed calls remain receipted. |

Two additional runnable product specimens exercise the same Runtime without
extending the evaluation corpus:

- `04-workflow-with-workspace.py` performs two typed source calls and writes a
  structured report through the rooted Guest filesystem;
- `05-developer-workflow.py` uses bounded read-only `git.status/log/show`, runs
  a grep-like search with ordinary `pathlib` against `/workspace`, stages an
  inspection report under per-Run `/tmp`, then copies it into
  `/workspace/reports`.

The Direct counts are the frozen evaluation-v2 comparison rule: one controller
boundary per `Broker.Call`. The Guest count is one Runtime submission per
program. Capability calls are reported separately and are not removed by the
Guest.

These are mechanism illustrations, not latency, token, cost, model-quality,
security, isolation or production-readiness measurements.

## Run locally

A verified Guest artifact is required. The runner starts a loopback fixture
server on an ephemeral port, creates Host-owned source and Git configuration,
runs all programs through the real `apyrun` CLI, and checks exact results,
capability-call counts, receipts, and workspace outcomes.

```bash
python3 examples/controller-boundaries/run.py \
  --artifact /path/to/agent-python-runtime.wasm
```

Expected compact output:

```json
{"capability_calls": 0, "example": "01-local-transform", "receipts": 0, "result": {"count": 2, "descending": [10, 7], "total": 17}}
{"capability_calls": 1, "example": "02-one-source", "receipts": 1, "result": {"count": 2, "ids": ["gamma", "alpha"], "top": "gamma"}}
{"capability_calls": 2, "example": "03-two-sources", "receipts": 2, "result": {"case_id": "workspace-summary", "ranked": [{"id": "gamma", "normalized_score": 0.1}, {"id": "alpha", "normalized_score": 0.07}, {"id": "beta", "normalized_score": 0.04}], "suite_id": "pysolate-core"}}
```

The loopback HTTP access log is written to stderr. Endpoint selection, methods,
redirect policy, budgets and transport remain Host-owned; the Python programs
can only call the two sealed `sources` functions.

## Paper use

A concise paper walkthrough can show the programs in order:

1. establish that Pysolate is not a claim about making ordinary loops shorter;
2. use the one-source tie as a negative control;
3. use the two-source join to show the actual mechanism: outer orchestration is
   consolidated while typed Host calls, authority and receipts remain visible.

For measured evidence rather than teaching code, cite
[`docs/research/workload-evaluation-v2-expanded.md`](../../docs/research/workload-evaluation-v2-expanded.md).
