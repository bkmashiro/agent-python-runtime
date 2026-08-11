# Placement canary stop record (2026-08-11)

## Decision

Stop before the paid three-arm development screen.

The execution mechanisms are real and working:

- a scripted `rd-003` program completed in the real WASI Guest under the artifact-bound `base` profile, with a Host-authored RunPlan and exact final state;
- the pinned, unmodified Cloudflare Computer `v0.1.1` Worker JavaScript backend completed the equivalent local workspace transform under Wrangler local;
- the thin Computer harness produced an authoritative exact trace for a Host-owned trusted-module call without patching upstream.

The first profile-qualified Spark program canary did not close after the one permitted minimal interface correction. The final admitted import-free proposal omitted the user-requested `cd("Documents")`, attempted to read `metrics.csv` from the wrong workspace directory, and ended with `python_exception`. That is a model-program failure, not evidence of a Pysolate Runtime defect.

Further tuning on this one task would optimize the prompt against one observed failure while adding no new task evidence. This meets the roadmap's anti-rabbit-hole stop condition.

## Corpus correction frozen before the stop

The canary also exposed a comparator issue before any decision task was shown to a model: an exact BFCL filesystem call sequence is not substrate-neutral. For reversible workspace-local work, `touch` followed by `echo` and `echo` alone can produce the same authoritative workspace state; native Cloudflare Computer filesystem operations also do not emit the same Host-tool trace.

The corpus and scorer now make that boundary explicit:

- workspace-local reversible tasks: exact final workspace state is primary; arm-native operation traces are retained as diagnostic and are not treated as cross-arm semantic effects;
- configured Host capabilities and irreversible/staged effects: exact semantic calls, arguments, order, and rejection remain strict;
- no observed score was reclassified, because the corpus change was frozen before a completed model trial or any sealed decision exposure.

## Evidence boundary

`summary.json` contains only digests and a sanitized failure classification. The raw model proposal remains private at `/private/tmp/pysolate-placement-canary/debug/` and is not committed.

Provider token usage for the failed proposal is unknown because the canary command did not persist usage on an execution failure. No token total is inferred. The failed attempt therefore cannot participate in formal efficiency statistics.

No Direct or Computer paid model canary, 40-task development screen, sealed decision cohort, ICL qualification, placement error analysis, or production Cloudflare experiment was run.

## What this evidence supports

- profile-qualified Pysolate can execute a bounded scripted workload in the real Guest;
- the fixed Cloudflare Computer local comparator and trusted-module trace surface work;
- the current program-generation treatment is not ready for a paid multi-task placement screen.

It does **not** support making Pysolate the default, estimating replacement share, comparing three-arm accuracy/cost, or making Cloudflare production claims.

## Continuation prompt

```text
Resume the Pysolate placement program from eval/agentic/results/placement-canary-stop-2026-08-11/ and .hermes/plans/2026-08-11-pysolate-placement-decision-megagoal.md, but do not rerun or tune rd-003.

First design a new pre-registered program-generation canary cohort of at least 6 development tasks sampled across workspace transforms, exact trusted capabilities, and admission rejection. The treatment must be frozen before model calls, must expose only the qualified base-profile import surface, and must persist a sanitized result plus usage/evidence even when provider, admission, or execution fails. Keep raw provider/proposal data private. Define workspace-local reversible correctness by exact final state and keep arm-native traces diagnostic; keep Host capability and irreversible effects exact.

Run one replicate per arm only after scripted Direct/Pysolate/Cloudflare Computer parity passes for the same six tasks. If profile-qualified Pysolate fails at least 2 of 6 for model-program reasons, do not tune per task: classify the treatment as not ready, save the cohort, and stop. If infrastructure/admission fails on any arm, fix only one shared harness defect and rerun once; otherwise stop. Do not expose sealed decision tasks, run GitHub CI, deploy Cloudflare, use Docker, or publish raw debug. If the six-task gate passes, continue the existing 40-task development screen and sealed decision plan with identity-bound artifacts.
```
