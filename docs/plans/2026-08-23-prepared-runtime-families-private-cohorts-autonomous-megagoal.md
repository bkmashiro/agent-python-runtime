# Prepared Runtime Families and Private Cohorts Autonomous Mega-Goal

> **For Hermes:** Execute this plan continuously in `/Users/yuzhe/projects/agent-python-runtime` only after Yuzhe starts it with `/goal`. Read the whole file and live Git/code first. Prefer the thinnest executable vertical slice, use RED -> GREEN -> refactor for behavior, keep the Runtime free of scheduling policy, make coherent signed commits, push after verified phases, and continue without waiting after ordinary checkpoints. Stop only at a named architecture, resource, permission or safety gate below.

**Status:** Active since 2026-08-23. Gates P0-P3 passed; Phase 4 authority/workspace composition is active.

**Current execution pointer:** Phase 4, composing the sealed image with distinct Broker/Plan and private workspace branches through existing Host/subagent primitives.

**Goal:** Turn Pysolate's completed fixed prepared-NumPy/private-COW research prototype into a small reusable Host-owned prepared runtime family. One sealed package/data image may create many fresh single-use Python runners with different program source, Run identity, authority and private workspace while preserving mutation, failure and teardown isolation. Prove the product contract with deterministic acceptance fixtures on macOS and Linux. Do not invent a real evaluation dataset or reopen paper economics.

**Project:** `/Users/yuzhe/projects/agent-python-runtime`

**Prepared baseline:** `main` at `0eb79b59` after restoring the split Guest bootstrap script-test baseline; `origin/main` aligned and clean.

**Paper relationship:** This is an explicit post-paper product/research lane. The canonical paper source of truth at [`2026-08-16-effect-compiler-paper-evaluation-source-of-truth.md`](2026-08-16-effect-compiler-paper-evaluation-source-of-truth.md) remains frozen. This goal does not alter paper claims, rerun retained prepared-data economics, or add a natural-workload evaluation.

---

## Mission in one paragraph

Pysolate already proves that a Linux Host can prepare a `numpy-core` CPython/WASI image and one fixed 8 MiB ndarray once, then create fresh private-COW consumers that run different Python code without sharing later mutations. That path is deliberately hard-coded into research probes and has no reusable Host API. This goal extracts the smallest product primitive: a sealed **prepared family** creates independent single-use `engine.Runner`s. A Host or the existing `subagent.Orchestrator` may coordinate those runners, fork private workspaces and select one sealed result. The Runtime never becomes a scheduler and the Guest never receives backing handles or authority from physical sharing.

## User intent

Yuzhe is treating Pysolate as a long-term project. The immediate priority is to make an existing distinctive mechanism usable, not to create another benchmark, Code Mode clone, workflow engine or general sandbox. Local macOS is suitable for code, compilation and portable correctness. Exact private-COW behavior, real `numpy-core` Guest execution and Linux race/lifecycle gates must run through `ssh shell2 -> gpu31` using the approved shared toolchain and cache roots.

## Value order

Prefer, in order:

1. fresh per-Run Python, authority and workspace isolation;
2. a small understandable Host API over the already-proved mechanism;
3. one physical package/data preparation serving independent single-use consumers;
4. explicit lifecycle, cancellation, resource bounds and teardown;
5. composition with existing workspace/subagent primitives instead of a new scheduler;
6. portable semantic fallback and Linux private-COW acceleration behind one contract;
7. honest correctness and resource evidence without fabricated workload claims;
8. deletion or consolidation of duplicate product-path code after parity is proved.

Performance is not a completion condition. A reusable mechanism with neutral economics is still useful. A mechanism that only works for the exact historical fixture is not productized and must stop at its gate.

---

## Verified authoring-session baseline

### Repository and tests

- `main` and `origin/main` were aligned before preparation.
- `go test ./... -count=1` passes locally.
- `go vet ./...` passes locally.
- Guest bootstrap unit tests pass locally.
- All 105 script tests pass after commit `0eb79b59` registered the split Guest bootstrap package before resolving relative imports. This was a pre-existing baseline regression exposed during plan preparation, not Cohort implementation.
- A bounded exact-HEAD Linux staging run passed:

```text
runtime/prepareddataset
runtime/preparedregion
runtime/workspace
runtime/subagent
```

### Linux path

The live path was verified on 2026-08-23:

```text
local macOS arm64
  -> ssh shell2
  -> ssh gpu31
  -> Linux x86_64
```

Use these approved shared roots for Host testing:

```text
GOROOT          /vol/bitbucket/ys25/pysolate/toolchains/go      # Go 1.25.0
GOPATH          /vol/bitbucket/ys25/pysolate/toolchains/go-work
GOMODCACHE      /vol/bitbucket/ys25/pysolate/toolchains/go-mod
GOCACHE         /vol/bitbucket/ys25/pysolate/toolchains/go-build
XDG_CONFIG_HOME /vol/bitbucket/ys25/pysolate/config
stage           /vol/bitbucket/ys25/pysolate/stage
```

