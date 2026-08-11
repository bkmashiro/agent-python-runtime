# Development

## Host tests

```bash
go test ./...
go vet ./...
```

## Build the Guest

The Guest build uses pinned CPython/WASI inputs and writes a distribution directory:

```bash
guest/build/build-guest.sh
```

Use the resulting `agent-python-runtime.wasm` with its `manifest.json`, `import-inventory.json` and `import-qualification.json` sidecars.

## Real-Guest verification

```bash
AGENT_RUNTIME_GUEST=/absolute/path/to/agent-python-runtime.wasm \
  go test ./integration/e2e -count=1 -v
```

The focused suite exercises:

- ordinary Python execution;
- fresh globals and timeout recovery;
- bounded WASI workspace behavior;
- Host-derived `csv` admission;
- typed in-memory workspace tools;
- Host-projected rooted workspace and complete capsule continuation;
- Host-authored receipts.

## Manual PoC

Build the CLI:

```bash
go build -o /tmp/apyrun ./cmd/apyrun
```

Create a Host config with a base profile and `workspace_files`, then pipe an Agent request containing only `run_id`, `code` and `inputs`. See [operator-cli.md](operator-cli.md).

## Change discipline

Keep the active implementation small:

- do not add a service, database, scheduler or new protocol to solve a local PoC problem;
- prefer conservative rejection over a complex parser;
- add a dependency only when the core execution proof cannot be expressed without it;
- preserve real-Guest checks for authority boundaries;
- put completed research findings in [research-history.md](research-history.md), not in dormant production code.
