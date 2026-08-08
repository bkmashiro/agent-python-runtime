# Cross-plane evidence bundle

`verification/evidencebundle` is a Host-side, read-only graph over evidence that already exists in the Runtime monorepo. It does not create new execution authority, authenticate an evidence source, or upgrade any source's replay qualification.

## Version and scope

The current schema is `evidence-bundle/v1`.

A bundle can connect these independently checked facts:

- a Host-owned Agent run;
- one exact `runtime.ExecutionRef`;
- the `claim-manifest/v2` reconstructed from the same integrity-checked `agenttrace.Playback`;
- an optional reconciled transaction evidence export bound to the same Host `ExecutionID`;
- an optional checkpoint event that is a recorded parent-chain ancestor of the manifest's unique successful completion event.

The transaction edge is execution-scoped: `TransactionEvidence.Transaction.RunID == ExecutionRef.ExecutionID`, matching the Host-owned capability/transaction provenance boundary. The reconciled operation's `ProviderRequestDigest` must equal that operation's `ManifestDigest`; this is an identity binding, not provider authentication.

## Build and verification

`Build(Sources)` validates source artifacts before emitting graph nodes and edges:

1. reject playback above 4,096 events or 8 MiB aggregate event bytes before revalidation;
2. validate the Claim Manifest;
3. reconstruct it from the supplied playback and require exact equality;
4. bind the manifest digest to its exact execution and the execution to its Agent run;
5. when transaction evidence is supplied, verify its consistency digest and semantic topology, require its Host identity to equal the exact `ExecutionID`, and require one applied irreversible operation with a kind-valid successful attempt, a manifest-bound unexpired approval consumed no later than dispatch creation, a recorded ambiguous → reconciled attempt path, causal attempt → operation → terminal-transaction ordering in which operation activation requires the latest relevant attempt to remain dispatching, committed/all-operations-applied consistency, a provider request equal to the operation manifest, and valid distinct provider-receipt/reconciliation-observation digests;
6. when a checkpoint is supplied, verify playback integrity and require the checkpoint event to be a recorded ancestor of the manifest's completed event.

`Verify(Bundle, Sources, Profile)` is read-only. Every present node and edge must be exactly supported by the supplied sources. Unknown kinds, duplicate nodes or edges, unsupported endpoints, cross-execution transaction binding, invalid attempt preconditions, missing/expired/mismatched/late-consumed approval, unrecorded reconciliation, causal reordering, provider-request/operation-manifest mismatch, unrelated checkpoints, and aliased receipt/readback evidence fail closed.

Removing a required but otherwise valid edge produces `insufficient`, not a fabricated positive result.

## Profiles

- `structural-execution/v1` requires the exact Claim Manifest → execution and execution → Agent-run bindings.
- `current-cross-plane/v1` additionally requires reconciled transaction → exact execution evidence and checkpoint → Agent-run/recorded-execution lineage.
- `full-outcome/v1` additionally requires a task-specific final-state oracle. That source and edge do not exist in this slice, so this profile intentionally remains `insufficient` with `final-state-oracle` reported as missing.

`Report.Status == verified` means only that the graph requirements for the selected profile are present and source-backed. It is not a Claim Manifest status, a signature, a MAC, provider authentication, production-provider qualification, or proof of semantic task success. Callers must obtain playback and transaction evidence from a trusted Host source; self-consistent SHA-256 snapshots do not authenticate provenance.

Checkpoint ancestry is likewise a metadata assertion over integrity-checked parent IDs and a state fingerprint. It does not verify checkpoint bytes, successful restore, semantic state continuity, or a final-state oracle.

## Replay boundary

`replayfixture` remains a separate controlled local fixture. Its R1/R2 reports are not attached to arbitrary Runtime executions because the fixture schema has no exact `ExecutionRef` binding. This bundle therefore does not infer a final-state oracle or outcome edge from fixture replay.

## Dependency boundary

The package lives under `verification/` and may consume Host- and Runtime-owned public data types. Runtime core must not import `verification/evidencebundle`, `claimmanifest`, `agenttrace`, or the replay/recorder verification packages.
