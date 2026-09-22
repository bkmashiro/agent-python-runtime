# Deterministic corpus replay

`cmd/pysolate-corpus` runs frozen Python programs through the real Guest and answers Host-tool calls from exact fixtures. It is intended for repeatable runtime, artifact, preparation-mode and Tool ABI regression testing. It does not call an LLM during measurement.

## What each lane measures

- **HumanEval reference lane:** executes the dataset's canonical solution and tests. It measures Python compatibility and execution correctness, not code-generation accuracy.
- **BFCL replay lane:** turns ground-truth calls into fixed namespaced Python calls, then matches each canonical tool identity and canonical JSON arguments against a frozen result. It measures dynamic tool injection, the single Host-call ABI and normal/PLM execution. It does not measure function selection by a model, and BFCL does not provide real tool return values.
- **Repository demo lane:** a small committed fixture for presentations and smoke tests.
- **Agent workload lane:** 20 fixed common Python and Tool-enriched programs in
  `examples/corpus/agent-workloads.jsonl`; see [`workload-pack.md`](workload-pack.md).

Keeping generation outside the timed run separates model variance from runtime regressions. A later model evaluation can generate code once, review/freeze the accepted program and add it as another corpus case.

## Run the committed fixture

```sh
./demos/05-corpus-replay.sh
# Equivalent:
go run ./cmd/pysolate-corpus \
  -guest dist/pysolate.wasm \
  -corpus examples/corpus/demo.jsonl \
  -prepared copy -iterations 2
```

The command prints one JSON object per sample and a final summary. `setup_ns` is separate from `run_ns`; a nonzero exit means setup, execution, replay consumption or expected-output validation failed.

Run the broader fixed workload lane with `make workloads`. Workspace mutation,
real MCP transport and durable process recovery remain separate acceptance
lanes because schema-v1 replay fixtures intentionally model exact read-only
lookups rather than ordered effects.

## Import pinned upstream data

The importer has no third-party Python dependencies. Do not commit upstream datasets into this repository. Record the exact Git revision in every generated case:

```sh
git clone https://github.com/openai/human-eval.git /tmp/human-eval
git clone --filter=blob:none --sparse \
  https://github.com/ShishirPatil/gorilla.git /tmp/gorilla

git -C /tmp/gorilla sparse-checkout set \
  berkeley-function-call-leaderboard/bfcl_eval/data

HE_REV="$(git -C /tmp/human-eval rev-parse HEAD)"
BFCL_REV="$(git -C /tmp/gorilla rev-parse HEAD)"

python3 tools/import-corpus.py humaneval \
  --input /tmp/human-eval/data/HumanEval.jsonl.gz \
  --output /tmp/humaneval.jsonl \
  --revision "$HE_REV"

BFCL_DATA=/tmp/gorilla/berkeley-function-call-leaderboard/bfcl_eval/data
python3 tools/import-corpus.py bfcl \
  --questions "$BFCL_DATA/BFCL_v4_simple_python.json" \
  --answers "$BFCL_DATA/possible_answer/BFCL_v4_simple_python.json" \
  --output /tmp/bfcl-simple.jsonl \
  --revision "$BFCL_REV" \
  --category simple_python
```

Run a bounded smoke before a larger campaign:

```sh
go run ./cmd/pysolate-corpus -corpus /tmp/humaneval.jsonl -limit 10
go run ./cmd/pysolate-corpus -corpus /tmp/bfcl-simple.jsonl -limit 10
go run ./cmd/pysolate-corpus \
  -corpus /tmp/bfcl-simple.jsonl -limit 10 -execution plm
```

`-case` selects one exact ID. `-prepared` accepts `fresh`, `copy`, or Linux-only `cow`. `-iterations` replays the same fully consumed fixtures against the same prepared Runner, while every Run still receives private Guest state.

## Corpus v1

Each JSONL record contains:

- `schema_version`: currently `1`.
- `id`, `source`, `inputs`, `expected`: frozen executable case and output oracle.
- `tools`: canonical Host identities, Python paths and optional descriptive schemas.
- `replay`: exact tool identity + canonical argument JSON mapped to a value or error; `count` permits a repeated identical call.
- `origin`: upstream dataset, immutable revision and upstream case ID.

The loader rejects unknown fields, duplicate IDs/tools/replay keys, invalid JSON, unknown replay tools, ambiguous value/error fixtures and unconsumed fixtures. Replay dispatch never uses the case ID and never treats `expected` output as executable authority.

Current v1 replay fixtures are stable read-only calls, so BFCL imports may opt into PLM early reads. Stateful writes, ordering-sensitive side effects, private workspace mutations and prefix-stream scheduling are deliberately excluded. Those need a richer event trace rather than pretending an unordered lookup table preserves side-effect semantics.

## Provenance used during implementation

The importer and smoke path were checked against:

- OpenAI HumanEval revision `6d43fb980f9fee3c892a914eda09951f772ad10d` (164 cases).
- Gorilla BFCL revision `6ea57973c7a6097fd7c5915698c54c17c5b1b6c8` (`simple_python`: 400 cases; `parallel`: 200 cases).

These counts are observations of those pinned revisions, not permanent dataset guarantees.
