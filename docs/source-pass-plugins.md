# Source transforms and runtime configuration

Runtime mechanisms are configured directly through `RunConfig.Mechanisms`. COW,
prepared execution, cache retention, single-flight, child fanout and cold-I/O
residency do not register passes or hash an intermediate configuration.
`wazero.Factory` accepts the configuration and the actual Host resources it needs.

```go
config := runtimeconfig.DefaultRunConfig()
config.Mechanisms.PreparedRuntime = true
config.Mechanisms.MemoryCOW = true
runner, err := (wazeroengine.Factory{}).New(ctx, artifact, config)
```

The existing mechanism dependencies and platform checks remain. For example,
COW requires prepared execution, and PLM requires a capability Broker. Invalid
combinations fail before Guest execution.

## Actual source transforms

`runtime/sourcepatch` contains three concrete transforms:

- `PureScalarCSE`: common-subexpression elimination for admitted scalar code.
- `PureScalarFold`: constant folding for admitted scalar code.
- `PLMCapabilityCalls`: same-Guest Prepare–Linearize–Materialize lowering.

CSE and folding are narrow correctness demonstrators, not established end-to-end speedups. Their historical [CSE](evidence/source-pass-plugin-v1.json) and [folding](evidence/pure-scalar-fold-paper-pass-v1.json) measurements remain unchanged.

Create the transform once, then call its typed entry point. There is no plugin
registry, string-based dispatch, `Enable` step or `Factory.Passes` field.

For CSE and folding:

```go
pass, err := sourcepatch.NewPureScalarCSE(passregistration.SemanticAnalyzerSHA256)
execution, err := pass.Execute(ctx, analyzer, runner, request)
```

The transform uses a qualified analysis Guest. Before derived execution starts,
a failed or inapplicable transform may run the original request. Once derived
execution starts, an error is returned without replaying the original program.
Generic pure transforms do not authorize capability or workspace effects.

## PLM

```go
config.Mechanisms.SplitPhaseCalls = true
runner, err := (wazeroengine.Factory{BrokerFactory: brokerFactory}).New(ctx, artifact, config)
pass, err := sourcepatch.NewPLMCapabilityCalls(passregistration.SemanticAnalyzerSHA256)
execution, err := pass.Execute(ctx, runner.(*wazeroengine.Engine), request,
    plan.PythonPrelude(), sourcepatch.PLMCapabilityProjections(plan))
```

These are abbreviated call-site examples; production callers must handle each
error before using the returned value.

PLM lowering runs in the same exact Guest that executes the final program. Host
capability declarations provide eligibility, temporal semantics and the projection
allowlist. The Run-owned `SplitPhaseTable` owns prepared work. Python receives the
value or error at the original call. Streamed source may prepare qualified PLM
candidates through that same owner; there is no separate pre-dispatch executor.

The ordinary, untransformed path calls `runner.Run(ctx, request, plan.PythonPrelude())`.
Skipping the explicit transform call is sufficient; it does not require a second
registry or a matching disabled-pass record.

## Prepared values and regions

Value slots use `valueslot.PythonPrelude(slotID)` and a Host-owned `valueslot.Table`.
Set `config.Mechanisms.ValueSlots` and provide that table to the Factory. There is
no `PreparedValuePass` wrapper.

Prepared-region and NumPy preparation remain with their existing runtime owners.
They are not dummy entries in a shared optimization catalog.

## Remaining metadata

Source registration and patch identities still bind an admitted source transform
to the exact Guest response. `runtime/passregistration` now describes source
registrations only; it no longer defines runtime optimization passes or analyzer-free
value-binding passes. Historical catalogs and their measurements remain in Git
history and the dated research documents, not in production dispatch.

See [execution core](execution-core.md), [PLM semantics](research/logical-time-plm-v1-contract.md),
and [Prepared Family](prepared-family-v1.md).
