# Workload candidate audit for evaluation v2

Status: **Proposed, read-only repository audit.** This document selects a candidate cohort and the evidence needed before implementation. It is not an evaluation result, product claim, or implementation roadmap.

## Decision question

The next bounded question is:

> For a qualified local workload cohort, does moving multi-step control flow into one Pysolate Guest reduce Host orchestration boundaries while preserving the same task oracle, typed Host authority, and final-state oracle as a scripted direct baseline?

The first experiment remains mechanism-only. It may compare exact call counts, boundary crossings, serialized bytes, workspace transitions, lifecycle outcomes, and oracle results. It must not claim model quality, token or latency benefit, economic advantage, placement share, computer replacement, or production readiness.

A later real-model experiment is separately activated only if this mechanism baseline finds a meaningful, correctly attributed difference.

## Current evidence inventory

The repository currently supplies:

- three canonical v1 workloads: one two-source transform, one workspace transform, and one one-source bounded selection;
- two credential-free typed `external_read + captured` capabilities: `sources.demo_catalog` and `sources.benchmark_manifest`;
- exact schemas, Host-owned target policy, sealed plans, strict playback, and body-free transport evidence for both sources;
- fresh CPython/WASI Guest execution, bounded workspace files, deterministic Capsules, and final workspace identities;
- task-specific result/workspace oracles and a real-Guest evaluation runner;
- no credential-bearing source, external write capability, generic HTTP, shell, subprocess, socket, or ambient Host filesystem authority.

The v1 corpus is intentionally closed around exactly three IDs, fixed family mappings, at most two capability calls, and four treatment dispositions. Evaluation v2 therefore needs a versioned corpus/plan contract rather than silently widening v1.

## Selection criteria

Candidates are scored from 1 (weak) to 5 (strong).

- **Thesis value**: separates direct orchestration from Guest-local control flow.
- **Oracle strength**: exact result and final-state correctness can be fixed in advance.
- **Reuse**: uses implemented Runtime mechanisms without a new authority path.
- **Boundary contrast**: produces a meaningful difference in Host/Guest crossings.
- **Risk**: 1 is low authority/implementation risk; 5 is high.

Priority favors high thesis value, oracle strength, reuse and contrast with low risk. Scores select candidates; they are not evaluation evidence.

## Candidate matrix

| ID | Shape | Reused mechanisms | Thesis | Oracle | Reuse | Contrast | Risk | Decision |
|---|---|---|---:|---:|---:|---:|---:|---|
| `catalog-top-direct` | Direct-favored single lookup and top-item extraction | demo catalog | 3 | 5 | 5 | 2 | 1 | Include as negative control |
| `manifest-suite-direct` | Direct-favored single manifest lookup and suite summary | benchmark manifest | 3 | 5 | 5 | 2 | 1 | Include as negative control |
| `catalog-threshold-loop` | Filter, stable sort and aggregate 32–256 catalog items | demo catalog | 5 | 5 | 5 | 4 | 1 | Include |
| `catalog-score-report` | Derive several aggregates and materialize canonical report | demo catalog + workspace | 5 | 5 | 5 | 4 | 1 | Include |
| `manifest-matrix` | Expand cases, artifacts and metrics into a normalized experiment matrix | benchmark manifest | 5 | 5 | 5 | 4 | 1 | Include |
| `manifest-policy-check` | Evaluate several bounded manifest invariants and emit violations | benchmark manifest | 4 | 5 | 5 | 4 | 1 | Include |
| `source-join-ranking` | Join catalog entries with manifest cases and rank a derived plan | both sources | 5 | 5 | 5 | 5 | 1 | Include |
| `source-join-workspace` | Join both sources, write canonical plan and summary files | both sources + workspace | 5 | 5 | 5 | 5 | 2 | Include |
| `seeded-reconciliation` | Reconcile seeded CSV/JSON state with one captured source | demo catalog + seeded workspace | 5 | 5 | 5 | 5 | 2 | Include |
| `capsule-two-run-rollup` | First run writes normalized state; fresh second run consumes its Capsule | workspace + Capsule lifecycle | 5 | 5 | 4 | 5 | 3 | Include as lifecycle boundary |
| `branch-sensitive-ranking` | Counterfactual captured source changes selected plan and report | demo catalog + branch | 3 | 5 | 5 | 3 | 2 | Reserve: validates branching, not placement |
| `deterministic-pure-reducer` | Pure input reduction under qualified deterministic profile | inputs only | 2 | 5 | 5 | 1 | 1 | Reserve: verifier control, weak placement evidence |
| `credentialed-provider-read` | Read a real account/provider | new credential capability | 2 | 3 | 1 | 3 | 5 | Reject for v2 |
| `external-publication` | Push, send or publish an artifact | new effect plane | 3 | 3 | 1 | 4 | 5 | Reject for v2 |
| `shell-package-build` | Install/build via shell or arbitrary process | forbidden authority | 1 | 2 | 1 | 4 | 5 | Reject |
| `browser-interaction` | Drive a live browser session | separate Computer boundary | 2 | 2 | 1 | 4 | 5 | Reject for this experiment |

## Recommended ten-workload cohort

### Direct-favored negative controls

1. **`catalog-top-direct`** — one typed source result, one stable top-item oracle, no workspace.
2. **`manifest-suite-direct`** — one typed source result, one suite/count oracle, no workspace.

These prevent a predetermined “Pysolate always wins” study. The expected mechanism result may favor direct execution because Guest startup and request framing add work without reducing meaningful orchestration.