Always set `GOTELEMETRY=off`. Do not use the default gpu31 home Go/module/config paths: the account home quota is exhausted and the system Go is only 1.22.2. The shared `/vol/bitbucket` filesystem is highly utilized, so retain no run-scoped source trees, binaries or logs after verified retrieval. Existing source/toolchain caches may remain.

The existing [`scripts/build-guest-workstation.sh`](../../scripts/build-guest-workstation.sh) already performs exact clean-HEAD `base`, `attrs-770` and `numpy-core` Guest builds on gpu31, retrieves a verified evidence bundle and cleans run-scoped remote paths. Reuse it. Do not create a second Guest build pipeline.

---

## Current implementation map

| Concern | Already implemented | Product gap owned by this goal |
|---|---|---|
| generic Run | `engine.Runner`, Wazero `Engine.Run`, fresh module lifecycle, Host `RunConfig` | no prepared-family runner factory |
| package preparation | `PrepareNumpyCOWShard` imports NumPy into a sealed Linux image | method is Wazero-specific, research-named and tied to one engine |
| prepared data | `DeriveNumpyI64COWDataset` derives one `<i8 [1024,1024]` full image | body size, dtype, shape, variable name and renderer are hard-coded |
| numeric validation | `runtime/numpycodec` validates bounded C-contiguous numeric dtype/shape/body up to 8 MiB | prepared input cannot reuse a small Host-owned layout contract without result-reuse-specific bindings |
| COW consumers | Linux `cowPreparedRuntime.prepare` creates single-use private mappings with mutation isolation | no stable owner that lends the same sealed image to independently configured runners |
| portable path | ordinary fresh Run and NumPy private-copy materialization already exist | no one semantic prepared-input API with an honest non-COW reference mode |
| authority | Host `RunConfig`, capability Plan/grants, Broker, attenuation and per-Run receipts exist | a prepared family must not union or inherit authority from another consumer |
| workspace | private `Attempt`, portable `Branch`/`Root`, Capsule, expected-base selection and discard exist | prepared consumer runners cannot yet bind different private branches through one shared family |
| subagents | `FreshRunnerExecutor` creates and closes one runner per child; `Orchestrator` owns private branches, selection and cleanup | no prepared-family-backed `RunnerFactory`; do not add a second orchestrator |
| evidence | prepared/COW state, lifecycle probes and retained research evidence exist | product receipts need body-free family/input/invocation/disposition identities, not another evidence framework |
| CLI | `apyrun` executes one ordinary request | no product requirement for a new scheduler CLI; a small example may be added after the Go API is stable |

### Existing research code is historical evidence

[`authority-preserving-prepared-data-contract-v1.md`](../research/authority-preserving-prepared-data-contract-v1.md) and its probes prove speculative `np.load`, exact dynamic claim and one fixed ndarray schedule. They explicitly say the mechanism is not a production API. Do not silently loosen or repurpose that contract.

The product path starts from a Host-provided already-authorized prepared value. It does not require prefix speculation, `np.load` source recognition, a staged filesystem read or a dynamic logical claim. Reuse lower-level codec/COW owners where appropriate, but keep research source-effect semantics intact.

### Pre-implementation gaps confirmed by independent review

The current code does **not** yet provide four product seams that later phases assume:

1. `runtime_prepare` accepts UTF-8 Python source only. There is no binary Host-to-Guest preparation ABI, and the research COW path embeds the 8 MiB body as base64 source.
2. `engine.Runner` is reusable and accepts public `trustedPrepare`; `subagent.RunnerFactory` does not carry a complete per-consumer `RunConfig`, grants or `InvocationRef`.
3. Ordinary `Engine.Close` has no prepared-family `new -> running -> terminal -> closed` admission contract.
4. Existing profile binding does not by itself identify every image-affecting property, and target-Guest dtype reconstruction has not been qualified beyond the fixed historical fixture.

Phase 0 must close these as written contracts and RED tests before product implementation. The preferred minimal binary preparation shape to test is:

```text
Host validates and copies bounded descriptor + body
  -> instantiate authority-free canonical Guest
  -> Guest allocates bounded staging memory
  -> Host writes descriptor and raw body into that private memory
  -> one dedicated Guest preparation export consumes both exactly once
  -> Guest constructs the private ndarray in the prepared namespace
  -> Host and Guest release staging, verify terminal preparation state
  -> only then seal the canonical image
```

The exact export name and ownership mechanics are a Phase 0 outcome, not a preselected API. It must use no workspace, Broker, Host path, JSON body, filesystem handoff or network authority. Adding the export requires target-Guest ABI/version, export-manifest and qualification updates plus an exact `numpy-core` rebuild. If a smaller existing binary ingress can satisfy the same ownership and teardown rules, prefer it and document why.

