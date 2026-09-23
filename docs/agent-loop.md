# Standalone Pysolate agent loop

`examples/agent-loop` is a deliberately small CLI harness for a model-driven CSV
analysis:

1. create a private workspace containing a fixed synthetic `sales.csv`;
2. ask one OpenAI-compatible Chat Completions endpoint for Python;
3. run that Python in a fresh Pysolate Guest with `RunWorkspace`;
4. return the value, stdout, or controlled error to the model so it can repair;
5. accept completion only after a successful Guest run created a non-empty
   `REPORT.md`; and
6. export only that report to a caller-selected, new output path.

The workspace is private and disposable, but it persists between execution
attempts. Python globals do not: every attempt is a new Guest. The only
Python-callable Host tool is the readonly, fixed local
`catalog.category_for_sku(sku=...)` map (`A-1 -> widgets`, `B-2 -> gadgets`).
It does not perform network or external writes. CSV and tool results are data,
not instructions.

## Run

For the selected DeepSeek live acceptance, the generic adapter can use the
existing DeepSeek credential without putting it in source or command-line arguments:

```sh
export OPENAI_API_KEY="$DEEPSEEK_API_KEY"
export OPENAI_BASE_URL='https://api.deepseek.com'
export OPENAI_MODEL='deepseek-flash'

mkdir -p ./out
go run ./examples/agent-loop \
  -guest dist/pysolate.wasm \
  -output ./out/sales-report.md
```

The adapter appends `/chat/completions`, so both `https://api.deepseek.com` and
an OpenAI-style base ending in `/v1` are supported. The example does not build
the Guest. A real DeepSeek run is recorded below.

`-output` is required. It uses no-overwrite creation (`O_EXCL`); an existing
path fails rather than being replaced. The temporary private workspace is
removed on both success and error. Progress is written to stderr and the final
report path, model-turn count, executed-attempt count, and Host-tool call count are written to stdout.
The API key is used only in the HTTP `Authorization` header and is never sent
to a Guest or included in errors.

### Flags and environment

| Flag | Default | Environment | Meaning |
| --- | --- | --- | --- |
| `-guest` | `dist/pysolate.wasm` | — | Pysolate Guest artifact |
| `-output` | required | — | new exported report path |
| `-model` | `gpt-4o-mini` | `OPENAI_MODEL` | model name |
| `-base-url` | `https://api.openai.com/v1` | `OPENAI_BASE_URL` | OpenAI-compatible API base; `/chat/completions` is appended |
| `-trace` | off | — | new private JSONL code/result trace |
| `-max-turns` | `6` | — | model-response limit |
| `-deadline` | `2m0s` | — | total agent deadline |
| `-execution-timeout` | `10s` | — | per-Guest execution cap, bounded by remaining total time |
| `-max-response-bytes` | `262144` | — | maximum provider response body |
| `-max-context-bytes` | `1048576` | — | maximum serialized conversation |
| `-max-output-bytes` | `262144` | — | maximum model/Guest/report output |
| — | — | `OPENAI_API_KEY` | required provider credential |

The provider performs one HTTP request per model turn and has no automatic
transport retry. The loop supports exactly one `execute_python` function call
per response. Wrong tool names, malformed arguments, multiple calls, missing
call IDs, unsupported call types, and premature final prose become controlled
messages for a later turn; they do not panic. Generated code is also rejected
for common shell/process capabilities (`subprocess`, `os.system`, `os.popen`,
and similar forms).

## Live DeepSeek validation

A real run against `https://api.deepseek.com`, using the available model
`deepseek-flash`, completed in **4 model turns, 3 Guest executions and 6 readonly
Host-tool calls**. The exported report was read back: 3 rows, total amount 35,
widgets 2 and gadgets 1, with zero unresolved categories. The model inspected
the CSV, queried the catalog and wrote the report through the actual runtime.

An earlier bounded run exhausted its turns. Integration identified two
important contracts: intermediate execution output must be returned even before
a report exists, and tool descriptions must explicitly state keyword-only
arguments and the JSON return shape. The descriptions now come from the same
Host manifest used for execution. Tests preserve these behaviors.

The loop checks completion/file existence, not general analytical correctness.
The synthetic fixture result above was independently checked; other reports
still need application-specific validation. Model turn counts are observations,
not a guarantee.

For local diagnosis, `-trace /private/new-file.jsonl` writes code/results with
mode 0600 and no overwrite. It does not log Authorization headers or provider
reasoning fields, but task data and generated code can be sensitive. Never
upload it automatically. Optional provider `reasoning_content` is preserved in
conversation state for compatible tool continuation, not printed or traced.
Each request asks for at most 2,048 completion tokens. Byte, turn and time limits
are enforced locally; responses marked incomplete fail explicitly.

## Tests

Run the example package tests with:

```sh
go test ./examples/agent-loop
```

Test names:

- `TestValidateExecuteArgumentsRejectsWrongFunctionShapes` — rejects malformed,
  extra-argument, non-string, and process-capability tool arguments.
- `TestOpenAIProviderReturnsControlledProviderFailure` — checks status failures
  do not expose the provider response body.
- `TestExportReportDoesNotOverwrite` — verifies no-overwrite export semantics.
- `TestRunAgentStopsAtMaxTurns` and `TestRunAgentHonorsCanceledContext` — cover
  model-turn and total-deadline cancellation limits when a Guest artifact is
  available.
- `TestAgentLoopRepairsWithFreshGuestAndExportsReport` — uses a local fake HTTP
  OpenAI-compatible provider, runs a real Guest when `PYSOLATE_GUEST` (or
  `dist/pysolate.wasm`) exists, repairs an erroneous first program, exercises
  the readonly catalog tool, checks report contents, and checks that Python
  state from the failed Guest is not shared.

Tests fail when the required Guest artifact is missing. Fake-provider tests
verify deterministic failure/repair and fresh-Guest behavior; they do not
substitute for the separate live-provider validation above.
