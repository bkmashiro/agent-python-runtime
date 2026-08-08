# Known-provider reconciliation fixture

**Status:** Current, bounded local provider-contract fixture.

`runtime/capability/fakemail` exercises the Host-side protocol required by a known provider that supports stable idempotency identity and readback. The provider is in memory and only accepts RFC-reserved `.invalid` recipients, so the test cannot send real mail.

## Contract covered

| Requirement | Current mechanism |
|---|---|
| Intent journal | `transaction.Operation` is proposed before provider dispatch |
| Stable idempotency identity | operation `ManifestDigest` is the provider request identity |
| Attempt/provider identity | `transaction.Attempt` stores ordinal, lease, and provider request digest |
| Host authority | irreversible send requires a consumed user approval bound to operation and manifest |
| Fault injection | explicit response-loss and accepted-timeout faults |
| No blind retry | `dispatching` and `ambiguous` controller states return `ErrSendReconciliation` without calling send again |
| Readback | `lookupSent` reconciles by the original manifest digest |
| Receipt binding | commit and reconciliation recompute the receipt digest and require the returned provider identity to bind to that exact manifest |
| Final evidence | reconciliation stores provider receipt and readback observation as distinct digests; `BuildTransactionEvidence` exports both |
| Persistence gate | SQLite reconciliation persists both digests across ledger reopen |

## Accepted-timeout path

The accepted-timeout fault occurs after the provider has stored the send but before a receipt reaches the controller:

1. the controller records a dispatching attempt before invoking the provider;
2. the provider accepts exactly one send under the manifest idempotency key;
3. `ErrAmbiguousSend` transitions the attempt to `ambiguous`, not `failed`;
4. repeated `Commit` calls return `ErrSendReconciliation` and do not dispatch;
5. `Reconcile` performs readback using the same key;
6. the attempt becomes `succeeded` with `ProviderReceiptDigest` bound to the provider result and `ReconciliationDigest` bound to the readback observation.

The adapter regression is `TestFakeMailAcceptedTimeoutRequiresReadbackAndNeverBlindRetries`. Transaction-level coverage is `TestReconciledIrreversibleEvidenceIncludesReceiptAndObservation`; SQLite reopen coverage is `TestSQLiteLedgerPersistsReconciliationObservationAcrossReopen`.

## Evidence boundary

This fixture proves the transaction and adapter protocol against a controlled known-provider model. It does **not** prove interoperability, timeout semantics, idempotency guarantees, or readback behavior for a production mail API. A production adapter must qualify those properties against the selected provider's versioned API contract before inheriting this claim.
