# Durable execution

`runtime/durable` reconstructs a logical Run in a fresh deterministic Guest after an interrupted attempt. SQLite stores the program, inputs, environment, tool declarations, call outcomes and waits. Python heap, linear memory and active stacks are recomputed, not persisted.

## Small API

- `Open(path)` opens a local SQLite store.
- `NewRunner(store, artifact, identity, environmentVersion, tools)` creates the execution driver.
- `Create(ctx, definition)` saves a new Run without executing it.
- `Resume(ctx, runID)` replays recorded calls, then continues live execution.
- `Decide(ctx, waitID, decision)` saves an approval/wait outcome.
- `Cancel(ctx, runID)` records terminal cancellation and signals an attempt owned by this Runner.
- `Close(ctx)` closes the compilation cache when no attempt is active.

A completed or failed Run returns its saved final payload. `ParkError` identifies a persisted wait. It exits the current Wasm invocation and cannot be caught by Python `try/except`. Infrastructure errors leave the logical Run retryable. History or environment mismatches block automatic recovery.

## External declarations

Every tool declares its recovery mode. Pysolate does not infer these properties:

- `RetrySafe`: an unresolved call may execute again.
- `Idempotent`: retries use `OperationKey(ctx)`; the provider must actually deduplicate that key.
- `Lookup`: the resolver reports `done`, `pending`, `safe_to_dispatch` or `unknown`. A missing record is not automatically proof that dispatch is safe.
- `Manual`: first dispatch is allowed; an unresolved call blocks automatic recovery.
- `WaitMode`: a persisted local wait supplies a later decision through normal Broker result validation.

Run IDs must be globally unique within the provider's operation-key namespace and must not be reused. The stable key is `runID/sequence`.

Tool Specs, grant identities and recovery modes are saved once with the Run and compared on resume. Handler, resolver and wait-function implementation changes remain the Host's responsibility to version. The runtime does not inspect function bodies. Credentials remain in the Host.

## Durability and ownership

The store uses SQLite WAL with `synchronous=FULL`. Intent commits before dispatch; the full Broker response commits before Guest delivery. Outcomes and decisions are single-assignment. Late results may be recorded without reopening a terminal Run.

One OS lock owns a Run attempt. Symlink paths resolve to the same database/lock; hardlinked database files are rejected because separate WAL names are unsafe. Brief initialization locking handles concurrent first opens. These are local filesystem semantics, not a distributed lease service.

Cancellation prevents later journal admission and terminal-result replacement. Other Runner/process owners observe persistent cancellation at subsequent boundaries; this API does not promise immediate interruption of their CPU work. Already dispatched external operations can still finish, even with same-Runner cancellation if a handler ignores its context.

Keep the database directory on persistent local storage. SQLite's WAL and shared-memory files belong with the database; use SQLite backup facilities rather than copying a live main file alone. The store is private plaintext local state. Protect access to recorded inputs and results.

## Bounds and initial scope

The default logical payload limit is 64 MiB per Run (an engineering limit). It counts retained definitions, arguments, outcomes and waits. It does not bound SQLite page/index/WAL overhead. Results are never truncated to fit: storage failure or a limit error stops delivery. A provider effect may then remain unresolved and must follow its recovery declaration.

The initial mode uses fixed seed/virtual Guest clocks, no mounted workspace, and no PLM, prefix, prepared/COW or residency policy. Compilation is shared through a Runner-owned cache. Real Host deadlines remain real. Same-artifact Python/NumPy cases were tested across independent processes; arbitrary extensions, concurrent scheduling and cross-platform floating-point equivalence are outside this claim.

Store schema 2 refuses older unpublished prototype databases. There is no automatic code/history migration. Keep the matching artifact and declared environment available for recovery.

## Run the example

The example's provider is a **separate SQLite fixture**, not an external payment service. Its transaction is independent of the Run journal.

```sh
go build -o /tmp/durable-demo ./cmd/durable-demo
GUEST=dist/numpy-core/agent-python-runtime-numpy-core.wasm
/tmp/durable-demo create -db /tmp/run.db -provider-db /tmp/provider.db -guest "$GUEST"
/tmp/durable-demo resume -db /tmp/run.db -provider-db /tmp/provider.db -guest "$GUEST"
/tmp/durable-demo decide -db /tmp/run.db -decision true
/tmp/durable-demo resume -db /tmp/run.db -provider-db /tmp/provider.db -guest "$GUEST" -read-value 999
/tmp/durable-demo provider-status -provider-db /tmp/provider.db
```

The first resume parks for approval. The second uses the saved read despite the changed fixture value. Use a fresh database pair for each example.

Reproduce the two process-crash windows:

```sh
python scripts/test-durable-crash.py --binary /tmp/durable-demo \
  --guest "$GUEST" --output-dir /tmp/durable-crash-fresh
```

See [the verification record](../research/durable-replay/README.md) for actual process and VM hard-stop results and their limits.
