# Framework integration test drive

This guide is for evaluating Agent Python Runtime as an isolation layer inside an existing agent or tool-use benchmark.

## Information needed from the framework

Before writing an adapter, identify:

1. the repository revision and its supported Python/Go versions;
2. the tool-schema format and tool-call envelope;
3. where model-generated code or tool calls are intercepted;
4. the expected task result format;
5. cancellation and timeout behavior;
6. whether tasks require read-only, reversible, compensatable, or irreversible effects;
7. the benchmark's fixture reset and scoring interface.

Keep the framework's orchestration and model interaction outside the guest. The sandbox should receive only one bounded execution request and the capabilities explicitly selected by the host.

## Phase 1: repository smoke test

Run the host-side gates first:

```bash
go test ./...
python3 -m unittest discover -s tests -v
python3 -m unittest discover -s guest/tests -v
```

On Linux x86-64, build the base guest and run real E2E:

```bash
AGENT_RUNTIME_ARTIFACT_PROFILE=base guest/build/build-guest.sh
AGENT_RUNTIME_GUEST="$PWD/dist/agent-python-runtime.wasm" \
  go test ./integration/e2e -count=1 -v
```

Record the repository revision, guest SHA-256, manifest, OS/architecture, Go version, and test output.

## Phase 2: minimal framework adapter

Start with one deterministic, read-only fake tool. Do not connect real credentials or network services during the adapter bring-up.

The adapter should:

1. translate framework code and public task inputs into an `abi/v1` run request;
2. register the fake tool in the host capability registry;
3. grant only that tool for the current run;
4. execute through the backend-neutral `engine.Runner` interface;
5. translate the validated JSON result back to the framework;
6. retain host-authored receipts and failure classes separately from model text.

The guest must not receive provider URLs, headers, credentials, filesystem paths, or host policy fields.

## Phase 3: required isolation cases

Run these cases before measuring benchmark quality or latency:

| Case | Expected result |
|---|---|
| pure computation | valid bounded JSON result |
| allowed fake tool | call succeeds and emits a host receipt |
| ungranted tool | denied before external work |
| malformed arguments | schema rejection |
| guest exception | bounded error; next run remains healthy |
| infinite loop | host timeout/cancellation |
| oversized result | response bound enforced |
| environment/filesystem/socket probe | no ambient authority |
| state written in run N | absent in run N+1 |
| concurrent runs | independent results and receipts |

Treat these as correctness gates. A task-success score cannot override a failed isolation case.

## Phase 4: compare isolation mechanisms

Use the same task inputs and host capability policy for each mechanism:

1. native subprocess baseline, if the framework already has one;
2. WASI `fresh`;
3. `single-use-preinitialized`;
4. Linux `cow-ready-single-use`;
5. profile-specific COW warmup, such as `numpy-ready-v1`, when applicable.

Measure at least:

- end-to-end request latency;
- preparation/import time;
- execution time;
- peak RSS/PSS and ready-instance memory;
- timeout and cancellation recovery;
- denied-call behavior;
- cross-run state leakage;
- task success and scorer output.

Do not compare a native fresh process against a persistent or prepared WASI request without naming the different preparation boundaries.

## Phase 5: effectful tools

Keep real side effects out of the first test drive. If the framework later requires writes, classify each tool as read-only, reversible, compensatable, or irreversible and keep commit/approval authority in the host. Guest code must not be able to self-authorize a commit.

## Acceptance criteria

The first integration is ready for broader experiments when:

- the exact guest artifact and framework revisions are recorded;
- all required isolation cases pass;
- task results are scored by the framework's normal scorer;
- host receipts, guest output, and model text are not conflated;
- no real credential or provider endpoint is present in fixtures or logs;
- failures leave the next run healthy;
- benchmark scripts and derived summaries are reproducible from retained raw evidence.
