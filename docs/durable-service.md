# Durable local service

`cmd/pysolate-durable-server` exposes the durable Runner as a bounded HTTP
control plane for a trusted local harness. The service owns execution,
persistence, replay, effect recovery and local resource admission. Goal
selection, model turns, user interaction, retries across tasks and publication
remain harness responsibilities.

## Start

A verified `agent-core` artifact is required:

```sh
make verify-artifact
go run ./cmd/pysolate-durable-server \
  -guest dist/pysolate.wasm \
  -db /tmp/pysolate-runs.db \
  -listen 127.0.0.1:8081
```

The standalone command intentionally grants no Host tools. It is suitable for
pure Python and for inspecting the durable API. An embedding application builds
a `durable.Runner` with its trusted, versioned Tool catalog and passes it to
`service/durable.New`; this is how HTTP, databases or MCP-backed effects enter
the service without enlarging the Guest artifact.

## API

Create a Run. The service pins the active artifact digest and the configured
environment version into the persisted definition:

```sh
curl -sS -X POST http://127.0.0.1:8081/v1/durable/runs \
  -H 'content-type: application/json' \
  -d '{"id":"demo-1","source":"result = inputs[\"value\"] + 1","inputs":{"value":41},"seed":"demo-seed"}'
```

Execute one fresh bounded attempt and inspect the Run:

```sh
curl -sS -X POST http://127.0.0.1:8081/v1/durable/runs/demo-1/attempts
curl -sS http://127.0.0.1:8081/v1/durable/runs/demo-1
```

Other endpoints are:

- `GET /healthz`: readiness plus running, resident, external-Tool and queued counts;
- `POST /v1/durable/runs/{id}/cancel`: durable cancellation;
- `POST /v1/durable/waits/resolve`: resolve a persisted wait with `wait_id` and exactly one result or error.

Requests are limited to 1 MiB and reject unknown fields. Queue saturation returns
HTTP 429; conflicting lifecycle operations return 409; closed service capacity
returns 503. The attempt response represents completed, parked, blocked,
Python-failed and cancelled outcomes as structured data. Go/internal failures
remain HTTP errors.

## Crash recovery contract

The SQLite journal is the durable source of truth. A restart creates a new
Runner and new Guests from the same database. Completed Tool outcomes are
replayed. An unresolved external effect follows its provider-owned recovery
mode; Pysolate does not blindly retry an ambiguous write.

The executable acceptance test uses a real Guest and a real idempotent external
ledger Tool. It kills the service process after the effect commits but before
the attempt records completion, restarts the entire service on the same SQLite
files, and verifies two dispatches produced one external effect:

```sh
./demos/13-durable-restart.sh
```

This test is deliberately process-level. An in-process handler test cannot prove
that recovery survives loss of all live Guests and Go state.

## Ownership and current limit

The service persists definitions, calls, decisions and terminal outcomes. It
does not preserve a Python stack or serialize process memory. Recovery starts a
fresh deterministic attempt and reuses recorded effect outcomes at declared
Tool boundaries. Ordinary unrecorded nondeterminism, hidden network/filesystem
access and unsafe provider effects cannot be recovered automatically.
