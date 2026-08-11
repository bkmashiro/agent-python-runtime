# North-star evaluation contract

## Status

**Proposed evaluation contract, frozen before result generation.**

This document defines the workload, comparison validity rules, lifecycle units, metrics, and oracles required to evaluate the post-Code-Mode product thesis. Explicit typed requirements rejection, artifact-bound profile/import manifest admission, target-Guest-generated discoverable-root inventory, curated fresh-Guest import/operation qualification, and bounded conservative source comparison are Current foundations; workspace overlays, real-provider reconciliation, and Cloudflare baselines are not implemented here.

The Current implementation boundary remains [Architecture](architecture.md), [Workspace Capsule v1](workspace-capsule.md), and the artifact-bound test/evidence suites. The broader system roadmap is maintained in [Vinculum](https://github.com/bkmashiro/vinculum/blob/main/docs/roadmap.md).

## 1. Thesis under test

Pysolate should not be evaluated as “Python in WASM with snapshots.” Cloudflare Python Workers already execute CPython through Pyodide/WASM, prepare top-level imports at deploy time, snapshot linear memory, and restore that snapshot to bootstrap new V8 isolates.

The distinct hypothesis is:

> A generated Python program can receive a logically single-use, clean, capability-bounded CPython execution for every Run, at a cost approaching a warm interpreter, while all external authority and effect truth remain Host-owned.

Current Pysolate implements one untrusted Run per served instance. A served fresh/preinitialized/COW-ready instance is closed and discarded; it is not returned to the ready pool. The Host creates or restores a never-served replacement from a trusted prepared baseline. Complete restoration and reuse of a served instance remain deferred.

## 2. Hypotheses

### H1 — Programmatic control-flow value

For a bounded data-heavy workflow, generated Python reduces model turns and tokens relative to a direct-tool loop without reducing authoritative final-state correctness.

A bounded development pilot using `gpt-5.3-codex-spark` over the corrected six-task routing diagnostic now provides preliminary evidence for this hypothesis. Across two clean replicates, forced Direct passed 11/12 trials with 31 provider calls and 523,521 total Codex CLI tokens; forced Python/Pysolate passed 10/12 with 12 provider calls and 193,471 tokens. On the nine task/replicate pairs where both conditions passed, Python used 62.15% fewer tokens and 60.87% fewer provider calls. The frozen two-stage Hybrid treatment passed only 5/12 and is not ready as a default router. This is development-only evidence: it has no Computer arm, latency measure, profile-qualified placement claim, or general-workload estimate. Exact trial artifacts and checksums are under `eval/agentic/results/codex-spark-routing-2026-08-11/`.

### H2 — Lifecycle economics

For a matching prepared profile, Pysolate reduces request-path initialization latency relative to fresh CPython/WASI while preserving one-Run-per-served-instance semantics. Preparation, memory, refill, and miss costs must be included.

### H3 — Isolation contract

Across sequential Runs, only the explicitly bound ordinary-file workspace may continue. Python heap, WebAssembly memory, mutable module state, Broker, capability budget, descriptors, `/tmp`, and Host path identity must not continue.

### H4 — Ambiguous-effect safety

For a qualified provider, provider acceptance followed by response loss enters a durable ambiguous state, blocks blind retry, and requires authoritative reconciliation. This hypothesis is **Proposed** until a real provider adapter and fault-injection verifier exist.

### H5 — Honest preflight profile rejection

For requests with an explicit bounded `requirements` declaration, Pysolate returns a typed `runtime_unsupported` outcome with required features and the legacy ABI field `escalation_required=true` before execution. That field means only that placement must change before a new Run. An explicit `compatibility` manifest separately opts into Static Import Agent Code: root-only declarations must exactly equal one module-level absolute import preamble and fit Host policy bound to verified artifact identity. The Host scanner rejects obvious violations early; exact target-Guest AST/compiler validation and preloading occur after never-served Guest checkout but before workspace activation or Broker construction. A pre-cache native CPython gate enforces the sealed module set. Dynamic, relative, conditional, nested, star, late, undeclared, or unused Agent imports are unsupported. Any late library import denial fails the current Run and never launches, retries, or migrates to a VM. This mechanism is **Current**; its placement and completion share on a representative workload corpus remains **Proposed**.

## 3. North-star workload

```text
read 50 bounded records
→ Python filter / join / aggregate
→ write a report into an attempt workspace
→ update one exact-reversible record
→ stage one approval-gated external action
→ inject response loss after provider acceptance
→ reconcile without duplicate dispatch
→ commit the selected workspace revision
→ emit independently validated evidence
```

The full workflow is an end-state target. Until workspace overlays and a qualified provider exist, Current evaluations must execute only the implemented subset and label omitted stages explicitly.

## 4. Comparison conditions

### 4.1 In-repository conditions

1. direct tool loop;
2. Pysolate `fresh`;
3. Pysolate `single-use-preinitialized`;
4. Pysolate `cow-ready-single-use` on a qualified Linux host;
5. profile hit and profile miss/construction as separate conditions.

### 4.2 External lifecycle baselines

Add only when the exact version, artifact, environment, and authority surface can be frozen:

- native fresh process;
- native warm process pool;
- native warm NumPy pool;
- Node-side just-bash CPython/Emscripten;
- Cloudflare Python Workers snapshot construction, cold-isolate bootstrap, and warm request on a reused isolate.

Code Mode is principally a programming-model/effect-workflow comparator. Computer is principally a durable-workspace/backend-facade comparator. Neither should be forced into an unlike per-Run runtime benchmark.

## 5. Lifecycle units

Every result must name its unit of preparation and reuse:

| Unit | Meaning |
|---|---|
| deployment | code/package validation, top-level preparation, snapshot construction |
| isolate | one V8/workerd instance that may handle multiple requests |
| slot | one Pysolate execution inventory item derived from a prepared baseline |
| Run | one Pysolate request; a served slot handles exactly one untrusted Run |
| request | one external invocation; may or may not map one-to-one to an isolate |
| refill | construction of replacement ready inventory after checkout |

A Cloudflare warm request on a reused isolate is not directly equivalent to a Pysolate COW checkout. A Pysolate profile hit is not equivalent to native fresh import. Results must expose the different amortization boundary rather than collapsing them into one “cold-start” number.

## 6. Fixed inputs and authority

Across comparable conditions, freeze:

- task input corpus and authoritative expected state;
- generated source or generation policy;
- tool schemas, names, argument/result limits, and error taxonomy;
- credentials and endpoint scope;
- network policy;
- workspace input tree and quotas;
- approval and commit policy;
- clock/random/read tape where replay is claimed;
- model/provider/version and decoding parameters when a model is in the loop;
- machine class, CPU allocation, memory limit, runtime version, artifact digest, and package profile.

A backend may offer more compatibility, but a comparison is invalid if it silently receives more authority.

## 7. Required measurements

### Task and model

- authoritative final-state correctness;
- output-schema correctness;
- task completion rate;
- model turns;
- input/output/cached tokens;
- repair attempts and profile-rejection classifications.

### Runtime lifecycle

- deployment/profile construction;
- queue and admission;
- checkout/restore;
- request preparation;
- execution;
- result validation;
- close/retire;
- replacement refill;
- profile hit/miss and unsupported/profile-rejection frequency.

### Resources

- ready, active, refilling, and peak slot counts;
- process RSS/PSS where available;
- canonical/shared/private/anonymous memory where attributable;
- FD count and temporary-root count;
- CPU time, wall time, and page faults;
- evidence bytes and verification time.

### Tools and effects

- Guest capability calls;
- Host adapter calls;
- real provider dispatches;
- duplicate provider effects;
- approval transitions;
- rollback/compensation attempts;
- ambiguous and reconciled outcomes.

### Workspace

- base identity and revision;
- attempt/overlay identity when implemented;
- changed files/bytes;
- commit, discard, freeze, or conflict disposition;
- absence of Host paths in Guest-visible output and public evidence.

## 8. Independent oracles

A successful JSON response is insufficient. The evaluation must use:

1. task-specific final-state oracle;
2. strict response/evidence schemas;
3. artifact/source/profile identity verification;
4. Host ledger semantic verifier;
5. provider readback for qualified external effects;
6. workspace tree inspection through the Host boundary;
7. real instance lifecycle verifier for heap/tmp/Broker/FD freshness;
8. duplicate-dispatch and crash-point counters for effect qualification.

A hash, receipt, signature, or self-consistent log establishes integrity or identity only within its declared contract. It does not establish provider truth or semantic correctness by itself.

## 9. Fault model

The eventual full workflow must inject at least:

- before dispatch;
- after intent journal, before provider call;
- after provider acceptance, before response receipt;
- after response receipt, before ledger transition;
- before and after workspace commit;
- Host/controller restart with durable ledger reopen;
- timeout and cancellation after a deterministic Guest barrier;
- malformed/duplicated/trailing evidence input;
- provider readback drift and adapter expiry.

No live-provider fault injection may run without separate attended authorization and an explicitly bounded test/sandbox resource.

## 10. Invalid comparisons

A result is invalid if any of the following holds:

- compares `A+B+C` fresh initialization with `C` prepared hit without reporting the preparation boundary;
- reports only profile-hit latency and omits construction, hit rate, memory, miss, and refill;
- compares a multi-request reused isolate with a one-Run-per-slot runtime as though their lifecycle contracts were equal;
- changes tool schemas, task inputs, authority, network, credentials, workspace scope, or output oracle across conditions;
- treats a local fake as a qualified real provider;
- treats response loss as proof of provider failure;
- treats compensation or readback as exact rollback;
- claims deterministic replay while clock/random/provider observations are not fixed;
- reports a Linux COW result produced by a fresh/prepared fallback;
- treats a single RSS sample as an instance-correctness oracle;
- omits failed, denied, timed-out, cancelled, ambiguous, or discarded attempts.

## 11. Current executable evidence

The repository currently provides:

- strict Go/Python unit and schema suites;
- real CPython/WASI integration tests;
- artifact-bound fresh/prepared/COW benchmark evidence contracts;
- Workspace Capsule verifier v2 with 30 default checks;
- optional bounded stress loops and Darwin/Linux FD oracle;
- deterministic local transaction/reconciliation fixtures;
- trace/evidence bundle semantic validation.
- a six-task, balanced routing diagnostic with two `direct_favored`, two `python_favored`, and two `boundary` tasks;
- `apyrun-eval-mechanism`, which executes each routing task through both direct Host calls and one real CPython/WASI Guest Run and checks exact call trace plus final state.

The mechanism report is deliberately labeled `mechanism_only_not_model_evaluation` and `internal_legacy_no_manifest`. It demonstrates that one Guest Run can carry the same Host-mediated tool sequence as multiple direct calls while preserving the task oracle. It prohibits claims about model quality, token or latency reduction, Computer replacement rate, profile-qualified placement, or decision eligibility. It uses deterministic oracle-authored Python—not model output—and records no timing.

Run it against an exact verified artifact:

```bash
go run ./cmd/apyrun-eval-mechanism \
  -guest /path/to/agent-python-runtime.wasm \
  -dataset eval/agentic/routing/v1 \
  -repository-commit <40-hex-commit> \
  -output /new/path/agentic-mechanism-baseline.json
```

These establish bounded runtime and local protocol foundations, including H5's explicit preflight mechanism and negative classification tests. They do not establish H4 against a real provider, model-generated workflow quality, or H5's route share on a representative external workload corpus. Those are the next decision-bearing experiments; the scripted mechanism report is only their prerequisite smoke test.

## 12. Phase exit rule

A phase advances only when it has:

1. a frozen contract written before result generation;
2. real execution under the claimed strategy;
3. machine-readable evidence and independent semantic validation;
4. explicit unsupported/insufficient states instead of silent fallback;
5. a published limitation section;
6. no authority expansion hidden inside compatibility or benchmark setup.