### Pysolate-favored local control flow

3. **`catalog-threshold-loop`** — iterate a bounded catalog, filter by threshold, stable-sort ties, and produce aggregate counts.
4. **`catalog-score-report`** — compute multiple related summaries and write one canonical workspace report.
5. **`manifest-matrix`** — flatten nested cases/artifacts/metrics into a stable matrix and summary.
6. **`manifest-policy-check`** — execute several bounded checks over metric units, ranges, tags and task classes and return a sorted violation list.

### Hybrid source/workspace boundaries

7. **`source-join-ranking`** — call both existing typed sources, join them locally, and select a plan under a fixed scoring rule.
8. **`source-join-workspace`** — perform the same two-source join while materializing exact plan and summary files.
9. **`seeded-reconciliation`** — read seeded local state, reconcile it with catalog data, and emit exact result plus workspace delta.
10. **`capsule-two-run-rollup`** — split one stateful task across two fresh Guests with an explicit Capsule boundary, verifying intermediate and final workspace identities.

The cohort has two negative controls, four local-control-flow workloads, and four hybrid/stateful workloads. It deliberately reuses only current authority surfaces.

## Comparison conditions

Every workload must have two oracle-equivalent scripted conditions:

### Direct baseline

A Host-owned evaluator sends canonical call envelopes through the same
`capability.Broker.Call` path and performs the transformation in evaluator
code. It must not call handlers directly or bypass schemas, grants, call
budgets, target policy, result validation, receipts, or effect classification.
Workspace pilots require a separately specified research-only bounded state
adapter over the same canonical initial/final tree representation; they may not
reach arbitrary Host paths or be promoted to a Runtime product API.

### Pysolate condition

One fresh Guest receives canonical inputs, the same frozen capability plan, and the same initial workspace. Multi-step control flow executes inside ordinary Python. All external authority still crosses the Broker.

`capsule-two-run-rollup` is the declared exception to “one Guest”: it has exactly two fresh Runs and one identity-bound Capsule handoff in both conditions.

The direct condition is an evaluation baseline, not a second product API. It should live under `research/` or test code and must not create a parallel authority path in Runtime.

## Required measurements

For each workload and condition, record:

- offered, admitted, started, completed, failed and timed-out executions;
- exact task-oracle and final-workspace-oracle outcome;
- Host capability calls and ordered capability identities;
- Host/Guest boundary crossings, with one frozen counting rule;
- canonical bytes entering and leaving each boundary;
- initial/final workspace identities and changed-entry counts;
- Guest count and Capsule handoff count;
- lifecycle outcome and explicit unsupported reason;
- runtime artifact, source commit, corpus, plan and condition identities.

Wall-clock timing may remain diagnostic but is not comparative evidence until preparation, warmup, machine profile, repetition policy and statistical treatment are separately frozen. Token, model quality and monetary cost remain `not_measured` in this phase.

## Contract gaps before implementation

Evaluation v2 must explicitly resolve these gaps rather than mutate v1:

1. replace the exact-three-ID adapter with a versioned v2 corpus that admits the selected IDs and families;
2. represent `direct_scripted` and `pysolate_guest` as conditions distinct from v1 replay/branch treatments;
3. define the boundary-crossing counting rule and canonical byte accounting;
4. allow more than two calls only if a selected workload actually requires them; the proposed cohort can remain at two source calls per Run;
5. represent a bounded two-Run Capsule workload without implying hidden Guest continuation;
6. bind equivalent direct and Guest oracle definitions so condition-specific code cannot silently change the task;
7. preserve private raw rows and body-free portable reports;
8. carry the existing prohibited-claims list forward unchanged.
9. define the research-only direct workspace adapter before admitting any
   workspace candidate; until then, only the two workspace-free pilots are
   implementation-ready.

Lab v1 need not change merely to begin this experiment. Add a Lab v2 field only if the final selected report contains a source fact that cannot be represented honestly by the current projection; do not invent projections in advance.

## Go/no-go and stop conditions

Proceed to implementation only if all ten workloads have:

- frozen code/input/source/workspace identities;
- an exact result oracle and, when applicable, exact final workspace oracle;
- a direct baseline that uses the same typed authority contracts;
- a declared maximum of two captured source calls per Run;
- no credentials, external writes, generic HTTP, shell, subprocess or public-network dependency.

Stop the mechanism phase when:

- all offered condition rows conserve exactly;
- supported rows pass task/effect/workspace oracles;
- boundary and byte accounting reconstruct independently;
- the two negative controls and at least two multi-step workloads produce interpretable contrast;
- failures and unsupported cases remain explicit;
- independent report rebuild and privacy gates pass.

### Real-model activation gate

Do **not** activate a paid model experiment merely because Pysolate can execute the cohort. Activate it only if the scripted baseline shows both:

1. at least two multi-step workloads reduce Host orchestration boundaries without changing task/effect/workspace oracles; and
2. the negative controls demonstrate that the metric does not mechanically favor Pysolate for every task.

The paid phase then requires a separately approved provider/model matrix, randomization, replicates, token ceilings, hard spend cap, failure policy and user-value scoring. None of those decisions are made by this audit.

## Decision

Adopt the ten-workload cohort above as the candidate set for evaluation v2. The next implementation slice should be the v2 condition/corpus contract plus two end-to-end pilot workloads: `catalog-top-direct` as the negative control and `source-join-ranking` as the strongest low-risk contrast. Do not implement the other eight until those pilots prove that direct and Guest conditions can share authority and oracle definitions without contract ambiguity.