The preferred consumer boundary is a narrow family-owned single-use runner adapter. Construction receives a frozen copy of Host `RunConfig`, workspace binding and invocation identity; `Run` accepts only the ordinary request and internally supplies the sealed preparation. Do not return the raw prepared Engine, retain caller-owned maps/slices or let `ChildProgram.TrustedPrepare` reach this runner.

---

## Target architecture

### Runtime primitive

The conceptual API is a **PreparedFamily**. Names may change in Phase 0 if live code proves a smaller fit.

```go
prepared, err := host.Prepare(ctx, PreparedInput{
    Name:       "dataset",
    Codec:      "numpy_ndarray_c_v1",
    DType:      "<i8",
    Shape:      []uint64{1024, 1024},
    Body:       body,
    MaxConsumers: 4,
})

runnerA, err := prepared.NewRunner(ctx, hostConfigA, workspaceA)
runnerB, err := prepared.NewRunner(ctx, hostConfigB, workspaceB)

resultA, err := runnerA.Run(ctx, requestA, "")
resultB, err := runnerB.Run(ctx, requestB, "")
```

This is illustrative, not a requirement to expose body bytes in one struct or add these exact methods.

### Ownership

```text
Host / existing Harness or Orchestrator
  owns program list, concurrency, scheduling, Plan/grants,
  workspace fork/select/discard, cancellation, cohort/member manifest
  and the join from each member to its Run and terminal disposition

Prepared family
  owns sealed package/data image, input identity,
  clone admission, active-consumer bound and close lifecycle

Single-use runner
  owns fresh Run identity, Broker, workspace lease,
  private memory mapping, response and terminal disposition

Guest
  sees a normal private Python value such as `dataset`
  and never sees memfd, map, pointer, FD, Host path or sibling identity
```

### Required separation

- Preparing the canonical image runs with no workspace or Broker authority.
- Sharing a physical image never shares a Broker, capability grant, workspace lease, Python continuation, mutable page, `/tmp`, stdout buffer or response body.
- The product family contract is Host-authored and immutable, but it is not a reused `PreparedDataContract`: that historical contract is Run-scoped by Run identity, privacy partition, budget reservation and plan epoch. Every family consumer still receives its own Run binding and terminal disposition.
- The multi-MiB body must not travel in `RunRequest`, Broker JSON, receipts, evidence, the public family handle or the promoted trusted-preparation source. A bounded Host-internal preparation transfer may fill private Guest memory before the image is sealed, after which its staging body must be released.
- Sharing one Host Wazero runtime and compiled module is allowed if each consumer receives a distinct module instance, private memory mapping, module configuration, Broker and workspace lease; do not require a separate Wazero runtime merely to simulate isolation already enforced below that layer.
- Each consumer validates its own Run request, profile/import closure, limits and authority before execution.
- A prepared-family runner rejects any caller-supplied non-empty `trustedPrepare`; only the Host-owned family may install its sealed preparation. The legacy `engine.Runner.Run` parameter must not become a preparation-injection escape hatch.
- The Host chooses concurrency and which workspace root, if any, is selected. `PreparedFamily` does not expose `RunMany`, queues, retries or selection policy in v1.
- The existing `subagent.Orchestrator` is the preferred composition owner for bounded multi-child execution. A thin `RunnerFactory` adapter is acceptable; a second orchestrator is not.

---

## Frozen v1 scope

### Supported prepared value

- exactly one Host-provided numeric C-contiguous ndarray value per family;
- existing `numpy-core` profile and exact verified artifact/import closure;
- one Host-validated public Python identifier, initially `dataset` in acceptance tests;
- existing allowlisted non-object numeric dtypes from `numpycodec` where the Guest NumPy build supports exact reconstruction;
- bounded rank/dimensions and at most the existing 8 MiB body limit;
- exact body digest and size/layout validation before preparation;
- private mutation allowed inside a consumer, with no Host or sibling mutation;
- one family has one sealed input identity and a finite Host-owned consumer/active-run bound.

### Portable semantics

- Linux may use full-image private COW.
- macOS must exercise the same logical prepared-value contract through a bounded private-copy/fresh reference path or return an explicit unsupported physical mode while portable contract tests remain executable.
- Evidence must say `private_cow`, `private_copy` or `ordinary_fresh`; it may not imply COW on Darwin.
- The all-off default remains ordinary fresh execution.

### Deterministic acceptance fixtures

Use tiny generated values only as mechanism fixtures, not as a real dataset or evaluation:

```text
<i8  shape [2,3]       values 0..5
<i4  shape [256,256]   deterministic integer formula
|u1  shape [1024,1024] deterministic bytes, exactly 1 MiB
```

Acceptance programs must be visibly different and include:

- reduction;
- indexed transformation;
- private mutation followed by an oracle;
- normal Python exception;
- cancellation or timeout;
- workspace write after Phase 4.

Do not call these fixtures natural workloads, benchmark datasets, agent evaluations or evidence of broad economics.

