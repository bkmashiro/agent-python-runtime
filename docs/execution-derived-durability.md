# Execution-derived durability

## Summary

Pysolate can execute ordinary agent-authored Python without requiring the author
to rewrite it as a workflow definition. External observations and effects cross
an explicit Host Tool boundary, so the runtime can record call identity,
arguments, order and outcomes as the program executes. Replay returns those
recorded outcomes at the same calls and detects history divergence.

This moves durability declarations away from every workflow's control flow and
toward the capability provider that understands the external system:

```text
ordinary Python       expresses computation and business control flow
Tool registration     declares authority, identity, version and recovery semantics
runtime execution     derives and persists the event history
```

A concise product statement is:

> Write ordinary Python. Pysolate derives durable boundaries from Tool calls and
> replays recorded outcomes automatically. Recovery semantics are declared once
> by the Tool provider, not repeated in every workflow.

The broader ownership split is documented in
[`runtime-harness-boundary.md`](runtime-harness-boundary.md).

## Contrast with workflow-first systems

Temporal requires developers to define Workflow code and move external I/O into
Activities. Workflow code must remain deterministic; Activities, timers,
signals, child workflows and side effects are explicit SDK concepts. During
replay, Temporal rebuilds Workflow state from Event History and reuses recorded
Activity results. This provides a strong programming model, but adopting it
usually means expressing an application in that model rather than running an
unchanged script.

Pysolate starts from a different constraint: the Guest has no ambient network,
Host filesystem, subprocess, credential or environment authority. Any granted
external operation appears as a generated Python Tool function:

```python
quote = market.get_price(symbol="AAPL")
approved = human.request_approval(change=proposal)
```

Both calls pass through the same narrow Host ABI. The runtime therefore observes
all granted external effects without requiring Python authors to wrap each call
in an Activity API. This is particularly useful for generated code: an agent can
write normal Python while trusted Host code owns effect safety.

Relevant Temporal references:

- [Workflows](https://docs.temporal.io/workflows)
- [Workflow definitions](https://docs.temporal.io/workflow-definition)
- [Activities](https://docs.temporal.io/activities)
- [Go SDK side effects](https://docs.temporal.io/develop/go/workflows/side-effects)

This section compares where declarations live. Temporal's distributed
orchestration, operational tooling and broader feature set remain outside
Pysolate's current scope.

## Why the Tool boundary is cleaner

The provider for an external system is best placed to answer questions such as:

- Is a retry always safe?
- Does the API accept an idempotency or operation key?
- Can an ambiguous call be looked up after a crash?
- Must an unknown outcome block for manual resolution?
- Is this a durable wait rather than an immediate effect?
- Is the call cooperative external I/O that may release a local running slot?

Declaring these properties once prevents separate scripts from assigning
inconsistent recovery behavior to the same operation. In the current durable
API, every Tool has a stable `Name` and `Version`, plus one recovery mode:

- `RetrySafe`
- `Idempotent`
- `Lookup`
- `Manual`
- `WaitMode`

`ExternalIO` is a separate Host-local scheduling declaration. It may release the
Executor's running slot while the live Guest waits, but does not alter persisted
recovery semantics.

Tool metadata discovered through MCP may help configure a provider, but remote
annotations remain descriptive. They do not independently grant authority,
enable early execution or prove that retry is safe. Trusted Host policy owns
those decisions.

## Derived history and replay

For each recorded Tool call, the durable path associates the operation with its
Run and sequence. The intended ordering is:

1. Validate the frozen source, inputs, artifact, environment and Tool snapshot.
2. Persist the Tool-call intent before external dispatch.
3. Execute or resolve the call according to the Tool's recovery contract.
4. Persist its outcome before delivering that outcome to Python.
5. On a later attempt, reconstruct a fresh Guest and execute from the beginning.
6. Match replayed call identity and arguments against history and return the
   recorded outcome instead of repeating a completed effect.
7. Stop on divergence or an unresolved unsafe operation rather than guessing.

Pysolate also controls the attempt's random seed and logical clocks. A prepared
deterministic image may reduce reconstruction cost when it was captured for the
same seed; replay and recovery semantics remain unchanged.

## What remains application logic

Tool declarations should describe capability behavior, not absorb business
control flow. The Python program still decides:

- which operations depend on earlier results;
- whether a rejected approval ends or changes the operation;
- whether to cancel an order, choose a fallback provider or ask a user;
- deadlines and compensation across multiple external systems;
- how results are transformed and which final value is returned.

For example, a billing provider can declare that `billing.charge` supports
lookup recovery. It cannot decide whether the application should cancel an
order after a declined payment.

## Limits of “automatic” durability

Pysolate can derive boundaries automatically only inside a closed execution
model:

- all external observations and effects pass through Host Tools;
- source, inputs, artifact, environment and Tool versions are frozen;
- program-visible nondeterminism is recorded or deterministically controlled;
- each effecting Tool has a trusted recovery contract;
- unknown outcomes fail or block safely.

No runtime can infer from arbitrary black-box code whether an external action
completed just before a crash or whether repeating it is safe. Pysolate does not
claim annotation-free exactly-once effects, arbitrary native-library recovery,
or instruction-stack checkpointing. Long pure computation is reconstructed by
re-execution, while completed Tool outcomes are replayed.

Writable private workspaces are also deliberately outside `RunRecorded` today.
A durable Tool stage can produce validated data, which a separate workspace Run
then turns into reviewable changes. This keeps external-effect history and
mutable filesystem publication from becoming one coupled recovery mechanism.

## Design consequence

The core advantage is moving declarations to a stable, reusable and trusted
capability boundary:

```text
workflow-first durability:
    every workflow author marks durable operations

execution-derived durability:
    ordinary Python calls granted capabilities
    capability providers declare effect semantics once
    the runtime derives history from observed calls
```

That division is especially valuable for agent-authored programs: generated
Python expresses intent, while recovery authority remains in Host code.
