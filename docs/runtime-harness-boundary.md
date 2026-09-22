# Runtime and harness boundary

## Position

Pysolate is the execution substrate for agent-authored Python. It accepts code,
inputs, explicitly granted capabilities, an optional private workspace and hard
resource limits. It returns a value, a Python failure, a control state and
measured resource facts.

The surrounding agent harness owns goals, model decisions and long-lived
orchestration.

A practical ownership rule is:

> If correctness depends on the Python execution position, Guest lifecycle or
> effect replay, it belongs in Pysolate. If behavior depends on user intent,
> task value or business policy, it belongs in the harness.

## Pysolate owns

### Isolated execution

- Create a private CPython/WASI Guest for each attempt.
- Expose no ambient Host network, filesystem, environment, credentials or
  subprocess authority.
- Enforce memory, message, output, Tool-call and elapsed-time bounds.
- Propagate cancellation and reliably release Guests and Tool workers.

### Capability mediation

- Inject only the granted Host Tools as natural namespaced Python functions.
- Preserve canonical Tool identity and version behind a bounded JSON ABI.
- Validate catalog/path collisions and route results to the correct Python call.
- Record call order, arguments and outcomes required for deterministic replay.

The Host or Tool provider owns credentials, authentication, transport, schema
validation and the actual HTTP, database, MCP or local implementation.

### Execution-level durability

- Persist intent before external dispatch and outcome before Guest delivery.
- Reconstruct a fresh Guest and replay completed observations.
- Detect source, artifact, environment, Tool-version, call-order and argument
  divergence instead of guessing.
- Apply the provider-declared `RetrySafe`, `Idempotent`, `Lookup`, `Manual` or
  `WaitMode` recovery mechanism.
- Return explicit `Completed`, `Parked`, `Blocked`, failed or cancelled states.

Pysolate owns this journal because replay correctness depends on the exact Tool
call position inside Python. The harness supplies decisions and external events;
it does not recreate execution history itself.

### Local resource lifecycle

- Bound running Guests, resident Guests, live external calls and queued
  admissions independently.
- Allow an opted-in `ExternalIO` Tool to release a running slot while its live
  Guest remains resident.
- Destroy a durably parked Guest while preserving enough state for later
  re-admission.
- Expose queue, Host-service, continuation-resume and lifecycle measurements.

Pysolate may provide simple local fairness and continuation progress. It does
not decide which user's goal is most valuable or place work across a fleet.

### Private-workspace containment

- Mount only a runtime-owned private workspace at `/workspace`.
- Enforce path, link, file-count, byte and depth boundaries.
- Produce snapshots, deterministic diffs and bounded change bundles.

The harness decides which source revision creates the workspace, whether to
accept a change, how to resolve conflicts, and whether to commit, push or open a
pull request.

## Harness owns

- Long-lived goals, plans and completion criteria.
- LLM selection, prompting, context construction, memory and turn budgets.
- Webhooks, timers, user messages and routing events to waiting Runs.
- Global priorities, quotas, cost policy, fleet placement and load shedding.
- Selecting which capabilities and credentials a Run may receive.
- Business retries, compensation and escalation.
- User approval UX and operator intervention for blocked effects.
- Repository publication and long-term workspace/artifact retention.

A typical interaction is:

```text
Harness selects a ready task
  -> submits/adopts one bounded Pysolate attempt
  -> Pysolate runs, replays, yields local capacity or parks
  -> Pysolate returns a structured state and execution facts
  -> Harness completes the goal, schedules another turn, subscribes to an
     event, or asks a user/operator for a decision
```

## Tool provider owns

The Tool provider forms the stable boundary between the two layers. It owns:

- implementation, transport and credentials;
- canonical identity, version and input/output contract;
- operation-key handling;
- idempotency and lookup behavior;
- recovery and local scheduling declarations.

Remote metadata such as MCP annotations may inform configuration but does not
independently grant authority or prove safe retry behavior.

## Intended API shape

The harness should not need Guest internals:

```go
attempt, err := executor.Admit(ctx, runID)
result, err := attempt.Result(ctx)

switch result.State {
case durable.Completed:
    // Feed the value back to the agent or finish the goal.
case durable.Parked:
    // Register the returned wait with the harness event system.
case durable.Blocked:
    // Request an operator/provider recovery decision.
}
```

The runtime contract can be summarized as:

> Give Pysolate code, inputs, capabilities, a workspace lease and limits. It
> executes them in isolation with bounded resources and replay-safe Tool
> boundaries, then reports exactly what happened.