---

## Explicit non-goals

Do not implement or claim in this goal:

- a real/natural evaluation dataset or a synthetic dataset presented as real;
- a new paper benchmark, timing campaign, economic preregistration or paper/slides change;
- streaming source speculation or generic `np.load` optimization;
- arbitrary Python object, heap, frame, pointer or module transfer;
- shared mutable memory or a reused interpreter;
- pandas, Arrow, Parquet, SciPy, tokenizer/model state or multiple prepared values;
- generic page allocator, object store, durable cache or cross-process image registry;
- multi-MiB body transport through JSON, evidence, receipts, model source or promoted `trustedPrepare` source;
- runtime `pip`, package solving or arbitrary user build recipes;
- public `RunMany`, scheduler, queue, retry engine or workflow language inside Runtime;
- automatic workspace merge, virtual Git, CRDT or last-writer-wins publication;
- external writes, EffectIntent, reconciliation, messaging, Git push or cloud adapters;
- distributed execution, Kubernetes, Docker, paid cloud, production deployment or package release;
- Cloudflare Code Mode parity, MCP marketplace or generic HTTP/network access;
- manual GitHub Actions runs.

---

## Autonomy and work allocation

### Main controller owns

- final API and authority boundary;
- all edits that touch Wazero engine lifecycle or cross-package contracts;
- integration of prepared image, runner, workspace and subagent seams;
- gpu31 staging, Guest builds and real Linux execution;
- signed commits, pushes, roadmap updates and final claims.

### Delegate only bounded work

Good independent tasks:

- read-only call-chain archaeology;
- one small package's RED tests after the API is frozen;
- workstation-script contract tests in files disjoint from runtime implementation;
- post-fix review of one exact diff/package;
- evidence/document consistency review.

Do not let multiple agents write the shared worktree. Do not delegate architecture synthesis, remote resource operation, final integration or claims. Integrate file-disjoint worker changes serially and inspect every diff.

### Work waves

```text
Wave 0 main only
  contract, package ownership, remote test path, RED fixtures

Wave 1 parallel only where file-disjoint
  A: numeric layout/helper tests
  B: workstation host-test script contract tests
  main: prepared family API RED tests

Wave 2 main
  generic Linux image derivation and lifecycle
  then independent engine review

Wave 3 main
  prepared family runner factory and portable reference mode
  then independent API/authority review

Wave 4 main
  workspace/subagent composition
  then independent leakage/cancellation review

Wave 5 main
  docs, example, full local/Linux closeout and simplification
```

---

## Phase 0: freeze product contract and repeatable Linux host gate

**Promise:** Start from a green exact baseline and establish one reusable remote test path before changing the COW lifecycle.

Tasks:

- [ ] Re-read this plan, live Git/status, current paper source of truth, prepared-data contract and recent prepared/COW commits.
- [ ] Confirm the current call chain from Host declaration/engine preparation through COW clone, `_initialize`, baseline restore, final Run and teardown.
- [ ] Freeze the product prepared-input identity fields, variable-name policy, family lifecycle and copy/COW disposition vocabulary.
- [ ] Freeze the data-plane rule: descriptor/identity may be encoded, but the body uses a bounded Host-internal preparation transfer and is released after seal; do not promote the historical base64-in-trusted-source probe into the product API.
- [ ] Specify per-consumer bindings and terminal dispositions separately from the shared family identity. Do not reuse a Run-scoped `PreparedDataContract` across consumers.
- [ ] Write a focused binary preparation ABI note and RED tests covering allocation/write/consume/release, short write, oversized body, descriptor/body mismatch, repeat consumption, preparation error, cancellation and cleanup. Include required Guest export/ABI/version/manifest/qualification changes and the `_initialize`/`runtime_init` ordering for canonical, copy and COW lanes.
- [ ] Freeze the family-backed runner adapter: the construction inputs, where `engine.WithInvocationRef` is installed, how per-consumer `RunConfig`/Plan/grants/Broker/workspace are created, and how public non-empty `trustedPrepare` is made unrepresentable or rejected before Guest execution. Do not treat `Descriptor.ChildPlanSHA256` as a Plan.
- [ ] Freeze `family: open -> closing -> closed` and `consumer: new -> running -> terminal -> closed` transitions, including duplicate Run, close-before-run, close-during-run, whether close waits or rejects, which failures consume total quota, and how active/total counters are released.
- [ ] Define one family-image compatibility identity over exact artifact/manifest, allowed/available/qualified imports, deterministic profile, memory/image-affecting config, preparation ABI/version, dtype/layout/name and body digest. Do not overload `ExecutionProfileBindingSHA256` unless the extended identity remains valid for all existing consumers.
- [ ] Freeze macOS semantics before implementation: prefer the same bounded binary preparation ABI with `private_copy`; if unavailable, expose an explicit unsupported physical disposition while keeping descriptor/lifecycle contract tests portable. The old RunRequest-base64 lane is not a product fallback.
- [ ] Freeze a small v1 target-Guest dtype allowlist from real `numpy-core` qualification (`np.dtype(...).str`, endianness, shape and exact reconstruction). Host arithmetic support alone is insufficient.
- [ ] Deep-copy/freeze every mutable field accepted at family/runner construction, especially `RunConfig.CapabilityGrants`, profile/import collections and mechanism/config values; RED-test caller mutation after construction.
- [ ] Assign the minimal family/member terminal record owner and validator. Prefer a small product contract adjacent to the family lifecycle and reuse existing Run/workspace identities; do not expand capability receipts or build another evidence store.
- [ ] Decide the narrow package owner. Prefer one lifecycle-owning package or an extension to `runtime/engine`; do not create parallel `prepared`, `cohort`, `family` and `scheduler` packages.
- [ ] RED-test the public Host contract before implementation: unknown codec/dtype, bad shape/size/digest/name, body mutation after seal, profile/artifact mismatch, zero/over-bound consumers, close with active runners and use after close.
- [ ] Add one minimal `scripts/test-host-workstation.sh` plus tested internal worker only if no existing script can express exact candidate Host tests. Freeze its contract first: exact candidate identity (`HEAD` plus clean tree, or base commit plus deterministic patch digest), allowlisted package/test command, shared environment roots, bounded JSON/text result manifest, local verification and unconditional run-directory cleanup. It must stage the current candidate, retrieve bounded logs and never turn into a general remote command runner.
- [ ] Keep `build-guest-workstation.sh` unchanged unless an exact missing Guest-build capability is proved.
- [ ] Run the new host-test path against current focused packages on gpu31 before runtime edits.

