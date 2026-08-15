# Authority-transparent 20-program campaign v2

**Status:** observed real-Guest campaign; fixture-bounded result

This campaign is a Pysolate-only research fixture. It is not an Agent Harness, production
scheduler, virtual Git implementation or external-effect benchmark. The canonical machine
contract is `workflowbench.CanonicalTransparentCampaign`.

The v2 machine contract separates two concerns:

- stimulus and validation fields describe source, inputs, release, identities, semantic
  oracle and prohibited claims;
- `execution` is a small tagged research-fixture contract describing which existing,
  general Pysolate mechanism the adapter must invoke.

`execution` is not a Runtime API or production scheduling policy. No package under
`runtime/` knows `P01`–`P20`, campaign families, paper labels or expected outcomes. The
adapter may dispatch on typed execution kind and transition, but must never dispatch on a
program ID, family or `expected` field. The driver enforces this boundary structurally:
`CampaignAdapter` receives `CampaignRequest`, which does not contain those fields; rejected
dispositions also come from Runtime admission rather than being copied from `Expected`.
Canonical capability Plans are materialized by identity in this research package and passed
as ordinary Host inputs to the existing capability/subagent APIs. Workflow start/resume
contracts use an opaque fixture state key; the adapter does not need to recognize `P13` or
any other case number.

## Fixed execution policy

- exactly 20 logical Python programs;
- release offsets are manifest inputs, not measured events;
- three physical slots;
- FIFO admission among ready programs, stable by manifest order;
- no implicit preemption;
- baseline and qualified treatment use the same source, inputs, roots and semantic oracles;
- actual admission, queue, start, wait, resume and completion times must be measured from a
  monotonic clock;
- the paper walkthrough set is fixed to `P02`, `P06`, `P11`, `P15`, `P18`; all valid runs
  remain in aggregate results.

## Corpus table

| IDs | Mechanism under test | Required observable result | This cannot prove |
|---|---|---|---|
| `P01`–`P04` | one authority-free producer feeding separately admitted consumers | one producer identity; independent child Plans, Runs and workspace attempts; cancelled consumer remains attributable | shared mutable Guest safety, cross-privacy sharing, speedup |
| `P05`–`P09` | exact identity sharing and one-field near-match rejection | `P05/P06` share; source (`P07`), input (`P08`) and privacy (`P09`) changes do not | semantic equivalence, arbitrary Python purity, speedup |
| `P10`–`P12` | exact-root verification | byte-identical selected roots with the same verifier contract may share; byte-distinct root does not | semantic workspace equivalence, merge/rebase correctness, speedup |
| `P13`–`P16` | authority-bound fresh resume | same authority resumes fresh; freshness/Plan/grant changes revalidate observations; expired authority starts no Guest | Guest continuation restore, general multi-wait scheduling, speedup |
| `P17`–`P20` | child attenuation and conservative reservation | valid child starts; widening, aggregate overcommit and late terminal child fail before Guest start | semantic policy subsumption, external-effect safety, speedup |

## Exact-sharing near-match contract

`P05` and `P06` have equal source, canonical inputs, Plan, workspace fixture, privacy and
policy identities. The following rows change exactly one qualification dimension relative
to that pair:

- `P07`: source only;
- `P08`: canonical inputs only;
- `P09`: privacy partition only.

Those are rejection controls, not approximate-sharing candidates.

## Workspace wording

`workspace_fixture_sha256` identifies the canonical initial fixture declaration. Campaign
execution must separately record the actual immutable Pysolate workspace root returned by
the workspace Manager. A fixture digest is not a Git commit and must not be presented as
virtual Git.

## Candidate claims

If validated real-Guest evidence supports them, the campaign may claim:

1. exact qualified logical requests can map to fewer physical executions while preserving
   separately attributable logical outcomes;
2. qualified physical producer output can feed consumers whose authority, fresh Runs and
   private workspace attempts remain separate;
3. fresh workflow resume can retain authority-free compute while revalidating observations
   affected by a current Host authority change;
4. every admission, rejection, sharing relation and terminal disposition can be reconstructed
   from sealed evidence rather than inferred from the Lab layout.

It must not claim generalized speedup, semantic equivalence, arbitrary-Python purity,
provider-level Agent behavior, virtual Git, or external-effect safety.

## Observed campaign result

The frozen real-Guest campaign at campaign source
`40882ca5a818f4c5388bdeebe7d36ee9dc5fe7c5` completed five balanced paired
repetitions on the recorded darwin/arm64 host. Every registered oracle and authority
rejection validated. Baseline used 19 physical executions in every repetition; the
qualified treatment used 17. Median wall time was 25.73 s versus 22.00 s. These are
fixed-campaign observations, not a general throughput claim.

The credential-free public projection, compact case table, identity table and measured
figures are generated in
[`authority-transparent-campaign-results.md`](authority-transparent-campaign-results.md).
The canonical hash-bound evidence remains local and private.
