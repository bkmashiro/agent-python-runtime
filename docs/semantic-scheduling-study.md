# Semantic scheduling study: phase costs before policy

Status: phase harness and bounded live-I/O scheduling implemented; broader policy remains a study.
Study baseline: `51293c7b91ab6b24011fb81f7a1769b378c34e39`. The explicit attempt lifecycle landed in `6268f28c6bf08d40df358e42608b75cfd6e3621e`; live-I/O scheduling and its pilot landed in `15bf3aabed01eedec2750c1485b6eea1c7382647`.

## Question

Pysolate already exposes explicit Host-tool and durable-wait boundaries. Before adding a scheduler, determine whether common agent-generated Python spends enough time and resident resources at those boundaries for reordering, parking, or reconstruction to produce a net system benefit.

The study asks:

1. Which ordinary programs are tool-I/O, tool-capacity, memory-residency, or CPU bound?
2. When a tool result is ready, how much work remains before the Run completes, issues another tool call, or parks?
3. When does releasing a Guest save enough memory-time or admission capacity to repay journal, teardown, reconstruction, replay, and page-fault costs?
4. Can a small set of explicit phase observations support population simulation without pretending to predict arbitrary Python?

This document does not authorize arbitrary preemption, unsafe effect retries, a distributed scheduler, or general static cost inference.

## Current boundaries

- `Runner.Advance` reconstructs one deterministic attempt and returns durable completion or park as explicit state.
- A parked durable Run retains its definition, completed call history, and wait identity; it does not retain a resumable Python stack.
- `Executor` separately bounds running and resident attempts. Inline Host calls keep the running slot; an explicitly `ExternalIO` Tool yields it while retaining the live Guest and reacquires it before Python continues.
- Ready live continuations precede new admissions, with a bounded burst; external Tool and resident counts remain independently bounded.
- Tool recovery declarations determine whether an unresolved external operation may be dispatched, looked up, parked, or sent for manual handling.
- Prepared copy/COW can reduce reconstruction cost for one fixed seed. COW is Linux-only.

Scheduling policy should consume these boundaries through an attempt-control interface. It should not own the journal, tool-effect protocol, Guest implementation, or prepared-image mechanism.

## Workload profile

Start with fixed source programs, deterministic inputs, and controlled Host tools. Do not regenerate programs with an LLM during measurement.

### 1. Read then finish

```python
record = remote_read(key=inputs["key"])
result = {"value": record["value"] + 1}
```

Common shape: one remote read followed by a short suffix. The useful point is result-ready continuation: run a little more, complete, and release the Guest.

### 2. Sequential tool chain

```python
first = remote_read(key=inputs["key"])
second = remote_detail(id=first["id"])
result = second["value"]
```

Common shape: the next request depends on the previous response. Calls cannot be parallelized, but promptly running each continuation can keep the tool pipeline moving.

### 3. Read, durable wait, and re-admit

```python
record = remote_read(key=inputs["key"])
approved = approval(change=record)
result = record["value"] if approved else None
```

Common shape: a completed read followed by a user or external decision. The first attempt should park and release its Guest. Re-admission reconstructs the Guest, replays the read outcome without dispatch, consumes the decision, and finishes.

### 4. Local NumPy computation

```python
values = np.arange(inputs["size"], dtype=np.float64)
result = float((values * values).sum())
```

Common negative control: no Host wait and potentially meaningful CPU/memory work. Reordering or reconstruction should not be assumed beneficial.

Workspace JSON/TOML/YAML edits remain an acceptance workload, but local filesystem calls are normally too short to justify releasing a Guest. They should be added to scheduling experiments only when combined with an explicit long wait or measured storage bottleneck.

## Phase observations

The initial harness records only boundaries it owns:

- service/Runner setup;
- attempt admission to terminal, parked, or error result;
- time spent inside controlled Host tools;
- time intentionally parked outside a Guest;
- decision persistence;
- re-admitted attempt and replay;
- tool dispatch count and final result oracle.

Each JSON sample includes the exact case, artifact identity, preparation mode, synthetic delay, iteration, and nanosecond durations. Synthetic waits are labelled and kept separate from runtime overhead.

The harness does not claim to measure arbitrary internal Python phases. A later runtime observation API is justified only if these coarse measurements cannot distinguish the candidate policies.

### Reproduce the single-Run study

```sh
# Human-readable demo; override PYSOLATE_PHASE_* environment variables as needed.
./demos/06-semantic-phases.sh

# Raw JSON Lines for analysis.
go run ./cmd/pysolate-phase-bench \
  -guest dist/pysolate.wasm \
  -case all \
  -iterations 3 \
  -preparation copy \
  -tool-delay 50ms \
  -park-delay 200ms > phase-samples.jsonl
```

`copy` is portable. `cow` requests the Linux-only COW preparation path and fails rather than silently falling back. `fresh` measures attempts without a prepared image. The tool and park delays are controlled experiment inputs.

### Preliminary evidence

The checked-in pilot data under `docs/performance-data/semantic-phases/` used the 34,684,853-byte artifact `d75b6f9c…3729`, macOS arm64, copy preparation, and three samples per case. These are descriptive medians, not SLOs:

