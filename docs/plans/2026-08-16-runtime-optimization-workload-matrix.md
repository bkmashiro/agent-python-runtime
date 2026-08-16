# Runtime Optimization Workload Matrix Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Measure Pysolate’s workload-dependent isolation cost and optimization recovery without extrapolating from one natural READ or one synthetic microbenchmark.

**Architecture:** Use one variant-bound, zero-model replay contract for frozen natural programs and separate synthetic suites for mechanisms that natural T2 does not exercise. Every row validates semantic/effect/receipt/oracle parity before timing; unsupported strategies produce an explicit skip reason.

**Tech Stack:** Go integration/E2E harness, Wazero, Python tau2 adapter, private append-only JSON evidence, Linux COW workers.

---

## Claim decomposition

Do not publish a single “Pysolate speedup.” Report four independent questions:

1. **Isolation tax:** native/direct control versus fresh Pysolate for the same source and effect.
2. **Tax recovery:** fresh versus prepared/COW under equivalent Pysolate semantics.
3. **Agent interaction efficiency:** direct model tool calls versus a multi-call program with local control flow.
4. **Safe reuse:** repeated pure programs, compilation and concurrent duplicate work only where freshness/effect contracts permit reuse.

A mechanism may help one class and hurt another. Aggregate only within a preregistered workload mix and retain per-class rows.

## Workload matrix

| Class | Representative work | Primary mechanism | Valid comparison | Required semantic gate |
|---|---|---|---|---|
| `external-read-single` | One natural tau2 READ; existing 36-program corpus | Fresh, prepared, COW | Isolation tax and startup recovery | Same task/source/Plan/args/content/oracle; one physical effect per lane |
| `external-read-batch` | Two to eight independent READs in one Agent-authored program plus local join/filter | Programmatic surface, prepared/COW | Model/tool round trips and total wall time | Same ordered effects and final answer; no hidden result cache |
| `pure-local-short` | JSON parsing, projection and small aggregation | Prepared runtime | Startup-dominated latency | Exact output digest; zero Broker calls |
| `pure-local-heavy` | Bounded CPU transform over medium/large JSON | Prepared runtime | Isolation tax versus useful compute | Exact output digest and bounded CPU/input size |
| `pure-repeat-exact` | Exact pure source/input repeats | Semantic reuse/function cache | Hit rate and saved compute | Purity proof, exact identity, zero external effects |
| `concurrent-duplicate-pure` | Simultaneous identical pure invocations | Single-flight | Physical executions and followers | One leader, identical outputs, bounded failure propagation |
| `multi-agent-artifact-burst` | Distinct programs sharing one Guest artifact/profile | Compile/runtime pooling | Compilation amortization, throughput | Distinct Run/attempt identities; no cross-run state leakage |
| `workspace-mutation` | Bounded private-file mutation and discard/export | Prepared+COW | Reset cost and dirty-byte density | Final workspace oracle, no state leakage, measured changed/materialized bytes |
| `external-read-repeat` | Repeated identical external READ | No reuse by default | Fresh/prepared/COW only | Every READ physically dispatched; cache/reuse forbidden absent freshness contract |

## Profiles

### CI smoke

- One body-safe authored fixture from `pure-local-short`.
- One private frozen `external-read-single` fixture when live tau2 paths are configured.
- Variants: `fresh`, `prepared`; COW runs only on supported Linux.
- One attempt; validates identity, selection, observations, receipts and output parity only.
- No performance claim.

### Natural replay

- Corpus: all 36 frozen remediation-v2 programs, retained privately.
- Variants: `fresh`, `prepared`, and `cow` where `COWProbe.COWSelected=true`.
- A no-Pysolate direct adapter is an absolute-only control because it omits Guest/authority lifecycle.
- Randomized deterministic lane order; explicit warmup policy; repeated attempts.
- Report Host phase timings separately from Guest `exec` and adapter/effect time.

### Deep synthetic

- All workload classes above with light/medium/heavy payloads where meaningful.
- At least 100 successful measured samples for any p99 claim.
- Record failures outside latency distributions.
- Linux-only COW rows include ready slots, active slots, RSS/PSS, dirty/materialized bytes and recovery behavior; mapping counts are not concurrency.

## Identity and evidence contract

Each measured attempt binds:

```text
parent corpus identity
+ workload/case digest
+ variant/config digest
+ task or fixture ID
+ logical turn
+ measured attempt
```

Use an independent private evidence root per protocol. Persist:

- source/artifact/profile/Plan/grant/config digests;
- `MechanismSet`, `PreparedState`, `COWProbe` and explicit skip reason;
- Host-authored observation event types and event digests;
- request/response/content/oracle digests;
- logical invocation, physical receipt count and effect disposition;
- analysis-engine construction, analysis, execution-engine construction, run, total Host wall time and Guest `exec` time;
- platform, CPU/runtime metadata and trial order for measured protocols.

Never reuse old Run/call/receipt identities. Never call a fallback sample COW or prepared. Never infer runtime observations from Agent `pysolate_events`.

## Current implemented slice

- `docs/research/agent-output-and-workflow-evidence-v1.md` and `research/workflowbench/evidence_layers.go` define the `return` + bounded `print` contract and enforce natural-census, trace-DAG, and mechanism-stress claim separation.
- `integration/e2e/tau2_t2_replay_test.go` defines exact `fresh`/`prepared`/`cow` mechanism variants and variant-bound Run identity.
- `integration/e2e/tau2_source_bound_canary_test.go` exposes phase timings, `PreparedState`, `COWProbe` and Host observation digests through the same real Guest/Broker path used by T2.
- Pilot v10 binds its private preregistration to the fixed public anchor `docs/evidence/tau2-t2-runtime-replay-pilot-v1.json`. Run identity binds the frozen case and full runtime configuration. Descriptor-rooted private I/O rejects pre-open and post-open path replacement. The pilot passed exact fresh/prepared oracles and remains non-performance evidence.

## Next implementation tasks

1. Add an append-only private replay driver that extracts frozen program fixtures without exposing bodies.
2. Add a strict no-provider aggregate builder that rejects identity, mechanism-selection, observation, receipt, content or oracle drift.
3. Pre-register repeated natural replay only after the driver and aggregate tamper tests pass.
4. Run macOS `fresh`/`prepared`, then Linux `fresh`/`prepared`/`cow` under the same protocol.
5. Implement multi-call natural/authored cases before claiming Agent interaction efficiency.
6. Add pure/concurrent/workspace synthetic suites one mechanism at a time; do not pool their results into a universal speedup.
