# Local durable Run export and offline replay

`pysolate-replay` is a local-only, explicit operation for inspecting and
re-running a completed durable Run. It does not provide a service endpoint,
network upload, corpus format, or generic ledger.

## Export

```sh
go run ./cmd/pysolate-replay export \
  -db /private/path/durable.sqlite \
  -run run-id \
  -out /private/path/run.bundle.json
```

The database must already exist. The CLI opens SQLite with `mode=ro`, without
migrations or permission changes. Export reads one `completed` or `failed` Run
and its existing ordered `calls`/`waits` rows. It does not claim the Run,
create/resume it, or update history. The output is one versioned JSON file,
created with `O_EXCL` and mode `0600`; an existing output is never replaced.

The bundle is sensitive. It contains the full source, JSON inputs, seed,
artifact identity, environment version, tool metadata, every call's
arguments and wire outcome (including errors), and the terminal output.
The file carries a privacy warning and must remain local. There is no automatic
upload or network API.

Only ended successful or Python-failed Runs are supported. Export rejects
active, waiting, blocked and cancelled Runs, pending/waiting calls, unresolved
waits, malformed outcomes, non-contiguous history, and incomplete terminal
results. An empty call history is valid for a pure-Python Run.

Tool Python paths are persisted in new durable declaration metadata. An older
Run whose tool declaration does not contain `python_path` is rejected rather
than guessing a path. Existing Runs can still resume through the normal durable
runtime using their original declaration contract. This is an export-only
compatibility boundary; the export
schema and database schema are not widened to infer missing provider metadata.

Bundles are not signed or authenticated. Replay detects divergence from the
provided bundle, not coordinated edits to both its program and expected results.
Keep the original private bundle when investigating a failure.

## Offline replay

```sh
go run ./cmd/pysolate-replay replay \
  -bundle /private/path/run.bundle.json \
  -guest /private/path/pysolate.wasm
```

Replay verifies the artifact SHA-256 before constructing a fresh Guest. The
Guest receives the original source, inputs and seed. The tool catalog contains
only non-dispatching stubs. A strict journal matches sequence, generated call
ID, canonical tool name, operation key and arguments (normalizing JSON whitespace
and Go HTML escaping introduced by persistence, while preserving values and order), then returns
the recorded outcome. It preserves ordered duplicate calls and error
outcomes. The journal never invokes its `next` callback or a real Tool.

Missing, unmatched or excess calls, altered arguments, altered ordering,
unknown tool requests, incomplete outcomes, artifact mismatches, and terminal
result/error/stdout differences fail closed. A Python `try/except` cannot turn
a journal mismatch into a successful replay. Replay never opens the durable
database and never mutates the bundle.

No workspace is mounted. Source that uses filesystem/workspace access or an
import that depends on workspace state fails under the Guest instead of being
silently granted a filesystem. Writable workspace continuation is a different
feature and is not part of this bundle format.

CLI failure paths intentionally print only `pysolate-replay: operation failed`;
private source, inputs, arguments, outcomes and Python exception text are not
printed by the command.
