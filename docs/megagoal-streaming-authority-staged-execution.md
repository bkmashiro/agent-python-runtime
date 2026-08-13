# Megagoal: prove streaming authority-staged Agent execution

Status: **Executed; required S1-S3 mechanism implemented and verified.**
Owner: Yuzhe
Execution repo: `~/projects/agent-python-runtime`

This file is a goal brief only. Preparing or updating it does not schedule,
automatically start, or otherwise authorize background execution.

## Mission

Build and verify the smallest real Pysolate mechanism showing that model-generated
append-only Python can be incrementally admitted and executed before generation
finishes, while incomplete or invalid source cannot publish filesystem changes or
dispatch writes.

The primary proof must combine:

```text
append-only generated Python stream
+ exact target-Guest incremental validation
+ complete-suite streaming execution
+ private unpublished filesystem state
+ eager preflight for one Host-qualified
  read_only + idempotent + speculative_safe fixture read
+ reach-gated behavior for one non-speculative-safe control
+ final source seal and publication/write barrier
```

This is an engineering proof, not a production feature or performance claim.

## Non-negotiable semantics

1. **Fresh baseline remains usable.** Streaming off buffers complete source and
   executes an ordinary fresh Run.
2. **No physical-line execution.** Execute only target-Guest-compiler-confirmed
   complete top-level statements or compound suites.
3. **Frozen preamble.** Encoding/module docstring, `__future__`, and static import
   preamble freeze before executable suites. Late/dynamic imports, `eval`, and
   `exec` remain rejected in the initial subset.
4. **One private namespace per stream.** Closed suites share a private module
   namespace only within that speculative Guest.
5. **No publication before final seal.** Filesystem writes target a private
   overlay/attempt; stdout/results and file bodies remain staging data.
6. **Invalid/abandoned suffix.** Destroy Guest and scratch, discard overlay and
   unpublished result, dispatch zero writes, retain truthful bounded evidence.
7. **Two read launch rules.** A closed call may preflight before control-flow
   reach only when the Host adapter qualifies it as `read_only + idempotent +
   speculative_safe`, canonical arguments are complete, and a speculation budget
   admits it. Other calls require actual dynamic reach.
8. **Eager waste is honest.** An unreachable eager-qualified call may dispatch
   once and become orphaned waste. An unreachable non-qualified call must not
   dispatch.
9. **No duplicate dispatch.** If final execution consumes a staged read, it must
   bind to the exact source range/dynamic occurrence/arguments/plan/freshness and
   must not call the provider twice.
10. **Writes remain gated.** A write can at most stage an intent before full
    source validation and authority/approval seal.
11. **Mechanisms remain orthogonal.** Incremental validation, streaming local
    execution, eager reads, fan-out, prepared runtime, memory COW, workspace
    attempts, and cache retain independent off-states.
12. **Do not weaken existing boundaries.** No shell, process, ambient network,
    Host path, credential, arbitrary binary, dynamic package install, or direct
    Broker bypass.

## Scope for tonight

### Required Track A seam

Implement only the internal mechanism selection needed for this proof. Do not
freeze public CLI/config names unless unavoidable.

Required combinations:

```text
incremental validation off/on
streaming local execution off/on
eager speculative fixture read off/on
private publication barrier always enforced in streaming mode
```

Invalid combinations fail before speculative Guest execution with precise errors.
Do not build the entire roadmap's feature matrix tonight.

### Required S1: incremental target-Guest validation

- Define a bounded append-only stream protocol with begin/chunk/end or an
  equivalent explicit state machine.
- Use the exact target Guest CPython parser/compiler as the authoritative
  complete/incomplete/invalid oracle. Host parsing may be a hint only.
- Freeze import-preamble semantics and preserve current exact profile/import
  admission.
- Record stable source byte ranges/digests for admitted complete suites.
- Focused tests: simple statements, multiline expression, `def`, decorated
  `def`, `if/else`, `try/except`, late import, invalid suffix, duplicate/end
  events, cancellation.

