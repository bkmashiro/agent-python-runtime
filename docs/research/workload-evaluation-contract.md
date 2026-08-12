# Workload evaluation contract v1

Status: **Current contract; execution corpus not yet qualified.**

This document freezes the first mechanism-only evaluation vocabulary and workload cohort. It does not report workload results; those become Current only after the real CPython/WASI runner and task oracles pass on exact identities.

## Falsifiable claim

For this bounded cohort, Pysolate should be able to reproduce captured executions, run Host-selected counterfactuals at typed capability-operation boundaries, and explain result/workspace differences from bounded evidence while preserving fresh-per-Run and Host-owned authority.

A successful first report may support only:

- real Guest control-flow carriage;
- strict offline playback equivalence for the named workload and identities;
- immutable branch lineage and observed divergence;
- evidence completeness/incompleteness as actually recorded;
- content-addressed evidence reuse and storage accounting;
- exact task-oracle outcomes.

Every plan and report must prohibit these claims:

- `arbitrary_determinism`
- `computer_replacement`
- `economic_advantage`
- `model_quality`
- `placement_share`
- `production_readiness`
- `token_or_latency_benefit`

The validator requires this complete, sorted list. It cannot be removed or weakened by an experiment author.

## Evidence vocabulary

| Value | Meaning |
|---|---|
| `current` | Implemented and supported by the named release contract. |
| `mechanism_only` | Evidence exercises runtime mechanisms without measuring model quality or product economics. |
| `qualified_workload` | A claim is limited to an exact corpus row, identities and oracle. |
| `experimental_partial` | A bounded mechanism has explicit unsupported classes, such as deterministic verification. |
| `not_measured` | The study collected no evidence for the dimension. Absence is not zero or success. |

The v1 corpus, plan and report all use `mechanism_only`. Later analysis may label an individual conclusion `qualified_workload`; it must not rewrite the source report's evidence class.

## Reuse inventory and gaps

Track A introduces contracts, not parallel execution machinery:

| Existing asset | Reused boundary | Remaining evaluation gap |
|---|---|---|
| `pysolate.playback-bundle.v1` | Exact live capture, strict ordered offline transcript and outcome/workspace binding | Expand a corpus plan into repeatable rows and bind every row to the same corpus/plan identity |
| `pysolate.runtime-observation.v1` | Bounded lifecycle, capability-call digests and workspace metadata | Project completeness and event references into report/Lab rows without bodies |
| `pysolate.branch-bundle.v1` | Immutable parent/child lineage and typed capability-operation fork | Define workload-specific suffix changes and exact child task oracles |
| `pysolate.deterministic-verification.v1` | Fresh-Guest clock/random controls and explicit admission denials | Qualify only the workspace-free bounded-planning treatment with fixed captured inputs |
| `sources.demo_catalog` and `sources.benchmark_manifest` | Two sealed Host-selected external-read adapters | Freeze canonical workload source fixtures and expected outputs |
| `pysolate.labstore-object.v1` and typed relations | Private immutable evidence, content dedup and graph links | Ingest evaluation rows and produce a stable body-free Lab read projection |
| `pysolate-research` CLI | Existing inspect/compare/DAG/store operator surfaces | Add bounded evaluation ingestion/export after Lab v1 freezes |

Still absent after Track A—and therefore not claimed Current—are real workload code and seeds, the row expansion/execution runner, measured reports, Lab v1 schemas/producers, and Web integration with canonical Runtime fixtures.

## Versioned contracts

The standard-library-only `research/evaluation` package defines strict canonical JSON:

- `pysolate.workload-corpus.v1`
- `pysolate.evaluation-plan.v1`
- `pysolate.evaluation-report.v1`

All decoders reject unknown fields, trailing JSON and non-canonical encoding. Identities are SHA-256 of exact canonical bytes. Bodies are not included: code, inputs and workspace seeds are represented by exact identities. A contract document is capped at 16 MiB; v1 plans/reports cap rows at 100,000, each row caps evidence references at 128, and each workload caps declared capabilities at 32. Normal studies are expected to be much smaller; these are parser safety ceilings, not target sizes.

### Corpus

Each workload binds:

- stable ID and integer version;
- one closed workload family;
- code, canonical input and optional workspace-seed identities;
- explicit typed capability requirements (`name`, `effect_class=external_read`, `playback=captured`), including an explicit empty array;
- applicable treatments;
- one exact result or result-plus-workspace oracle;
- expected capability-call count.