- With no synthetic delay, read-then-finish took **10.94 ms**, the two-call chain **8.24 ms**, and park/re-admit used **6.95 ms** for the first attempt plus **6.90 ms** for re-admission. The read dispatched once across both attempts, confirming replay rather than redispatch.
- With 50 ms per Host call, read-then-finish took **64.52 ms** including **52.03 ms** measured in the tool. The two-call chain took **135.92 ms** including **104.07 ms** in tools.
- With the same 50 ms tool delay and a controlled 200 ms parked interval, park/re-admit used **72.80 ms** for the first attempt and **30.91 ms** for re-admission. The parked interval does not retain a live Guest.
- The local NumPy case took **3.44–3.45 s**. It is a useful negative control: import/computation dominates these millisecond lifecycle costs, so park policy should not target this shape without another explicit wait.

The pilot establishes measurement separation and workload correctness. It does not establish a population-level scheduling gain; that requires Linux memory measurements and controlled competing arrivals after a decision boundary is found.

### Live-I/O slot reuse pilot

`ExternalIO` is an explicit Tool scheduling declaration. During that Host callback, the Executor releases only the running slot. The Wasm instance, Python stack, and Guest memory remain resident, so continuation does not reconstruct or replay the program. Once the callback returns, the attempt joins the ready-continuation queue and must reacquire a running slot before Python resumes. `MaxResident` and `MaxInflightTools` independently bound retained Guests and concurrent external callbacks.

The checked-in five-sample pilot under `docs/performance-data/live-wait/` used the same `d75b6f9c…3729` artifact, macOS arm64, copy preparation, two fixed Runs, one running slot, two resident slots, two external-tool slots, no private heap fixture, and a controlled 100 ms Host delay. Setup was excluded from the compared phase:

- Inline scheduling serialized the Host waits: median first park batch **238.16 ms**, with peak Host concurrency **1**.
- `ExternalIO` overlapped the two Host waits: median first park batch **125.91 ms**, with peak Host concurrency **2**.
- The controlled batch duration fell by **47.1%**. This demonstrates capacity reuse for an I/O-heavy shape; it is not a general throughput or production-latency claim.

The later approval boundary still performs the existing durable park and replay. Resume timings are recorded but are not attributed to live-I/O scheduling because no live Tool callback occurs in that phase. macOS does not expose the Linux `/proc` RSS measurement used by this harness, so this pilot makes no memory claim. Run `./demos/07-live-io.sh` for the side-by-side demonstration.

The scheduling declaration is intentionally absent from the durable Tool snapshot: it may be tuned without invalidating Runs, while `Version` and `Recovery` remain semantic compatibility boundaries. A crash after an external response but before journal completion retains the existing recovery semantics; yielding a slot does not make an unsafe operation retryable.

## Cost model

For one waiting Run, retaining a Guest has an approximate memory-area cost:

```text
resident_memory_area = reclaimable_private_bytes * remaining_wait_time
```

Releasing it adds:

```text
release_cost = journal + teardown + reconstruction + replay + page_faults
```

A release decision also depends on active-slot pressure and CPU pressure. A long wait with a small Guest may not repay reconstruction. A large waiting Guest may be worth releasing when memory limits admission, while the same reconstruction may be harmful when CPU is saturated.

Tool estimates should keep queue time separate from provider service time. Process-local bounded observations keyed by canonical tool identity, provider version, cost class, and payload-size bucket are sufficient for an initial study. Successful latency samples, errors, timeouts, and cancellations must remain distinguishable; an arithmetic mean alone is not a scheduling contract.

## Legal actions before scoring

Optimization estimates never decide effect safety. Before comparing costs, filter actions through hard rules:

- an explicit durable wait may park;
- completed recorded reads may replay;
- unresolved retry-safe, idempotent, lookup, manual, and wait operations follow their declared recovery protocol;
- an unknown external write cannot be redispatched merely because memory is scarce;
- arbitrary Python stack suspension is unsupported;
- black-box or low-confidence code remains eligible for ordinary bounded FIFO execution, not semantic prediction.

## Candidate policy order

If single-Run phase data supports further work, compare policies in this order:

1. fixed concurrency/FIFO with inline Host calls;
2. explicit live-I/O slot release plus result-ready continuation, with a bounded admission burst;
3. explicit durable park and re-admission;
4. dependency-aware ordering supplied by the Harness;
5. safe pressure eviction only if measured memory pressure and reconstruction costs justify it.

Do not collapse these into one opaque score initially. Completion priority and eviction have different safety and cost boundaries.

## Measurement ladder

Controlled tool service delays: `0`, `5`, `50`, `200`, and `1000` ms. These are experiment points, not claims about production APIs.

Initial private-state sizes: approximately `0`, `8`, and `64` MiB where the case naturally permits it. Run one attempt at a time first. Later population models may consume the measured phase distributions without claiming that synthetic arrivals are real users.

Report:

- phase and end-to-end durations;
- reconstruction and replay durations;
- tool dispatches and replayed calls;
- CPU time where the platform provides it;
- process RSS and, in a controlled Linux job, cgroup memory;
- failures, cancellations, timeouts, and incomplete Runs;
- artifact hash, source revision, machine envelope, and sample count.

## Advancement criteria

Continue to a population scheduler only if common cases show a material decision boundary, such as:

- ready continuations release meaningful residency sooner than FIFO;
- park/re-admit repays reconstruction under realistic wait and private-memory conditions;
- provider concurrency or queueing, rather than local execution, is the actual bottleneck;
- simple admission and continuation priority do not already capture the observed gain.

If local tools, workspace edits, and ordinary computation show no net benefit, keep them on the direct path. Negative results are a valid outcome and should remove policy complexity rather than motivate unusual fixtures.