**Gate P0:** Green local baseline and one clean bounded gpu31 focused Host run. Binary preparation ABI/ownership and initialization ordering, family/consumer state machines, adapter/authority construction, compatibility identity, macOS disposition, target-Guest dtype allowlist and terminal-record owner are each written and backed by RED tests. `RunConfig` mutable authority is frozen at construction. The design adds no scheduler, generic cache or paper mechanism. Until every item passes, remain in Phase 0 and do not edit the COW product lifecycle. If candidate-source staging cannot be made exact without a broad deployment system, use coherent local commits as remote test inputs instead of expanding scope.

## Phase 1: Host-owned prepared ndarray contract and portable reference

**Promise:** Define one reusable value contract independently of Linux COW.

Likely seams, subject to Phase 0:

```text
runtime/numpycodec
runtime/engine
runtime/engine/wazero
new lifecycle owner only if justified by P0
```

Tasks:

- [x] Extract or expose the smallest shared ndarray layout validation from `numpycodec` without changing historical descriptor JSON or duplicating dtype/shape arithmetic.
- [x] Seal one Host-owned prepared input from immutable copied bytes, exact layout, variable name, artifact/profile/import identity, consumer bounds and body digest.
- [x] Reject Guest/model-produced attempts to mint or widen this contract.
- [x] Implement a bounded private-copy/fresh reference runner that makes the prepared value available under the same Guest-visible name and produces the ordinary response contract.
- [x] Keep the reference body out of Run/Broker/evidence JSON. A temporary test-only source renderer may remain only as an oracle while the product path receives an explicit bounded Host transfer and removes staging on every terminal path.
- [x] Prove body mutation after Host seal cannot affect the sealed input.
- [x] Prove consumer A mutation cannot affect the Host body or consumer B.
- [x] Prove error, timeout, cancellation and unconsumed-family close release all private state.
- [x] Preserve ordinary Run behavior and all-off default exactly.

**Gate P1:** All three deterministic layouts pass portable copy/reference oracles with different program source. One-field contract drift and all lifecycle misuse fail before Guest execution. No Linux/COW claim is needed yet.

## Phase 2: generic bounded Linux private-COW image

**Promise:** Replace the exact `<i8 [1024,1024]` research renderer with one Host-rendered, descriptor-bound v1 family while keeping full-image COW as the implementation.

Tasks:

- [x] RED-test at least three supported layout/body identities and two distinct bodies with the same layout.
- [x] Generalize the bounded trusted loader logic from validated Host layout/name, but move the body itself through a one-shot Host-internal preparation transfer rather than embedding multi-MiB base64 in the promoted trusted source. Never concatenate unvalidated model source into trusted preparation.
- [x] Keep `PreparedRegionTable` as its existing scalar one-Run claim table; do not repurpose its 256-byte payload as the family data plane or a cross-Run store.
- [x] Bind the sealed image to exact artifact, profile/import closure, layout, body digest, variable name and preparation implementation version.
- [x] Retain one immutable package parent, derive each data image from a fresh parent clone and never derive input B from input A's data image.
- [x] Create N=0/1/2/4 fresh private consumers from one data image.
- [x] Verify module-global, ndarray mutation, `/tmp`, stdout/stderr and response isolation.
- [x] Verify concurrent clone creation, child cancellation, failed instantiate/restore, close during active run and complete final unmap/FD cleanup.
- [x] Record body-free mapped/private-copy counters and family/consumer dispositions. Do not run or report a performance campaign.
- [x] Keep the historical fixed prepared-data probes passing or adapt them through an exact compatibility wrapper without changing retained evidence files.

