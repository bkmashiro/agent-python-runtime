# Pysolate Lab Web

A private development trajectory inspector for Pysolate agent sessions.

The primary invariant is:

> **Model-visible means logged.**

The Lab reads a closed local session index and validated append-only session exports. The
default session is the reset balanced-order real-Guest workflow experiment; a second
scripted full-source fixture exercises memory, skill and subagent records. The inspector
exposes:

- system, developer, user, memory and skill context;
- effective request headers plus every raw provider chunk and its assembled-event citations;
- each exact ordered model-request context;
- provider-visible reasoning and assistant output chunks;
- tool calls and complete arguments/results;
- subagent dispatch and child-session results;
- Pysolate logical requests, Runs, physical executions and Host spans;
- workspace changes and terminal state;
- the raw hash-chained event.

The model-request context is never inferred from nearby UI events. Every `model.request`
contains the ordered prior event IDs actually supplied to that request, and validation
rejects forward, duplicate, missing-body or missing-reference entries.

## Data boundary

`public/lab-data/trajectory.json` is a synthetic, credential-free development fixture
exported from the real private recorder. It is explicitly labelled as a fixture and must
not be presented as a live model/provider run.

The source recorder stores complete bodies privately in `research/labstore` and keeps the
append-only metadata stream in a hash-chained JSONL file. The browser export materializes
those bodies for local inspection. Credentials are rejected at the recorder admission
boundary; callers must classify them as absent before storage.

A provider's hidden chain-of-thought cannot be recorded if the provider does not expose it.
Provider-visible reasoning/reasoning summaries are ordinary logged model events. The UI
must not invent hidden reasoning.

The former paired-experiment and Runtime-only debugger schemas were deliberately removed.
There is no compatibility reader or fallback fixture.

## Fixture generation

```sh
mkdir -p .hermes/trajectory-fixture

go run ./research/trajectory/cmd/trajectory-fixture \
  -store .hermes/trajectory-fixture/store \
  -output apps/lab-web/public/lab-data/trajectory.json \
  -source-commit "$(git rev-parse HEAD)"
```

The private `.jsonl` source log remains under `.hermes/trajectory-fixture/`; only the
credential-free browser fixture is checked in.

## Development

```sh
npm install
npm test -- --run
npm run build
npm run test:e2e
npm run dev
```

The current deployment is static and read-only. Live append, resume, fork and replay require
a future local Harness service; the session contract is designed so those operations use
the same append-only event stream rather than a separate debug projection.
