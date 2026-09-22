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
  -listen 127.0.0.1:8081 \
  -max-run-duration 30s
```

The standalone command intentionally grants no Host tools. It is suitable for
pure Python and for inspecting the durable API. An embedding application builds
a `durable.Runner` with its trusted, versioned Tool catalog and passes it to
`service/durable.New`; this is how HTTP, databases or MCP-backed effects enter
the service without enlarging the Guest artifact.

## Optional Linux COW data images

Start with `-cow-data-image -cow-data-image-seed demo-seed` to opt in to a prepared deterministic image. The default remains fresh execution. When enabled, new Runs must use the configured seed; a different seed receives HTTP 400 before persistence. If the seed flag is omitted, its value is `pysolate-durable-service-v1`.

Existing Runs also need matching artifact/environment and seed. Reopening a database does not migrate seeds. Choose the original seed for recovery or use the unprepared default. Non-Linux hosts, empty preparation seeds and unsupported artifacts are rejected.

Embedding applications use `durable.Preparation{Seed: seed, COW: true, COWDataImage: true}` for the Runner and `durableservice.Options{PreparationSeed: seed}` for the HTTP service. The HTTP seed check gives an early error; the Runner's own deterministic-state check remains authoritative.

`-max-run-duration` and `durableservice.Options{MaxRunDuration: ...}` optionally cap one HTTP attempt. Zero preserves the uncapped behavior. An attempt may set a smaller positive `timeout_ms` in milliseconds, but cannot exceed the service cap. The deadline starts before admission and therefore includes queue time; it applies to this attempt only, not the durable Run lifetime. Cancellation is cooperative, while durable journal persistence remains owned by the durable Runner. Durable attempts do not support ordinary `early_reads`.

See [COW data-image tradeoffs](cow-data-image.md), including extra retained memory. No arbitrary provider gains exactly-once semantics from this option.

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

Pass `{"timeout_ms":5000}` to the attempts endpoint to shorten the configured cap. Non-positive and over-cap values are rejected with HTTP 400; a cooperative attempt deadline returns HTTP 408 and leaves the Run resumable when no terminal outcome was persisted.

Other endpoints are:

- `GET /healthz`: readiness plus running, resident, external-Tool and queued counts;
- `POST /v1/durable/runs/{id}/cancel`: durable cancellation;
- `POST /v1/durable/waits/resolve`: resolve a persisted wait with `wait_id` and exactly one result or error.

Requests are limited to 1 MiB and reject unknown fields. Queue saturation returns
HTTP 429; conflicting lifecycle operations return 409; closed service capacity
returns 503. The attempt response represents completed, parked, blocked,
Python-failed and cancelled outcomes as structured data. Go/internal failures
remain HTTP errors.

## Read-only call history

```sh
curl -sS 'http://127.0.0.1:8081/v1/durable/runs/demo-1/history?from_sequence=0&limit=32'
```

The response has `calls`, `next_sequence`, and `has_more`. Each call exposes only `sequence`, `tool`, `version`, `state`, and `outcome_class`. Continue from `next_sequence` when `has_more` is true. The default page is 32 calls; the backing Store caps pages at 64. These are engineering limits, not Wasm limits.

Arguments, raw results, call IDs and operation keys are never included, even if a query requests payloads. Internal history-read failures return a generic error. Persisted call state cannot tell you which earlier attempt dispatched, replayed or looked up an outcome; this endpoint does not invent that timeline.

Unknown Runs return 404, malformed pagination returns 400 and writes to this route return 405. Embedding runtimes that do not implement the optional history-reader interface return 501. This is still a trusted local control plane, not a new authentication or tenant boundary.

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
