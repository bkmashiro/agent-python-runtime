# Megagoal: strengthen fast, deterministic and resource-aware execution

Status: active. This roadmap is the single execution pointer for the approved goal.

## Desired state

Keep Pysolate's small execution model while strengthening its main line: isolated computation, Host operations, waits, deterministic execution, resource scheduling and replay. Improve end-to-end latency, CPU work, allocations and memory cost. Existing functions are not a ceiling: a small new capability may be justified by a concrete correctness, recovery or resource-lifecycle need, as well as by eliminating measured work. Deliver working improvements and comparable before/after evidence. Do not substitute a benchmark report or new business application for implementation. If no candidate produces a defensible net gain, report that result rather than inventing a win or retaining speculative complexity.

## Non-goals

- No new business application, Agent/Hermes integration, shared exploration tree, cross-Run batching, general workflow/object system, distributed scheduler or speculative policy framework. A bounded in-process executor for existing Run/park/resume semantics is a main-line candidate, not prohibited by this rule.
- Do not invent features to meet a count, but do not restrict work to local tuning. Optimization candidates need measurable net benefit. Core capability extensions need a real caller, a precise semantic/resource gap, focused correctness evidence and explicit added costs; they need not make every individual request faster.
- Durable combinations with PLM, prefix or prepared/COW are not established. A bounded composition may be investigated where it addresses a real main-line cost. Prove the supported case before exposing it; do not silently enable all combinations or attempt arbitrary interpreter-stack checkpoints.
- No resurrection of the retired runtime, pass catalogs, proof records or policy frameworks.
- No speedup obtained by weakening isolation, memory/message bounds, cancellation, tool declarations, original-position result/error delivery, or durable persistence ordering.

## Baseline

- Repository: `agent-python-runtime`; work on `refactor/execution-core`. Recover live status before editing and preserve unrelated work.
- Starting implementation: `ae071608`, minimal core, Guest and durable implementation totaling 2,883 physical source lines. This is a size reference, not a rigid line quota.
- `make check`, targeted PLM/prefix and Store/journal race tests, Linux COW, process-kill recovery and VM hard-stop recovery have passed. A full durable race invocation exceeded its initial execution-time budget; do not describe it as a full race pass.
- NumPy Guest exists at `dist/pysolate.wasm`; its legacy/Generator static-link ABI separation has real regressions. Preserve those fixes.
- There is no controlled before/after performance baseline for this new core yet. Historical measurements are not its performance evidence.
- Machine-specific build/VM notes are local in `.hermes/plans/performance-environment.md`; verify resources live before using them. They are not a second roadmap. The existing Guest is 32,919,442 bytes with SHA-256 `1050ae5118f531c27075a8885954c6bb2bcde911bffdef76850261486958e8a7`. Keep a separate baseline artifact if Guest sources change.

## Workloads and boundaries

Use bounded workloads through the real public execution APIs, reusing existing correctness cases:

1. Short Python computations and representative NumPy computation.
2. Small and larger tool payloads, repeated tool calls, and independent versus dependent reads.
3. Complete-source PLM and source-prefix input with explicitly recorded arrival schedules. Synthetic delays and schedules must be labelled as such.
4. Durable live calls, completed-history replay, approval park/resume, and increasing history lengths within existing limits.

Measure cold creation separately from repeated execution on an existing Runner. Include preparation cost and its amortization, data conversion, tool waiting, and required cleanup. Distinguish real computation from injected I/O delay. Preserve results, errors and relevant dispatch counts as correctness oracles.

## Ordered lanes

### 1. Locate the dominant costs

Create the smallest runnable measurement driver. Measure construction/compilation, initialization, instantiation or image mapping, Python work, Host bridge, source analysis and cleanup. For durable, separate SQLite work from Guest reconstruction and replay. Use temporary profiling and counters where needed; do not add production telemetry.

Completion: reproducible baseline samples and a short ranking of avoidable costs. Every candidate states old work removed, added work, expected end-to-end benefit and semantic risk. Preserve the baseline code and artifact separately before changing the Guest.

### 2. Improve startup and repeated execution

Attack the largest measured costs in fresh, prepared-copy and Linux COW paths. Consider repeated initialization, unnecessary copying, invariant work and resource lifetime. Compilation reuse across the relevant lifetime is a candidate, not a pre-authorized new cache subsystem.

Completion: keep, revise or revert each tested candidate based on comparable end-to-end measurements and fresh-state/isolation tests. Report cold and warm trade-offs separately.

### 3. Improve tool and source-scheduling overhead

Profile actual boundary calls, JSON conversion, buffering/copies, PLM transformation, prefix processing and unsupported-case fallback. Remove repeated work where demonstrated. Test zero/short waits as well as longer waits so backend delay does not hide runtime overhead.

Completion: successful candidates bypass the old work; result/error timing, permission checks, single consumption, fallback, cancellation and worker cleanup still pass. Do not retain an optimization solely because its isolated function is faster.

### 4. Improve durable record and recovery cost

Measure live journaling and replay at increasing history lengths. Investigate repeated queries, scans, conversions, compilation or initialization only where evidence points to them. Preserve intent-before-dispatch, outcome-before-Guest-delivery, WAL/FULL, stable operation keys, declaration/version checks and terminal-state ownership.

