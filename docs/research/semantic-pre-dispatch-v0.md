# Semantic pre-dispatch prototype v0

Status: **Track E first consumer implemented; default-off; G2 pending**

## Accepted shape

The prototype consumes only the G1 subset: one verified module-entry,
`necessarily_reached`, single-occurrence scalar read with exactly one positive call site,
no semantic barrier or unknown module effect, and a sealed eligible live-only
`PreDispatchContract`. Captured-playback reads remain excluded because the
prototype does not mint a second transport transcript. It does not admit writes,
unknown effects, conditional calls,
coalescing, caching, hoisting, replay or backend inference.

`MechanismSet.SemanticPreDispatch` is false by default. Enabling it requires both
`SemanticAnalysis` and `StagedObservation`.

## Authority and lifecycle

1. `CanPreissue` produces an opaque `QualifiedCall` bound to the exact occurrence,
   canonical arguments, capability plan/grant/handler, epochs, privacy, lineage and
   budget-reservation identity.
2. `PreDispatchBudget` atomically consumes that reservation before physical start.
3. `SemanticPreDispatch.Start` requires an explicit Host `PreDispatchLauncher`; scheduler
   capacity is acquired first, and `Launch` accepts exactly one eventual execution with no
   ambiguous error return. The semantic package never creates an implicit goroutine or task.
4. `capability.PreparedPreDispatch` executes the sealed eligible read handler exactly
   once and validates the output contract.
5. The result is held in the existing Run-private `streaming.StagedObservation`, now
   using the `semantic_call` v2 identity form. No second cache or Guest ABI is added.
6. At the unchanged Python Host-call boundary, `capability.Broker` revalidates the normal
   capability/schema contract and atomically consumes the exact staged result. A mismatch
   is an error; it never issues a duplicate live request.
   Handler and invalid-result outcomes are staged as typed logical outcomes, preserving
   the same baseline Broker error code/message and exception placement.
7. `ExecuteSemanticPreDispatch` owns start-to-terminal lifecycle. The controller owns a
   child physical context, so either Broker failure finalization or wrapper failure cancels
   a running read before waiting for its terminal disposition; successful Runs that never
   claim a completed result mark it orphaned. The Wazero Broker path finalizes staged work
   on both success and failure.

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

The checked-in machine report
[`semantic-predispatch-experiment.json`](../evidence/semantic-predispatch-experiment.json)
runs five alternating real-Guest baseline/optimized pairs with a bounded 1 s physical
read. Both conditions produce the same result digest, one logical call and one physical
call per trial. The optimized condition additionally records exactly one
issue/start/finish and zero rejected claims. On this run the baseline median was
3,214,550 µs, the optimized median was 2,196,151 µs, and measured median critical-path
savings were 1,018,399 µs. Report SHA-256:
`9ca672d010cad1f6b191919e4a2f7bc972048b57db1b17c3da7cb39b4e49bcf7`.

Reproduce with:

```sh
go run ./research/effectgraph/cmd/semantic-predispatch-experiment \
  -artifact /tmp/pysolate-overlay-f64be2d.wasm \
  -trials 5 -delay 1s \
  -output docs/evidence/semantic-predispatch-experiment.json
```

Current gates pass:

- full Go tests and build;
- focused race tests for runtime, capability, streaming, semantic, Wazero and E2E;
- `go vet ./...`;
- Guest Python 78/78 and compileall;
- real-Guest semantic pre-dispatch, legality and overlay E2E;
- exact Broker-response differential tests for handler and invalid-result exceptions;
- cancellation, unclaimed disposition, budget exhaustion, dynamic mismatch,
  captured-playback rejection and explicit-enable regressions;
- Linux ARM64 execution of cross-compiled `semantic`, `capability`, `streaming` and
  experiment-validator test binaries. SHA-256 respectively:
  `eb79d31287ab984830eea9f56e72de8f36db7a70af565d8f794e153c94cf087d`,
  `e7c73de57f358de4f6f523944d978f6da97bbd186d965d3c858c5f7b185d1e92`,
  `a890cd69ce53877936890e2ac65e9ccfee1bef90086af5ca51c75e3aa3f717a2`,
  `4a17a63d876a8d7443ce80a95472e35b61a98c0c172adba600ad04a88a2b673c`;
- `git diff --check`.

G2 remains closed until an independent post-fix review reports no blocking finding.