### Required S2: streaming local execution

- Execute admitted complete suites in one private namespace.
- Use private filesystem state that cannot mutate the published base before
  final seal. Prefer the smallest mechanism that truthfully proves isolation;
  do not implement general recursive CAS/merge tonight.
- Final valid source must produce the same supported-program result and derived
  filesystem state as the complete-source control.
- Invalid suffix/cancellation must publish nothing and leave the base unchanged.
- All Host tools denied in the S2-only treatment.

### Required S3: eager and reach-gated reads

Use deterministic in-process fixture adapters, not paid or live providers.

Adapter E:

```text
read_only=true
idempotent=true
speculative_safe=true
bounded delayed fixture response
canonical JSON-like literal arguments
```

Adapter R:

```text
read_only=true
idempotent=true
speculative_safe=false
bounded delayed fixture response
```

Prove:

- E may dispatch when its closed literal call is parsed even if unreachable;
- unused E result is recorded as orphaned/wasted;
- R dispatches only when actual Python execution reaches its occurrence;
- unreachable R does not dispatch;
- consumed E or R result dispatches once total;
- invalid suffix may spend E but publishes no FS/output and dispatches no write;
- source-range/occurrence/arguments/policy/freshness mismatch rejects staged
  result reuse;
- separate speculation call/count/cost budget is enforced.

### Required north-star fixture

Create one versioned deterministic stream/timeline:

```text
preamble closes
→ local filesystem analysis suite closes and begins
→ eager-qualified literal read closes and starts
→ model stream delay continues
→ a non-qualified call appears in reachable/unreachable variants
→ private filesystem output is written
→ valid or invalid final suffix closes
→ final seal either publishes selected derived state or discards everything
```

The harness controls token/chunk timing; do not require a paid model. It must
produce machine-readable evidence for overlap and dispatch counts.

### Stretch only after required proof is green

1. **S4 streamed two-child fan-out:** closed structured child descriptors start
   two staged children before parent seal; parent-invalid variant publishes no
   child state/write.
2. **Prepared runtime control:** restore only the safest never-served single-use
   prepared path behind an off-switch if current interfaces permit a bounded
   change.
3. **Linux memory COW:** do not implement on macOS; only recover/design the Linux
   path if required proof is complete and a suitable Linux verification
   environment is available.
4. **Content-addressed cache/React re-evaluation:** no implementation unless every
   required streaming deliverable and S4 are complete.
5. **General external-write effect plane:** do not expand beyond a deterministic
   denied/staged write fixture required to prove the barrier.

## Architecture constraints

- Understand current source validation, Guest bootstrap ABI, Broker lifecycle,
  Workspace Manager, Wazero Factory, RunPlan, and observation records before
  editing.
- Prefer a narrow internal package/state machine over invasive rewrites.
- The existing non-streaming API and CLI remain compatible.
- The direct baseline continues through `runtime/capability.Broker.Call`; no
  fixture handler bypass.
- Do not claim that arbitrary Python streaming is supported. Version and name
  the supported subset.
- Do not use Host Python as semantic truth for the Guest.
- Do not expose private body, Host path, credentials, remote environment details,
  or speculative result bodies in public evidence.
- Do not build Lab/UI work tonight.
- Do not add broad third-party dependencies without necessity.

## Execution and delegation policy

Use at most two concurrent subagents for independent work with independent value.
Recommended split after the main controller freezes shared contracts:

- **Lane 1:** S1 stream/parser/admission state machine and focused tests.
- **Lane 2:** S3 fixture adapter, speculative operation identity/evidence, and
  focused tests.

The main controller owns S2 integration, public/internal contracts, merge,
end-to-end proof, and final delivery. Subagents receive exact file boundaries and
must not both edit shared contract files concurrently.

Review policy:

- no ritual spec-review/quality-review subagents;
- each worker runs focused tests for its package;
- main controller performs one direct diff/authority-boundary review;
- no second opinion unless a concrete semantic uncertainty remains.

