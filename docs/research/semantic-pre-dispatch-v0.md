# Semantic pre-dispatch prototype v0

Status: **Track E first consumer implemented; default-off; G2 pending**

## Accepted shape

The prototype consumes only the G1 subset: one verified module-entry,
`necessarily_reached`, single-occurrence scalar read with a sealed eligible
`PreDispatchContract`. It does not admit writes, unknown effects, conditional calls,
coalescing, caching, hoisting, replay or backend inference.

`MechanismSet.SemanticPreDispatch` is false by default. Enabling it requires both
`SemanticAnalysis` and `StagedObservation`.

## Authority and lifecycle

1. `CanPreissue` produces an opaque `QualifiedCall` bound to the exact occurrence,
   canonical arguments, capability plan/grant/handler, epochs, privacy, lineage and
   budget-reservation identity.
2. `PreDispatchBudget` atomically consumes that reservation before physical start.
3. `SemanticPreDispatch.Start` requires an explicit Host `PreDispatchLauncher`; it never
   creates an implicit goroutine or task.
4. `capability.PreparedPreDispatch` executes the sealed eligible read handler exactly
   once and validates the output contract.
5. The result is held in the existing Run-private `streaming.StagedObservation`, now
   using the `semantic_call` v2 identity form. No second cache or Guest ABI is added.
6. At the unchanged Python Host-call boundary, `capability.Broker` revalidates the normal
   capability/schema contract and atomically consumes the exact staged result. A mismatch
   is an error; it never issues a duplicate live request.
7. `ExecuteSemanticPreDispatch` owns start-to-terminal lifecycle. Failed Runs cancel
   running physical reads; successful Runs that never claim a result mark it orphaned.
   The Wazero Broker path also finalizes staged work on both success and failure.

The original Python source and dynamic call remain execution authority. The semantic
overlay starts no work by itself.

## Observation surface

`PreDispatchSnapshot` distinguishes physical issue, start and finish counts, logical
claims, rejected claims and terminal disposition. The only equivalent successful shape
is `issues=starts=finishes=logical_claims=1`, `rejected_claims=0`, disposition
`consumed`. Cancelled, failed, late or orphaned physical work is not silently equivalent.

## Verification

The real CPython/WASI E2E test
`TestRealGuestSemanticPreDispatchClaimsAtUnchangedPythonCall` proves that the physical
handler starts before Guest execution, unchanged Python reaches the ordinary Broker
boundary, the staged result is consumed once, and no duplicate physical call occurs.

Current gates pass:

- full Go tests and build;
- focused race tests for runtime, capability, streaming, semantic, Wazero and E2E;
- `go vet ./...`;
- Guest Python 78/78 and compileall;
- real-Guest semantic pre-dispatch, legality and overlay E2E;
- `git diff --check`.

G2 remains closed until adversarial exception/control coverage, machine-readable
differential traces and a bounded off/control latency experiment are complete and an
independent post-fix review reports no blocking finding.
