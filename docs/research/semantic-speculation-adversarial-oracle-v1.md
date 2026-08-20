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

## Exact Guest checkpoint

Phase 1 was rerun against the base-profile CPython 3.14 WASI artifact built at source commit `1c07fc2b9a012abab9071abb777e9ba80f18ee66`:

- artifact SHA-256: `7be7bc7ea15951364427764d36fa6ac40b6f2ed68e71a5a6c639492a2f21df79`;
- manifest SHA-256: `0a0113e0ef7a47d30116c2d3ad1264c8e39772cadb38d81a62ea68c5534633b8`;
- import inventory SHA-256: `165761d1b40630089fc57b1f97033d9dc37290c87d6876ac5f94987b53ae122a`;
- import qualification SHA-256: `f3e8dc64d7c98d8f21d0ddad27ee53b410c3dd2eebd8a1514ad593554116a506`;
- exact-Guest oracle tests: 3 tests passed in 27.024 s;
- focused gate: 14 packages passed;
- race gate: 3 packages passed;
- artifact manifest, dist checksums and supply-chain verification passed.

The body-free private Phase 1 evidence manifest has SHA-256 `d2902898091a95c71da9214e6643673dfbb8d6289a6b1ec95a49bb8f2e675d35`. The first remote attempt failed after the runtime compile because Go still used the quota-limited default home `GOPATH`; recovery preserved the verified private build tree, redirected `GOPATH`, `GOMODCACHE` and `GOCACHE`, reran configure/artifact assembly at the exact source commit, and passed canonical artifact verification. No canonical source file was modified for the recovery.

## Current limit

This phase supplies classifications and semantics gates. It does not yet provide the matched serial/EAGER/Pysolate campaign, performance distributions or a pure-region materialisation result. Those remain later phases.