Branch treatment requires at least one captured external-read capability. The three families also have fixed environment invariants: structured-source and bounded-planning workloads have no workspace seed and require captured external-read capabilities; stateful-local workloads require an exact workspace seed, have no external capabilities, and cannot declare branch or deterministic-verification treatment. Omission of a seed or weakening of a capability declaration therefore cannot change admission semantics.

### Plan

The plan binds:

- exact Host commit;
- Guest artifact and manifest identities;
- corpus and Runtime-profile identities;
- ordered treatment set and bounded repetitions;
- maximum rows, per-row wall budget and evidence-byte budget;
- the fixed prohibited-claims list.

Treatment order is declared execution order, not a performance ranking.

### Report

Each expanded row has a domain-separated deterministic identity derived from workload ID, treatment and repetition. Report validation enforces:

```text
offered = completed + failed + timed_out + unsupported
```

Task status/oracle status and evidence completeness are independent dimensions. In particular, an oracle may fail while the evidence remains complete. Completed rows require a passing oracle and complete referenced evidence; timeout and unsupported rows cannot claim complete evidence.

Standalone report validation proves canonical structure, internal identities and count conservation; it does not prove that the report covers every row implied by a particular corpus and plan. Track D owns deterministic plan expansion and the cross-contract check that every expected `(workload, treatment, repetition)` row appears exactly once and no extra row appears. Keeping that logic out of the wire decoder prevents the contract package from becoming an execution scheduler.

## Workload cohort

### 1. `structured-source-v1`

Family: `structured_source_synthesis`

The Guest reads both sealed curated sources, normalizes and ranks records, returns a canonical summary and writes a deterministic workspace report.

Treatments:

- `live_capture`
- `offline_replay`
- `counterfactual_branch`

Oracle:

- exact result identity;
- exact workspace identity;
- exactly two capability operations.

The branch changes one Host-authored captured source result at a typed capability boundary. It does not modify the Agent request, grant or URL policy.

### 2. `stateful-local-v1`

Family: `stateful_local_analysis`

The Guest starts from a sealed workspace seed, performs a multi-step local transformation, and emits summary/index files.

Treatments:

- `live_capture`
- `offline_replay`

Here `live_capture` means the ordinary execution/capture baseline; this workload has zero external source calls. Offline playback still verifies the fresh offline path and identical result/workspace outcome.

Oracle:

- exact result identity;
- exact final workspace identity;
- zero capability operations.

Counterfactual branch is unsupported because v1 branches only at typed capability-operation boundaries. Deterministic verification is unsupported because mounted WASI workspaces are denied.

### 3. `bounded-planning-v1`

Family: `bounded_planning_search`

The Guest evaluates a bounded candidate set obtained through one sealed curated source and returns the selected candidate plus a digest-bound score summary.

Treatments:

- `live_capture`
- `offline_replay`
- `counterfactual_branch`
- `deterministic_verification`

Oracle:

- exact result identity;
- exactly one capability operation.

The branch substitutes a sealed candidate-set result at that capability boundary. Deterministic verification repeats only fixed captured/overridden capability inputs with no mounted workspace. It does not claim that the live external read is deterministic.

## Treatment applicability

| Workload | Live/capture | Offline | Branch | Deterministic verification |
|---|---:|---:|---:|---:|
| structured source | yes | yes | yes | not in v1 |
| stateful local | yes, zero source calls | yes | unsupported: no capability boundary | unsupported: mounted workspace |
| bounded planning | yes | yes | yes | yes, fixed captured input only |

Unsupported rows must be emitted explicitly with `status=unsupported`, `oracle_status=not_run`, `evidence_complete=false`, and a stable reason code. They must not disappear from row conservation.

## Canonical and adversarial fixtures

`research/evaluation/testdata/` contains producer-generated canonical corpus, plan and report fixtures. `testdata/invalid/` covers:

- unknown field;
- trailing JSON;
- duplicate workload ID;
- missing oracle;
- incompatible deterministic treatment;
- report row identity drift.

Tests regenerate the producer output in memory and require exact byte equality with the checked-in files. Invalid fixtures must remain rejected by their strict decoder.

## Boundaries retained

This contract adds no Runtime authority and is not imported by Runtime core. It does not add shell, generic HTTP, external writes, credentials, package installation, persistent Guest state or memory snapshots. It describes Host-side research plans and results only.

The next milestone is Lab v1 read schema. That schema will project bounded views from these contracts and existing evidence; it will not become the LabStore disk format or an authorization mechanism.
