# Development

## Host tests

```bash
go test ./...
go vet ./...
```

For Track F semantic-region and Lab work, use the repository gate wrapper instead of
reconstructing long commands by hand:

```bash
scripts/track-f-gate.sh --list
scripts/track-f-gate.sh focused
scripts/track-f-gate.sh full
AGENT_RUNTIME_GUEST=/absolute/path/to/agent-python-runtime.wasm \
  scripts/track-f-gate.sh guest
scripts/track-f-gate.sh lab
```

`release-check` is read-only and intentionally requires an already clean, signed and
upstream-aligned tree. The wrapper never commits, pushes, deploys or substitutes a
missing Guest artifact.

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

### Playback acceptance artifact

For a persistent, machine-readable live-capture-to-offline-playback proof, build
`apyrun`, create a private evidence directory and run the repository-maintained
acceptance command:

```bash
go build -o /private/tmp/apyrun ./cmd/apyrun
install -d -m 0700 /private/tmp/pysolate-acceptance
go run ./cmd/pysolate-acceptance \
  -artifact /absolute/path/to/agent-python-runtime.wasm \
  -apyrun /private/tmp/apyrun \
  -evidence-dir /private/tmp/pysolate-acceptance \
  -output /private/tmp/pysolate-acceptance/report.json
```

The command does not build or substitute a Guest. A missing artifact, qualified
sidecar, executable or protected evidence directory exits with code 2 and an
`acceptance unavailable` diagnostic. It starts only a loopback source, runs a
fresh live Guest, publishes a protected Bundle, closes the source, runs a fresh
offline Guest and emits bounded `pysolate.playback-acceptance.v1` JSON. The
report contains identities, equality relations and counts rather than Agent
source or result/workspace bodies. Bundle and report publication are `0600` and
no-overwrite.

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