**Gate P2:** On gpu31, more than one shape/dtype/body works through the same bounded implementation; all consumers are fresh and private; the promoted prepare source and all response/evidence documents are body-free; staging is released after seal; no per-consumer body copy is reported for the COW lane; copy-reference results match. If safe generalization requires a generic allocator, mutable shared handle, Wazero fork or Python heap transfer, stop and present a compute-only fixed-profile alternative rather than building it automatically.

## Phase 3: prepared family runner factory

**Promise:** Expose a reusable Host primitive without embedding orchestration policy.

Tasks:

- [x] Implement a sealed prepared-family lifecycle with explicit `Prepare`, single-use runner creation, finite total/active consumer bounds and `Close`.
- [x] Ensure every runner receives a new Run/Invocation identity, validation path, output buffers, cancellation scope and terminal disposition.
- [x] Ensure runner creation cannot widen the family's artifact/profile/import identity or reuse another consumer's RunConfig/Broker.
- [x] Wrap or narrow the legacy Runner surface so every prepared-family runner rejects caller-provided non-empty `trustedPrepare`; only the family-internal sealed preparation may be installed.
- [x] Review whether existing default-off `PreparedRuntime`/`MemoryCOW` mechanism selection is sufficient before adding any new flag.
- [x] Do not add `MechanismSet.Cohort`: cohort membership and scheduling belong to Host/Harness, while physical selection continues to use existing prepared/COW dispositions.
- [x] Provide explicit physical disposition: `private_cow`, `private_copy` or `ordinary_fresh`.
- [x] Make unsupported platform/mechanism selection fail explicitly or use the tested reference mode according to the frozen P0 contract.
- [x] Keep `engine.Runner` ordinary semantics and `RunRequest` untrusted shape unchanged unless a smaller reviewed extension is required.
- [x] Keep cohort/member selectors, shared input authority and family handles out of `RunRequest` and Guest inputs.
- [x] Add one focused example/test that runs visibly different programs over one family. Do not add a production scheduler CLI.
- [x] Run an independent API/lifecycle review before proceeding.

**Gate P3:** A caller can prepare once, create multiple ordinary single-use runners, close each runner independently and close the family after all consumers. Default-off ordinary execution remains unchanged. The API exposes no backing or scheduler handle.

## Phase 4: private workspace and authority composition

**Promise:** Consumers share only the sealed physical input, while their real Run authority and mutable outputs remain separate.

Architecture preference:

1. keep preparation authority-free;
2. lend the immutable image to independently configured runners;
3. bind each runner's module configuration to its own workspace branch and Broker/Plan;
4. let existing Host/subagent code seal, select or discard roots.

Tasks:

- [x] Spike the smallest safe image-owner/runner relationship. Prefer one immutable ref-counted image provider attached to independently configured runners over making workspace binding mutable inside a shared engine.
- [x] Prove artifact/profile/import/config drift rejects image attachment before Guest execution.
- [x] Bind two consumers to private branches of one expected base root and run different programs.
- [x] Prove file writes, temporary state, mutation, failure and cancellation remain branch-local.
- [x] Prove capability Plans/grants are independently constructed and never unioned because of shared preparation.
- [x] Seal successful branches to immutable roots; keep failed/cancelled branches private or discard them.
- [x] Select one root through existing `workspace.SelectRoot` or existing authority-aware `subagent.Orchestrator`; do not select inside the prepared family.
- [x] Add a thin prepared-family-backed `subagent.RunnerFactory` only if it composes the existing interfaces without duplicating orchestration.
- [x] Verify family close waits for or rejects active consumers deterministically and never invalidates an already sealed workspace root.
- [ ] Run independent authority/workspace leakage review.

**Gate P4:** On gpu31, two or more different programs use one prepared image with different Run identities, Plans and private branches. Consumer mutation/failure cannot affect siblings; only the Host-selected root becomes the chosen immutable result. If Wazero image sharing across independently configured runners requires shared mutable Engine policy, a broad Runtime rewrite or authority in the canonical prepare image, stop for Yuzhe's architecture decision. A compute-only prepared family remains a valid completed subset.

## Phase 5: deterministic product acceptance and documentation

**Promise:** Demonstrate the actual developer capability without inventing a dataset or evaluation claim.

Tasks:

