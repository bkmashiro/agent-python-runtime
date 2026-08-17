# Source-Prefix Overlap Mechanism Experiment Implementation Plan

> **For Hermes:** Execute this plan task-by-task with RED/GREEN tests and signed commits; do not broaden into dynamic DAG scheduling.

**Goal:** Produce exact-Guest, matched, independently validated mechanism-stress evidence that sequential Python source-prefix execution can overlap one reached Host-mediated read with continued source production.

**Architecture:** Reuse the existing exact-Guest `_StreamingSession`, `runtime/streaming` prepare seam, private workspace and typed Broker. A bounded timed producer emits checked-in safe source chunks. The baseline waits for the same frozen production schedule to complete before delivering all chunks; the treatment delivers each chunk at its scheduled time. Both lanes execute the same source, Plan, handler, oracle, lifecycle, logical call and physical dispatch. No DAG, dependency scheduler, static preflight, semantic transformation, provider or external write is introduced.

**Tech Stack:** Go, existing Wazero/CPython-WASI Guest, `runtime/streaming`, `runtime/capability`, `research/workflowbench`, JSON evidence.

---

## Scope and claim boundary

- Evidence layer: `mechanism_stress` / `authored_mechanism_case`.
- Target mechanism: `source_prefix_execution_overlap`.
- Claim: one authored early-read workload overlaps a frozen source-production tail while preserving result, effect/receipt disposition, call counts, source schedule, Plan and runtime identity.
- Non-claims: natural prevalence, provider/model behavior, generic speedup, dynamic DAG scheduling, static call hoisting, parallel tool execution, external-write rollback, or production Harness integration.
- Existing frozen tau2 cohorts, replay evidence and prior artifact roots are read-only.
- Portable evidence contains only checked-in safe fixture identities, digests, timings, call counts, dispositions and aggregate comparisons. Private logs/artifacts stay under a new `0700/0600` evidence root.

## Frozen experiment shape

The checked-in source chunks form one sequential program:

```python
record = slow.lookup("alpha")
label = record["label"].upper()
result = {"label": label}
```

The source schedule closes the first read statement early and completes the remaining source later. The Host fixture handler sleeps for a fixed bounded duration and returns `{"label":"Alpha"}` through the real Broker. A fixed final wrapper returns `stream_final`.

Treatments:

1. `generate_then_execute`: wait through the entire frozen source-production schedule, then deliver the identical begin/chunk/end prepares.
2. `stream_while_generating`: deliver begin immediately and each chunk at its frozen release offset through a bounded queue while Guest execution may block in the Host read.

Alternate treatment order by pair index. Create a fresh private attempt, Broker and Runner per lane. Runner construction/readiness is outside the mechanism timing window in both lanes; record it separately if useful and do not call it end-to-end provider latency.

## Acceptance gates

- Independent oracle validates canonical result `{"label":"ALPHA"}`.
- Exactly one logical call and one physical dispatch in every lane.
- Same source/chunk schedule, artifact/profile, Plan/grants, capability spec/handler, inputs, workspace base and oracle identities.
- No write-class receipt/effect and no workspace divergence.
- Queue bounds: at most 32 chunks and 64 KiB buffered source; overflow/cancellation fails closed.
- Evidence decoder rejects duplicate/unknown fields, wrong digests, unbalanced pairs, lane drift, oracle failure, call-count drift, fallback, invalid timing order and an unsupported claim boundary.
- Final result reports all rows, pairwise deltas and medians. A positive speedup claim requires every accepted pair to pass the oracle and the preregistered median treatment wall time to be lower than baseline; otherwise report a valid null/negative mechanism result.

### Task 1: Add bounded timed-source contracts with RED tests

**Files:**
- Create: `research/workflowbench/source_prefix_overlap.go`
- Create: `research/workflowbench/source_prefix_overlap_test.go`

**Steps:**
1. Write RED tests for frozen schedule validation, stable identity, deterministic treatment order, bounded enqueue, cancellation and overflow.
2. Run `go test ./research/workflowbench -run 'Test(SourcePrefix|TimedSource)' -count=1 -v` and confirm missing API/behavior failures.
3. Implement the minimal schedule, producer and bounded event recorder.
4. Rerun focused tests and `go test -race ./research/workflowbench -run 'Test(SourcePrefix|TimedSource)' -count=1`.

### Task 2: Add strict evidence schema and independent oracle with RED tests

**Files:**
- Modify: `research/workflowbench/source_prefix_overlap.go`
- Modify: `research/workflowbench/source_prefix_overlap_test.go`

**Steps:**
1. Write RED tests for strict JSON decode and all acceptance gates above.
2. Implement canonical digest helpers, row/pair/evidence validation, median calculation and body-safe encoding.
3. Prove mutations of source schedule, lane config, artifact, Plan, result, call counts and timing fail closed.
4. Run focused and race tests.

### Task 3: Add matched real-Guest experiment command with RED boundary tests

**Files:**
- Create: `research/workflowbench/cmd/source-prefix-overlap/main.go`
- Create: `research/workflowbench/cmd/source-prefix-overlap/main_test.go`

**Steps:**
1. RED-test required exact artifact/manifest/commit/preregistration inputs, output permissions and refusal of mutable/identity-mismatched inputs.
2. Implement a real capability Plan and fixed slow read handler; no provider/network access.
3. Implement fresh matched lane construction over existing `streaming.ExecuteStream` and Wazero Runner.
4. Emit private strict evidence atomically with `0600`; stdout prints only bounded aggregates/digests.
5. Run command-package tests and a fixture-only smoke.

### Task 4: Freeze public preregistration and run exact artifact experiment

**Files:**
- Create: `docs/evidence/source-prefix-overlap-mechanism-v1.json` as the fixed body-safe preregistration/anchor before final measurement.
- Create after measurement: `docs/evidence/source-prefix-overlap-mechanism-v1-result.json` only if the projector validates private evidence and emits aggregate/body-safe fields.
- Create/update: `docs/research/source-prefix-overlap-mechanism-v1.md`.

**Steps:**
1. Freeze case, oracle and complete lane-config digests plus repetition count and comparison rule.
2. Commit the implementation/preregistration with `git commit -S` before final measurement.
3. Build an exact-commit Guest artifact on the workstation and verify manifest/source/digest plus private `0700/0600` permissions.
4. Run the preregistered matched experiment without provider access.
5. Validate and project body-safe evidence; never rewrite the preregistration after seeing results.

### Task 5: Verification and closeout

1. Run focused/race tests, `go test ./...`, `go vet ./...`, Python suites, Linux/COW cross-compile and `git diff --check`.
2. Run exact-artifact real-Guest focused E2E for source streaming and the experiment command.
3. Request a frozen-diff independent review focused on matched identity, timing attribution, oracle independence, queue/backpressure and claim boundary; fix blockers and rerun affected gates.
4. Sign final commits. Do not push, merge, deploy or publish unless separately requested.
