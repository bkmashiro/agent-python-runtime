# Workload evaluation v2.1 expanded cohort

Status: **Observed bounded local mechanism evidence; activation gate not met.** Five workspace-free workloads ran under `direct_broker` and `pysolate_guest` on signed source snapshot `0e1383e8fd0d6d4c981c6f3fdb97c1eb9d92606b`. This is an experimental partial result, not a product, placement, performance, model-quality or economic claim.

## Why this expansion exists

The original two-workload pilot established that Direct and Guest conditions could share typed Host authority, exact source fixtures and condition-neutral task oracles. It was too small to show whether the boundary metric mechanically favoured Guest or whether local transform complexity mattered.

The v2.1 cohort preserves the published v2 pilot corpus and decoder. It adds a separate versioned corpus, plan, study and summary shape rather than changing the old identities.

## Frozen cohort

| Workload | Source calls | Transform shape | Role |
|---|---:|---|---|
| `catalog-top-direct` | 1 | select top catalog item | negative control |
| `manifest-suite-direct` | 1 | summarize suite/case/artifact/metric counts | negative control |
| `catalog-threshold-loop` | 1 | filter, stable rank and aggregate | single-source multi-step |
| `manifest-matrix` | 1 | nested flatten and stable normalization | single-source multi-step |
| `source-join-ranking` | 2 | cross-source selection and join | multi-source pilot |

Both conditions use the same closed capability order, source-fixture digests, capability-plan identity, expected result digest and call count for each workload. Direct issues every source request through `capability.Broker.Call`; Guest issues the same typed calls from one fresh CPython/WASI Run.

## Observed result

The exact signed-HEAD study completed all ten offered rows:

| Measure | Observed |
|---|---:|
| Offered / completed | 10 / 10 |
| Failed / timed out | 0 / 0 |
| Task oracle passed | 10 / 10 |
| Evidence complete | 10 / 10 |
| Direct controller boundaries | 6 |
| Guest controller boundaries | 5 |
| Direct typed capability calls | 6 |
| Guest typed capability calls | 6 |

Per-workload interpretation under the frozen counting rule:

- all four one-source workloads are `1 Direct : 1 Guest` controller boundary;
- only `source-join-ranking` is `2 Direct : 1 Guest`;
- local filtering, looping, aggregation or nested normalization alone does not reduce this boundary count when the Direct scripted evaluator already performs that transform locally;
- the metric does not mechanically award Guest a lower count for every workload.

Portable identities:

- corpus: `sha256:c7ea2125341b5189b606f078599b7de9856a8ac6a12e3b0d8370366780932fda`
- plan: `sha256:32607f37810c6740e6dad9754e6499913fa4a4a5ec6cb92f13dbdffbfe79bcd6`
- study: `sha256:3f15fc04fb8488392e17e1d1bfcf36c156e00fa96acf24e3208232c258dcabf0`
- summary: `sha256:1afb2aeb1faee021babd09df1fe2957f2c138ae31d9819b79dc7b2e0dbb1e96f`

## Decision

The documented real-model activation gate requires at least two multi-step workloads to reduce Host orchestration boundaries while negative controls remain interpretable. This cohort has one such reduction, not two. The gate is therefore **not met**.

Do not add an artificial second two-source workload merely to reproduce the counting rule's guaranteed `2:1` shape. Stop the workspace-free scripted expansion here. Do not start a paid model experiment and do not claim a general placement advantage.

A future slice requires a new decision question rather than more near-duplicate tasks. The admissible options are:

1. design a bounded research-only Direct state adapter and ask whether stateful workspace interactions create a non-tautological placement contrast; or
2. stop evaluation v2 at the current mechanism evidence.

Neither option is activated by this result.

## Evidence and validation

- Published v2 pilot corpus identity remains unchanged and old v2 schema tests continue to pass.
- v2.1 strict contracts reject schema mixing, row/order drift, source fixture drift, capability-plan mismatch, duplicate JSON keys, trailing data and summary count wraparound.
- Private output contains only canonical corpus, plan, body-free study, summary and identities with `0700` directory and `0600` file modes.
- An independent helper decoded all four documents, checked cross-document bindings and rebuilt the summary byte-for-byte.
- Full Go tests, race tests, vet, command builds, Python tests and compile checks passed; the real Guest artifact verifier passed.
- Codex independently predicted the same gate outcome from the frozen counting rule. A bounded implementation review returned PASS; a reproducible count-overflow issue visible in its transcript was then fixed and covered by regression testing before this signed study.

## Boundaries

This result does not measure or establish:

- workspace or Capsule behavior;
- model/controller quality;
- token use, wall-clock advantage or monetary cost;
- arbitrary determinism, security/isolation or production readiness;
- placement share or computer replacement;
- Windows runtime evidence.

It changes no Lab v1 wire contract, Runtime authority surface, capability catalog or storage backend.