- [x] Add one small checked-in acceptance manifest or Go fixture describing the three generated arrays and distinct programs. It is a correctness fixture, not a benchmark corpus.
- [ ] Execute copy/reference and Linux COW lanes against the same semantic oracles.
- [x] Cover N=0/1/2/4, success, Python error, timeout/cancellation, mutation and workspace selection.
- [x] Produce a compact body-free acceptance report with exact source/artifact/profile/input/family/invocation/root identities and terminal dispositions.
- [x] Document the Host API in README or one focused design page, including an ordinary fresh fallback example and platform limitations.
- [x] Update `docs/product-maturity-and-roadmap.md` with Current/Experimental/Deferred facts. Do not edit paper claims.
- [x] Mark real workload dogfood as **not entered**. It may begin only when a naturally occurring Pysolate consumer and meaningful inputs exist and Yuzhe approves that separate goal.
- [x] Remove only product-path duplication proved redundant. Keep historical evidence/probes required to verify earlier claims.

**Gate P5:** A new developer can understand and run the bounded prepared-family example. The report proves correctness/isolation only, uses no invented model/dataset claims and distinguishes portable copy from Linux COW.

## Phase 6: closeout, simplification and independent review

**Promise:** Leave a small maintainable product slice with honest platform evidence.

Tasks:

- [ ] Review every new exported type, mechanism flag, schema and package. Remove one-use wrappers and duplicate validators.
- [ ] Confirm the Runtime still contains no queue, retry, selection policy or general object/cache subsystem.
- [ ] Run focused race/leak tests for prepared family, Wazero COW, workspace and subagent composition.
- [ ] Run complete local gates.
- [ ] Build the exact clean-HEAD `numpy-core` Guest through the existing gpu31 workstation build path.
- [ ] Run named real-Guest and private-COW acceptance on gpu31 through clean run-scoped staging.
- [ ] Run independent engine, authority/workspace, platform-script and documentation reviews in small slices; fix findings and rerun proportional gates.
- [ ] Update this plan's execution pointer, checkboxes and completion log with exact commands/results and body-safe identities.
- [ ] Sign coherent commits, push, verify `HEAD == origin/main` and clean local/remote staging.

**Gate P6:** All admitted phases and final reviews pass, or an earlier named subset is closed with an explicit architecture no-go and no overstated claim.

---

## Verification ladder

### Per-slice local

```bash
cd /Users/yuzhe/projects/agent-python-runtime

git diff --check
go test <changed packages> -count=1
go test -race <concurrency/lifecycle packages> -count=1
go vet <changed packages>
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
```

### Shared contract or final local gate

```bash
go test ./... -count=1
go vet ./...
env -u AGENT_RUNTIME_GUEST go test -race ./runtime/engine/... ./runtime/workspace ./runtime/subagent -count=1
```

### Exact Guest build

```bash
scripts/build-guest-workstation.sh \
  --artifact-profile numpy-core \
  --gateway shell2 \
  --output /absolute/private/evidence/directory
```

The output directory must be absent or empty. Keep bulky bundles under `~/.hermes/evidence/pysolate/`, not the repository.

### Linux Host/real-Guest gate

Use the Phase 0 workstation test wrapper, or the exact reviewed manual equivalent, with:

```text
GOROOT=/vol/bitbucket/ys25/pysolate/toolchains/go
PATH=$GOROOT/bin:$PATH
GOPATH=/vol/bitbucket/ys25/pysolate/toolchains/go-work
GOMODCACHE=/vol/bitbucket/ys25/pysolate/toolchains/go-mod
GOCACHE=/vol/bitbucket/ys25/pysolate/toolchains/go-build
XDG_CONFIG_HOME=/vol/bitbucket/ys25/pysolate/config
GOTELEMETRY=off
```

Required final Linux gates include:

- focused/unit/race tests for changed packages;
- exact real `numpy-core` Guest acceptance;
- private-COW selected with no false Darwin claim;
- mutation/failure/cancellation/workspace isolation;
- active-run close and complete cleanup;
- ordinary fresh/all-off comparator.

Do not treat a skipped real-Guest test as PASS. Do not leave source trees, test binaries or raw response bodies on gpu31/shared staging.

---

## Mandatory architecture and safety stops

Stop and ask Yuzhe only when one is demonstrated:

1. the reusable input contract requires changing the frozen paper semantics or retained evidence;
2. supporting more than the fixed historical ndarray requires a generic allocator, broad Wazero fork, Python heap transfer or mutable shared memory;
3. sharing an immutable image across private workspaces requires mutable shared Engine authority or grant union;
4. a usable API requires a scheduler/workflow system inside Runtime rather than a runner factory;
5. the mechanism can only support the exact old body/shape and no honest bounded generalization passes;
6. gpu31 or the exact `numpy-core` toolchain is unavailable and Linux private-COW behavior cannot be verified;
7. repeated resource/leak/race failures require a product trade-off rather than a local fix;
8. work would require Docker, paid cloud, production resources, external writes, package release or manual CI;
9. no bounded binary preparation ingress can be added without exposing a Host handle/path to Guest, retaining the body after seal, changing frozen paper behavior or broadly redesigning the Guest ABI;
10. all executable phases are complete.

