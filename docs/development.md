# Development

## Scope

Read the canonical documents before changing runtime behavior:

1. `docs/plans/2026-07-22-agent-python-runtime-handoff.md`
2. `docs/plans/2026-07-22-agent-python-runtime-implementation-plan.md`
3. `docs/architecture.md`
4. `docs/threat-model.md`
5. `docs/adr/`

The project is independent from prior domain-specific runtime experiments. Do not introduce unrelated protocols, compatibility exports, or product-specific request fields.

## Local cheap gates

```bash
python3 tools/verify_sources_lock.py
python3 -m unittest discover -s tests -v
python3 -m unittest discover -s guest/tests -v
go test ./...
go test -race ./...
go vet ./...
go build ./...
git diff --check
```

These gates validate contracts and Host code. They are not WASI execution evidence.

## Linux/WASI evidence

The guest workflow must build the actual `wasm32-wasip1` artifact from pinned inputs, validate imports/exports and hashes, and pass that exact artifact to Go/wazero E2E tests. A native Python run, source-contract test, cache hit, or uploaded filename does not prove guest behavior.

The first COW strategy requires an explicit fixed-memory artifact:

```bash
AGENT_RUNTIME_COW_FIXED_MEMORY=1 guest/build/build-guest.sh
```

This keeps the default artifact's growable `128 MiB → 512 MiB` contract unchanged, while the COW artifact binds `initial_pages == maximum_pages == 2048` in `manifest.json`. Do not request `cow-ready-single-use` with an unbound or growable artifact.

## TDD and commits

For behavior changes:

1. write one failing test;
2. run it and confirm the intended RED;
3. implement the smallest GREEN change;
4. run focused and relevant full gates;
5. inspect the diff;
6. create a signed conventional commit;
7. push and inspect the corresponding Actions run.

Do not commit large generated guest artifacts. Tiny synthetic ABI fixtures are allowed when their source and reconstruction test are checked in.

## Publication boundary

CI may package private Actions artifacts. Tags, GitHub Releases, public visibility, package publication, deployment, and paid infrastructure require a separate explicit decision.
