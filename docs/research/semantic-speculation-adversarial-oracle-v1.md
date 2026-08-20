# Semantic speculation adversarial oracle v1

Status: Phase 1 semantics oracle and body-free projection contract. This document does not enable a new optimisation or report Phase 3 performance.

## Outcome model

`pysolate.semantic-speculation-trial.v1` records six independent projections:

1. final-program outcome and whether final Python started;
2. eager-prefix Python execution count;
3. logical capability calls;
4. provider-visible physical attempts, result bytes and cost units;
5. physical terminal dispositions, including consumed, cancelled, orphaned, late, timed out, failed and fallback;
6. authority and workspace disposition.

The record is bound to source, Guest artifact, artifact manifest, import inventory, execution profile, capability plan, privacy partition and preregistration identities. It is canonical, sealed, strict-decoded and body-free. Provider cost is intentionally independent from attempt count: one attempt may incur more than one billing or quota unit.

## Adversarial classification matrix

| Preregistered case | Whole-file serial oracle | Semantic pre-dispatch classification | Direct evidence |
|---|---|---|---|
| `external_read_valid_suffix` | Final Python starts; one logical call produces one provider-visible physical attempt and a consumed read. | The exact qualified physical attempt may start early; final unchanged Python still makes one logical claim; one attempt is consumed. | `TestWholeFileOracleSeparatesParseReachabilityAndRuntimeFailure`; `TestSemanticPreDispatchClaimsExactlyOnceAtUnchangedBrokerBoundary` |
| `later_syntax_error` | `ErrAgentSourceInvalid` is returned before Broker construction. Final Python does not start; logical and physical counts are zero. | A previously admitted complete prefix may already have issued physical work. The invalid final suffix starts no final Python and makes no logical call; generation failure finalises the issued operation as cancelled in the exercised path. | `TestWholeFileOracleSeparatesParseReachabilityAndRuntimeFailure`; `TestSemanticPreDispatchRecordsCancelledPhysicalWithoutLogicalCallForLaterSyntaxError` |
| `later_runtime_error` | Final Python starts, reaches one logical read and then reports `python_exception` / `RuntimeError`. The read remains a consumed provider-visible attempt. | A claimed staged read must preserve the same later Python exception class. | `TestWholeFileOracleSeparatesParseReachabilityAndRuntimeFailure`; `TestSemanticPreDispatchPreservesBaselineExceptions` |
| `earlier_exception` | Final Python starts and raises before the candidate; logical and physical counts remain zero. | `necessarily_reached=false` fails qualification, so no physical preparation is authorised. | `TestWholeFileOracleSeparatesParseReachabilityAndRuntimeFailure`; `TestCanPreissueRequiresVerifiedExactNecessarilyReachedCallAndFrozenContext` |
| `branch_not_taken` | Final Python starts and succeeds without calling the capability. | A call under opaque or non-mandatory control does not qualify for pre-dispatch. | `TestWholeFileOracleSeparatesParseReachabilityAndRuntimeFailure`; `TestCanPreissueStreamingPrefixAllowsOnlyStraightLineIndependentReadLookahead` |
| `custom_wrapper_unknown_call` | Ordinary final Python retains authority over wrapper execution. | Target-Guest analysis records `sources.read` only as a function summary capability, emits no positive source occurrence, marks the dynamic invocation region `unknown_effect`, and the planner emits no pre-dispatch decision. | `TestSemanticSidecarDoesNotAuthorizeDynamicWrapper` |
| `allowed_immutable_read` | One reached logical read produces one physical attempt. | Qualification additionally requires exact fixed arguments, trusted read-only/idempotent `PreDispatchContract`, freshness, grant, privacy, source and budget identities. | `TestCanPreissueRequiresVerifiedExactNecessarilyReachedCallAndFrozenContext`; `TestSemanticPreDispatchPassReusesExistingLegalityDecision` |
| `denied_write_and_unknown_effect` | No speculative action is inferred from syntax. Ordinary execution remains governed by the capability plan. | External writes, missing write-effect coverage, unsupported effect questions and unknown regions fail closed. | `TestAnalyzeRejectsMissingWriteEffectCoverage`; `TestUnsupportedSharedLegalityQuestionsFailClosed`; `TestEffectfulUnknownOrNoncanonicalRegionIsNotReusable` |
| `cancellation_before_claim` | A cancelled ordinary call remains a cancelled call. | Issued physical work is cancelled and remains distinct from a logical claim; the baseline error class is preserved. | `TestSemanticPreDispatchCancelledClaimPreservesBaselineErrorClass`; `TestExecuteSemanticPreDispatchCancelsRunningPhysicalReadOnRunFailure` |
| `ready_unclaimed_late_orphaned` | There is no speculative record in serial execution. | Ready-but-unclaimed work receives an explicit terminal disposition. Cancelled, orphaned, late, timed-out, failed and fallback outcomes are separate schema values and cannot be consumed after terminalisation. | `TestSemanticPreDispatchUnclaimedResultHasTypedTerminalDisposition`; `TestStreamingSemanticPreDispatchFinalizesUnclaimedReadsWithTypedDisposition`; `TestStagedObservationTerminalDispositionPreventsConsume`; `TestTrialRecordAcceptsEveryPhysicalTerminalDisposition` |
| `identity_and_policy_mismatch` | Ordinary final execution remains the fallback authority when its plan permits the call. | Plan, source, arguments, grant, freshness, privacy, lineage and budget mismatch cannot claim a staged result. Rejection is recorded separately from physical work. | `TestSemanticPreDispatchBudgetAndMismatchFailClosed`; `TestStreamingSemanticPreDispatchSameCapabilityMismatchFailsClosed`; `TestCanPreissueRequiresVerifiedExactNecessarilyReachedCallAndFrozenContext` |

## Binding and mutation evidence

The trial contract rejects:

- unknown or duplicate JSON fields;
- non-canonical encodings and trailing documents;
- mutations to every bound source, artifact, manifest, imports, profile, plan, privacy and preregistration identity;
- impossible syntax-error execution/logical-call projections;
- physical disposition totals that do not match physical attempts;
- result digests on failed programs;
- external-write authority dispositions in this read-only study.

The public contract never stores source, result, logs, tracebacks, credentials or workspace bodies.

## Claim boundary

This oracle establishes that the selected adversarial outcomes are representable and directly testable without treating physical preparation as a logical call. It does not establish arbitrary Python observational equivalence, safe external-write speculation, production latency, or an EAGER performance comparison. Physical work that is never consumed remains provider-visible work and cost.