Do not stop merely because:

- a focused test fails;
- one implementation approach is rejected;
- a commit or phase is complete;
- COW is not faster in a tiny fixture;
- no real evaluation dataset exists;
- a child Agent review finds a bounded bug.

When stopped, record the exact blocker, modified files, tests, Git status and smallest safe alternatives.

---

## Tracking rules

- This file becomes the execution source of truth only when Yuzhe starts the goal.
- Add a `Current execution pointer` below before the first implementation edit.
- Change `[ ]` to `[x]` only with real evidence.
- Mark rejected work `[rejected]` and optional work `not entered`; do not erase negative gates.
- Append concise completion entries with phase, commands/results and commit.
- Do not put raw arrays, response bodies, machine-private paths or large logs in committed evidence.
- Signed push after every independently useful verified phase is a checkpoint, not a stopping condition.

## Completion log

- 2026-08-23: Goal prepared from live code and Linux-path archaeology. Current fixed prepared-data/COW, `numpycodec`, `engine.Runner`, workspace and subagent seams were mapped. Local full Go/vet/Guest gates pass. A pre-existing split-bootstrap script-test regression was fixed and pushed at `0eb79b59`. Exact-HEAD focused Host tests passed on gpu31 using the shared Go 1.25 toolchain/caches. No Cohort implementation, real dataset, performance observation or paper change has started.
- 2026-08-23: Independent read-only implementation audits confirmed that the current 8 MiB body is base64-embedded in research trusted source while `PreparedRegionTable` carries only a scalar claim, and that `PreparedDataContract` is Run-scoped. The prepared goal now requires a body-free promoted source, bounded Host-internal preparation transfer, per-consumer Run bindings, Host-owned cohort joins, unchanged `RunRequest`, and no `MechanismSet.Cohort`.
- 2026-08-23: Independent launch review found that product implementation was still underspecified at the Guest ABI, runner authority adapter, lifecycle, compatibility identity, portable fallback, Host Linux gate, initialization parity, terminal record, dtype qualification and mutable `RunConfig` seams. Phase 0 now owns explicit contracts and RED gates for all ten; later Runtime/COW phases may not start before Gate P0.
- 2026-08-23: Phase 0 passed. `docs/prepared-family-v1.md` freezes the copy-or-broker binary ingress, Wazero-owned family API, per-consumer authority adapter, lifecycle, compatibility identity, portable/COW dispositions and terminal-record owner. Commit `81c941e3` added the bounded clean-HEAD gpu31 Host gate; its baseline suite passed on gpu31 with Go 1.25. The exact `numpy-core` artifact qualified all fourteen bounded dtype strings by reconstruction. Guest ABI and family/config/lifecycle tests were observed RED before implementation (`PYTHONPATH=guest/bootstrap python3.13 -m unittest guest.tests.test_prepared_numpy_input guest.tests.test_source_contract`; `go test ./runtime/engine/wazero -run TestPrepared -count=1`).

- 2026-08-23: Phase 1 passed. Commit `7cc7139f` added the bounded Guest binary ndarray ingress, immutable copied Host input, config freezing, single-use lifecycle/adapter tests and portable private-copy real-Guest oracle. Local Go/vet and 272 Python tests passed. The exact clean-HEAD gpu31 `prepared-family` suite built `numpy-core`, ran the real three-layout private-copy/mutation-isolation test plus focused race/vet gates, and returned `passed=true` in 531651 ms. Multi-MiB bodies remained outside Run/Broker/evidence JSON.

- 2026-08-23: Phase 2 passed. Commits `4b0bf7dd` and `5ae60911` replaced the fixed base64-derived dataset path with generic descriptor-bound binary preparation over one immutable package parent. Local full Go/vet, focused race and Python gates passed. Two exact clean-HEAD gpu31 suites built the new Guest and proved three dtypes/layouts, same-layout different bodies, mutation/error isolation and N=0/1/2/4 fanout; the final run returned `passed=true` in 578088 ms. Existing fixed probes and body-free image state remained intact; no performance campaign ran.

## Short prompt to start this mega-goal

```text
Read docs/plans/2026-08-23-prepared-runtime-families-private-cohorts-autonomous-megagoal.md fully and execute it in /Users/yuzhe/projects/agent-python-runtime. Start at Phase 0 and do not edit the product COW lifecycle until Gate P0 freezes and RED-tests the binary preparation ABI, family-backed authority adapter, state machine, compatibility identity and Linux Host gate. Then turn the fixed prepared NumPy/private-COW probe into the smallest reusable Host-owned prepared runner family, keep the Runtime free of scheduling policy, compose existing workspace/subagent primitives, use local macOS plus exact bounded gpu31 Linux gates, update the roadmap, signed commit and push after coherent verified phases, and continue until complete or a named architecture/safety gate. Do not invent a real evaluation dataset, reopen paper economics, trigger CI or build an external-effect plane.
```
