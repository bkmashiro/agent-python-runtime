# Workload evaluation v2 pilot

Status: **Observed bounded local mechanism evidence.** Two workspace-free pilot workloads ran under `direct_broker` and `pysolate_guest` on the signed source snapshot `b2c64f0a73cdd3ee11952c428984adac4c812996`. This is not the full evaluation v2 cohort and is not a product, placement, performance or economic claim.

## Question

For the two qualified pilots, can local control flow move into one Pysolate Guest boundary while preserving the same typed Host authority, source fixtures, capability results and task oracle used by a Direct Broker baseline?

## Conditions

- `direct_broker`: the Host evaluator issues every canonical capability envelope through `capability.Broker.Call` and performs the local transform.
- `pysolate_guest`: one Runtime request executes the transform in a fresh CPython/WASI Guest; Guest tool calls cross the same Broker implementation.

Both conditions use the same capability definitions, grants, loopback-only captured sources and expected canonical result. Each Direct/Guest pair must carry the same capability-plan identity. Direct handler invocation is not admitted.

## Frozen pilots

| Workload | Shape | Required calls | Purpose |
|---|---|---:|---|
| `catalog-top-direct` | one source read plus top-item selection | 1 | Direct-favoured negative control |
| `source-join-ranking` | two source reads plus local join, filter and ranking | 2 | multi-source Guest-control-flow pilot |

The corpus binds capability order, canonical source-fixture digests, expected-result digest and expected call count. The plan binds corpus, Host commit, Guest artifact, artifact manifest, Runtime profile and fixed ceilings.

## Observed result

A real Guest study on the signed source snapshot completed all four offered rows:

| Measure | Observed |
|---|---:|
| Offered | 4 |
| Completed | 4 |
| Failed / timed out | 0 / 0 |
| Task oracle passed | 4 / 4 |
| Direct controller boundaries | 3 |
| Guest controller boundaries | 2 |
| Direct typed capability calls | 3 |
| Guest typed capability calls | 3 |

The boundary result is exactly the frozen mechanism shape: Direct exposes one controller boundary per Broker call; Guest exposes one Runtime request per workload while preserving the same total typed capability calls. It does **not** establish lower latency, lower token use, lower cost, better model quality or a general placement advantage.

Portable identities:

- corpus: `sha256:3151488fe73d188abbf958a33c74c60f928bb858968c77ab2475f924a7b68060`
- plan: `sha256:4616e4d0a2954349886160c352cdc8ed3a43b5c05b774d4b5c1057bc519b712f`
- study: `sha256:8c9146077d0edb3c2ca4167b4bd4d4c3d92d850f183a115b775807fc58ad4b74`
- summary: `sha256:b134488236a0affb0dde785aa797fc048e36248c723cf5ac092d881bf233f166`

## Evidence and validation

The implementation uses a separate closed v2 contract rather than extending evaluation v1 wire documents. Strict decoders reject unknown fields, duplicate keys, malformed/trailing JSON, identity mismatch, row/order drift, condition drift, source-fixture drift, incomplete receipts/transcripts and mismatched capability-plan identities.

The private study output contains only canonical corpus, plan, body-free rows, summary and identities. It retained `0700` directory and `0600` file modes. An independent helper decoded the corpus, plan, study and summary, revalidated cross-document bindings and rebuilt the summary byte-for-byte.

All repository Go tests, race tests, vet, command builds, Python tests and compile checks passed. The real Guest artifact verifier passed before execution. A narrow independent post-fix review returned PASS with no blockers.

## Boundaries

This pilot does not admit:

- workspace or Capsule comparisons;
- a research-only Direct state adapter;
- public network, credentials, external writes, shell or subprocess authority;
- real-model trials or paid experiments;
- token, latency, cost, model-quality or placement-share conclusions;
- Lab v1 changes, SQLite metadata or production-readiness claims.

## Next decision

The follow-on five-workload workspace-free cohort is complete. See
[workload-evaluation-v2-expanded.md](workload-evaluation-v2-expanded.md). It did
not meet the frozen real-model activation gate, so no paid phase or workspace
adapter is activated by these pilot results.