Completion: matched live/replay/resume measurements and relevant recovery regressions. Do not reduce sync durability or change persisted semantics to improve a timing result. A database-format change requiring migration is a decision boundary, not unattended scope.

### 5. Evaluate main-line capability gaps

Deterministic computation and basic replay already exist; do not rebuild them. First identify what prevents an existing logical Run from being managed efficiently across computation, Host work, persisted waiting and recovery.

A preferred candidate is a small in-process executor for admission, bounded active attempts, persisted park, and re-admission after an explicit decision/event or supported deadline. Start with fixed bounds and a simple queue, not prediction, priorities, arbitrary preemption or a policy registry. A parked Run should release its Guest and active-attempt capacity; a still-live Guest blocked in a Host call remains resident and must not disappear from accounting. Cancellation does not roll back external effects, and retries remain governed by tool declarations.

Another candidate is reducing the cost of deterministic reconstruction by composing an existing preparation mechanism with replay. Treat it as a cheap semantic feasibility probe first: initialized images can contain RNG/clock-dependent state; PLM preparation must not repeat historical calls or move external writes. Physical scheduling may vary, while each Run's program-visible observations, errors and required order remain stable. Completion-order-sensitive program choices must be recorded, constrained or rejected explicitly.

These are candidate directions, not a mandate to ship both. Implement a supported gap when the existing APIs and a bounded contract suffice. If it needs a new broad public model, migration or materially stronger semantics, record the trade-off for the user and continue independent optimization work rather than building around the ambiguity.

Completion: either a useful, exercised main-line extension with measured overhead/resource behavior, or a documented reason to defer it. For an executor, compare with direct execution under simple fixed concurrency plus explicit park/resume; account for queueing, cancellation, waiting resources and recovery work.

### 6. Validate the integrated result under resource limits

Run the resulting paths under a fixed Linux resource envelope. Begin with concurrency 1, 2 and 4; try higher only when measured headroom permits. These are initial experimental choices, not capacity claims. Measure completed throughput, queueing/end-to-end latency, CPU, process/cgroup memory, and resource release. Distinguish the reusable image, active Guest attempts and Host-side waits; do not invent a ready-slot pool to create a metric.

Completion: a compact final report and raw machine-readable samples comparing the starting and final versions. Count failures, cancellations and timeouts. Do not claim high-percentile latency from inadequate samples or equate PSS reduction with capacity gain. Explain material regressions or trade-offs and remove candidates whose net benefit is within noise or negative.

## Execution rules

- Follow Ponytail. Prefer removing work and simplifying ownership over adding knobs, interfaces, caches or controllers.
- Start with the largest unblocked measured cost. The lane order may change with evidence; keep one current pointer.
- Make one primary performance change at a time. Run focused oracles and matched measurements, then keep/revise/revert. Finish with the affected broader checks and real Linux validation for platform-dependent changes.
- Subagents are authorized for independent profiling, implementation or review. The controller owns architecture, integration and end-to-end claims. Maintain one writer per mutable worktree; use isolated worktrees for concurrent implementation. Freeze shared interfaces first and give each child bounded ownership and a concrete verification target. Do not share benchmark CPU/memory with concurrent compilation or other measurements.
- Keep detailed samples and profiles outside production code. Do not log credentials or sensitive tool payloads. Avoid large committed datasets or artifacts.
- Signed commits and pushes to the existing refactor branch are allowed after validation. Do not automatically merge main, publish a release or deploy a service.

## Resource and authority envelope

- Reuse the local Mac and existing 2-vCPU/2-GiB Linux VM for initial validation. Keep baseline/candidate comparisons on the same machine and limits.
- Existing ICL Slurm resources may be used for necessary Linux builds or measurements if the local environment is unsuitable. Do not run heavy work on login nodes, and do not combine incomparable measurements from different machines.
- Do not create new paid resources, use Docker, relocate Docker data, install a large new toolchain locally, or manually trigger GitHub Actions.
- Prefer existing Guest build inputs. Keep new local benchmark/profile artifacts within a 1-GiB planning budget; this is an engineering budget. Stop for a decision rather than expanding disk use silently.
- Shut down temporary VMs and release compute allocations after use. Preserve unrelated files and historical source revisions.

## Stop and delivery

Continue through meaningful optimization and verification, not merely until the first report or commit. Stop when the main measured avoidable costs have been addressed and further candidates are low-value/within noise, or when progress requires a product, semantic, permission, storage or resource decision. Do not keep adding features to fill the night.

Deliver: implemented changes, exact versions/artifacts, concise before/after results with measurement boundaries, costs removed and costs added, correctness evidence, accepted limitations and remaining bottlenecks. No guaranteed percentage speedup is set before measurement.

## Execution pointer

Current: finish validation of seeded deterministic preparation, then optimize remaining replay/bridge overhead and implement a bounded Run executor.
Next: use measured replay residuals and the executor contract; preserve the main-line seeded-image isolation and COW park regression.
Blocked: none. Baseline and Guest are preserved. Dominant fresh cost was CPython initialization; matched seeded-COW replay is measured in docs/performance-results.md. The mapping-lifetime defect exposed by park has been fixed and reproduced successfully.
