# Agent workload pack

The workload pack freezes common Agent-generated Python and a few boundary
acceptance paths so runtime changes can be evaluated without an LLM in the timed
loop. It is deliberately small: 20 ordinary Python/Tool cases plus three
integration lanes. Cases were selected for frequency and implementation value,
not novelty.

## Run

```sh
make workloads
# or
./demos/14-agent-workloads.sh
```

`PYSOLATE_WORKLOAD_ITERATIONS=N` repeats each frozen corpus case against its
prepared Runner. The default is one iteration for a quick regression run.

The first lane emits JSONL. Each sample records the exact artifact digest,
preparation mode, execution mode, setup time, run time, output and pass/fail
result. The final three lanes are process-level Go acceptance tests and report
their elapsed test time.

## Frozen cases

`examples/corpus/agent-workloads.jsonl` contains 20 schema-v1 cases:

- structured data: JSON filtering, CSV aggregation, TOML, YAML and XML;
- text and reporting: log regexes, Markdown, HTML links and symbol indexing;
- common stdlib work: datetime buckets, hashing/Base64, URL normalization and
  path planning;
- numeric work: a small NumPy aggregation;
- Host enrichment: one read, dependent calls, independent calls, a repeated
  stable read, handled Tool failure and an MCP-shaped search/detail/report chain.

Every case stores ordinary Python source, JSON input, exact output, and when
needed the generated Python Tool path plus exact canonical argument/result or
error fixtures. The origin is pinned to `pysolate/agent-workloads@v1`. The
corpus loader rejects unknown fields, duplicate identities, malformed JSON,
unknown tools, ambiguous fixtures and unconsumed calls.

## Boundary lanes

The script also runs:

1. a real private-workspace edit and a failed Run whose files remain
   inspectable until cleanup;
2. a real MCP stdio server discovered through the official Go SDK and called
   from generated Guest Python;
3. a real service process kill after an idempotent Host effect commits, followed
   by SQLite recovery and one-effect verification.

These remain explicit tests rather than pretending workspace state and ordered
side effects fit the corpus's read-only lookup-table replay model.

## What this evidence means

The pack answers whether the current artifact and Tool ABI still execute these
representative fixed programs correctly and how long their measured phases take
on the current host. It does not measure model code-generation quality, claim a
population distribution for all agents, or justify runtime policy by itself.

Use the data to decide what to build next:

- a recurring unsupported common case is evidence to expand the artifact or
  Host capability boundary;
- long Tool waits in workspace workflows are evidence for ordinary/workspace
  running-slot yielding;
- rare synthetic patterns are not sufficient reason to complicate scheduling;
- deterministic-computation and replay research should first preserve this
  pack's exact source, input, effect and output contracts.
