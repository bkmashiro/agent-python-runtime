# Runtime and research identity model

Status: identity inventory for the **Current** Runtime contracts and explicitly
marked **Experimental** research contracts. This document classifies
identities; it does not make every digest an authority credential or a
semantic-correctness proof.

## Rules

- A digest proves equality of canonical bytes, not correctness, safety, authorship or meaning.
- Agent Python neither supplies nor needs identity bookkeeping. Host code authors execution, authority, artifact, playback and workspace identities. A future Lab may display semantic comparisons without injecting raw digests into Agent context.
- A Playback Bundle self-identity detects corruption and supports content addressing. Authentication comes from the separately supplied Host-protected expected Bundle identity.
- A field-level digest remains justified when it supports independent admission, incremental validation, lookup/deduplication, privacy-preserving comparison or tamper localization. Otherwise prefer a verified domain root and references.
- Content-addressed research storage must store one immutable typed body once and refer to it; it must not duplicate a body merely because several relations retain its digest.

## Identity inventory

| Identity | Producer and canonical bytes | Consumer / trust owner | Class and purpose | Derivable / visibility |
|---|---|---|---|---|
| `ExecutionRef.executed_code_sha256` | Runtime Host hashes exact `RunRequest.Code` bytes after decoding | Harness/Lab correlation and response validation; Host-owned | Content: distinguishes model segment from exact executed code | Recomputable from protected code; Host response visible, not Guest-authored |
| artifact SHA-256 | CLI/profile verifier hashes exact Guest WASM bytes | Pre-Guest artifact admission and Playback binding; Host-owned | Artifact/config | Recomputable only with artifact bytes; Host/operator visible |
| manifest/profile/import inventory identities | verifier hashes canonical distribution/profile documents | pre-Runner compatibility admission | Artifact/config | Recomputable from protected distribution files; Host/operator visible |
| `CapabilityGrant` policy identity | Host canonicalizes domain-separated private grant policy | Registry sealing, Playback admission | Authority: exact per-Run target and budget policy without exposing bytes | Plan includes the identity, not policy body; opaque to Agent |
| capability Plan identity | Host hashes canonical `pysolate.capability-plan.v7` containing sorted Specs, Grant bindings and max calls | Broker, receipts, response projection, Playback admission | Authority root | Recomputable from the full sealed Plan; Host response may expose it as evidence |
| Broker run / execution ID | Host invocation reference or generated physical-run identifier | receipts and execution correlation | Evidence/relation, not a content hash or authority | Not derivable; Host/Harness/Lab visible; Guest cannot claim it |
| capability call request digest | Broker hashes canonical capability-call request relation | duplicate-call detection, receipts and Playback matching | Evidence/relation and tamper localization | Recomputable from canonical call; compact receipt visible |
| capability call response digest | Broker hashes canonical validated response bytes/relation | receipts and response attribution | Evidence/relation and privacy-preserving equality | Recomputable from protected result; compact receipt visible |
| transcript `arguments_sha256` | capture hashes canonical validated arguments bytes | Bundle validation and operation-local Playback matching | Content/evidence: incremental validation and localization | Duplicates information committed by Bundle root but has independent per-entry matching value |
| transcript `result_sha256` | capture hashes canonical schema-validated result bytes | decode validation, offline return validation and per-entry comparison | Content/evidence: protected body identity and operation-local validation | Bundle-root derivable only after loading whole Bundle; retained for bounded local validation |
| transport `body_sha256` | live source adapter hashes accepted raw HTTP response body | protected transport attribution | Evidence: distinguishes raw wire body from normalized capability result | Not generally derivable from normalized result; Host/Bundle only |
| Host request SHA-256 | Host hashes canonical admitted `RunRequest` | workspace receipt and Playback admission | Artifact/config relation: same source, inputs, schema and requirements | Recomputable from protected request; Bundle stores only digest |
| expected Agent-result SHA-256 | capture hashes canonical final Agent result | final Playback outcome check | Evidence/relation and privacy-preserving equality | Recomputable from result body, which Bundle deliberately omits |
| per-file SHA-256 | workspace Capsule builder streams exact ordinary-file bytes | import validation, future content dedup and tree construction | Content | Recomputable from file bytes; portable manifest visible |
| workspace tree SHA-256 | workspace builder hashes canonical ordered paths, kinds, executable bits, sizes and file digests | workspace comparison and Capsule validation | Content/structure | Recomputable from complete manifest entries |
| workspace SHA-256 | workspace builder hashes domain document containing tree, counts, bytes and limits | Run initial/final binding and Playback admission/outcome | Artifact/config state identity | Deliberately differs for equal trees under different Host limits |
| Capsule byte SHA-256 | publishing CLI streams exact complete Capsule framing, manifest and payload bytes | publication receipt and exact artifact validation | Artifact/content | Different Capsule encodings may represent equivalent tree only if format evolves; current v1 canonicalizes |
| Playback Bundle identity | encoder hashes canonical Bundle payload excluding its identity field | decode integrity, CAS naming and expected-identity comparison | Content root / evidence relation | Self-hash alone is not authentication |
| trusted `expected_bundle_sha256` | capture operator records the issued Bundle identity outside the mutable Bundle | pre-Guest offline admission | Protected external trust anchor | Not derived from the candidate during admission; Host config only |
| execution-profile SHA-256 in Bundle | CLI hashes the Host-selected execution-profile binding document | pre-Guest Playback compatibility | Artifact/config | Recomputable from selected profile context |
| deterministic-verification profile identity (**Experimental/Partial**) | Host hashes the canonical `pysolate.deterministic-verification.v1` descriptor, including exact artifact, random-seed digest and declared clock/denial policy | artifact admission, execution-profile binding, observation and qualified-repeat comparison | Artifact/config | Recomputable only with the Host seed and descriptor; never Agent-selected |
| observation `execution_id` plus sequence/parent (**Current**) | Host context supplies the physical execution identity; Session assigns accepted one-based sequence and earlier causal parent | Recorder/future Lab correlation | Evidence/relation, not content identity or authority | Joined to Host `ExecutionRef`; events have canonical bytes but no event self-hash in v1 |
| branch prefix SHA-256 (**Experimental**) | Host hashes the exact parent identity, fork operation and canonical parent entries before the fork | branch-manifest parent validation and mixed Broker admission | Evidence/relation | Recomputable only from the protected parent Bundle and fork; stale/reordered prefixes fail |
| branch-manifest identity (**Experimental**) | Host hashes the canonical v1 parent/fork/prefix/request/artifact/profile/workspace/child-Plan/Grant/suffix document | pre-Guest branch admission and lineage display | Content root / evidence relation | Self-hash is not authentication; Host config separately anchors the expected identity |
| child Playback Bundle identity (**Experimental relation**) | ordinary Bundle v1 encoder hashes the child transcript/outcome relation | child playback and parent/manifest/child lineage outside Bundle v1 | Content root | Does not embed its parent; provenance requires the protected manifest/outcome relation |
| LabStore typed content reference (**Experimental**) | local Lab hashes domain, exact semantic kind, sorted links and body bytes | deduplication, validated reads, workspace/branch graphs and retention | Domain-separated content identity | Equal bytes under different kinds intentionally differ; storage does not authorize execution |
| LabStore privacy and named roots (**Experimental**) | local operator writes protected mutable policy/index records | portable export and reachability retention | Mutable policy, deliberately not content identity | May change without changing object identity; missing privacy fails to private |