## Test cadence

During a slice, run only focused tests and `git diff --check`.

Do not repeatedly run full repository, race, vet, or Linux artifact rebuild after
every edit. After the required Track S proof closes, run one consolidated gate:

```text
GOTOOLCHAIN=go1.26.5 go test ./... -count=1
GOTOOLCHAIN=go1.26.5 go test -race <changed stateful packages> -count=1
GOTOOLCHAIN=go1.26.5 go vet ./...
python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s tests -p 'test_*.py'
git diff --check
```

If actual Guest behavior/artifact changes, rebuild Linux x86_64 Guest and run the
relevant real-Guest acceptance flow once at the Track boundary. Prefer the
existing private Linux path; keep all remote caches in job-local scratch. Do not
manually trigger GitHub Actions.

## Performance measurement

Do not claim speedup from synthetic code without a control. Required treatments:

```text
complete-source ordinary fresh
incremental validation only
streaming local execution, tools denied
eager speculative fixture read on/off
reach-gated fixture read
valid/invalid suffix
prepared runtime off/on if stretch is implemented
```

Measure at least:

```text
time to first complete suite
remaining simulated generation time
local-prefix execution interval and overlap
eager/reach-gated read start/end
source-end to result
end-to-end completion
provider dispatch count
orphaned speculative count/cost
base and overlay file identities
published/discarded state
```

Report mechanism measurements, not production performance or universal Agent
benefit.

## Delivery gates

### Gate 0: repository and contract recovery

- clean baseline confirmed;
- current source/Guest/Broker/Workspace paths mapped;
- proposed internal seams and affected files recorded.

### Gate 1: S1 proof

- append-only state machine and target-Guest oracle work;
- focused syntax/import/invalid tests pass;
- streaming off path remains unchanged.

### Gate 2: S2 proof

- supported valid stream matches full-source control;
- invalid/cancel paths leave base unchanged and publish nothing;
- tools are absent/denied.

### Gate 3: S3 proof

- eager and reach-gated fixture behavior matches the two launch rules;
- no duplicate dispatch;
- invalid suffix spends only admitted read resources;
- writes and FS publication remain gated.

### Gate 4: north-star evidence

- deterministic fixture emits structured timeline and identities;
- all treatment controls execute;
- claims map to actual outputs.

### Gate 5: consolidated verification

- full Go/Python/race/vet/diff gates pass;
- Linux Guest rebuild/E2E passes if artifact semantics changed;
- temporary local/remote artifacts are cleaned.

### Gate 6: delivery

- active design/roadmap updated only where implementation changes Current truth;
- no Proposed mechanism is relabeled Current without evidence;
- signed commit(s) pushed to `main`;
- `main == origin/main`, working tree clean;
- final report leads with actual implementation, test output, limitations,
  measured overlap, and stopped stretch items.

## Stop conditions

Stop and report honestly rather than forcing implementation if:

- target Guest cannot expose a safe bounded incremental compile oracle without an
  artifact/ABI redesign too large for the session;
- incremental execution cannot preserve supported complete-module semantics;
- private FS staging would require unsafe direct-write claims;
- speculative result occurrence binding cannot prevent duplicate dispatch;
- current architecture requires broad public API breakage;
- consolidated tests expose a foundational regression that cannot be isolated;
- a required Linux artifact path is unavailable after one reasonable fallback.

A stopped spike must leave the default path green, preserve a minimal failing
fixture, and record the exact blocker and next experiment.

## Tonight's final deliverable

The preferred result is a working required Track S proof through Gate 6. A valid
fallback deliverable, if stopped by a real architecture constraint, is:

```text
working S1/S2 subset
+ minimal executable blocker fixture for S3
+ measured evidence
+ precise contract/ABI change required next
+ default path green
```

Do not substitute a mock timeline, fabricated provider output, or documentation
claim for a mechanism that did not execute.