## Deliberate overlaps

The following apparent repetitions protect different boundaries and are not removed in Bundle v1:

- Bundle identity versus `expected_bundle_sha256`: self-consistency versus independently trusted admission.
- Bundle root versus per-operation argument/result digests: whole-artifact integrity versus incremental Broker matching and operation-local diagnostics.
- normalized result digest versus transport body digest: semantic result identity versus accepted raw-source attribution.
- file, tree, workspace and Capsule digests: immutable bytes, path structure, structure plus policy limits, and exact serialized artifact.
- Plan identity versus Grant identities: one sealed authority root plus opaque per-capability policy bindings needed for admission and inspection.

Removing any encoded v1 field would change canonical Bundle or Capsule identity and therefore requires an explicit versioned compatibility decision plus RED tamper/admission tests. This baseline found no field whose removal both preserves compatibility and has no independent admission, lookup, privacy-comparison or tamper-localization value.

## Lab storage rule

The Experimental local LabStore applies the separation below; a future Lab
service must preserve it:

```text
typed immutable body -> one domain-separated content object
event/run/operation/workspace/branch -> bounded relation referencing that object
authority admission -> separate Host-owned Plan/Grant/trusted-root relation
```

This reduces stored bytes without pretending relation identities are redundant. Indexes, retention metadata, display labels and access-control state are mutable metadata and do not belong to immutable content identity.
