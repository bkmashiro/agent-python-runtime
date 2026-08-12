# Pysolate Research Substrate Autonomous Megagoal

> **For Hermes:** This is the long-running `/goal` source of truth. Read this file completely, inspect the live repository and Git state, then execute continuously across multiple coherent TDD slices. A green slice or signed commit is a checkpoint, not a stopping condition. Stop only at the explicit conditions below or after every required checkbox and final release gate is complete.

**Goal:** Turn the completed curated-source playback vertical into a research-grade substrate: simplify and document identity relations, productize reproducible acceptance, expose a bounded Lab-ready observation contract, prototype content-addressed deduplicated research storage, support counterfactual branches at capability-operation boundaries, add a verified deterministic-execution profile, and prove generality with a second research-oriented curated source.

**Architecture:** Pysolate Runtime remains a fresh-per-Run CPython/WASI execution and authority substrate. It emits optional Host-owned execution evidence and content references but does not own Agent conversations, provider parsing, long-term study UX or swarm orchestration. A future independent Pysolate Lab/Harness will join Agent events to Runtime execution references, store large bodies once in protected content-addressed storage, construct branch DAGs and provide a GitLens-like interface. This roadmap implements the Runtime-side contracts and a bounded local research prototype without turning the Runtime into the Lab.

**Tech Stack:** Go 1.25+, wazero, real CPython/WASI artifact, canonical JSON, SHA-256 where an actual content/authority/relation identity is required, current Workspace/Capsule/Capability/Playback machinery, standard library by default. Any SQLite or other storage dependency requires an explicit benchmark-backed decision and must live outside the Runtime core dependency path.

---

## 1. Source of truth and repository

- Repository: `/Users/yuzhe/projects/agent-python-runtime`
- Branch: `main`
- Starting required baseline: `38e06f2bf0904aa2d890fae940aaaf220c795685`
- Real Guest artifact: `/private/tmp/pysolate-current-artifact/dist-272811/agent-python-runtime.wasm`
- Previous completed roadmap: `.hermes/plans/2026-08-12_002754-curated-information-source-playback-megagoal.md`
- Active roadmap and checkbox source of truth: this file
- Companion execution prompt: `.hermes/plans/2026-08-12_025644-research-playback-branching-determinism-prompt.md`
- Product direction: `docs/product-direction.md`
- Architecture and threat boundary: `docs/architecture.md`, `docs/threat-model.md`
- Playback and workspace contracts: `docs/playback-bundles.md`, `docs/workspace-capsules.md`

Inspect real Git state before editing. Never reset, discard, overwrite or duplicate existing work. If the tree is dirty, understand and finish the existing coherent slice first.

## 2. Product intent

This is one broad, integrated megagoal—not a single branching feature. It must create a useful research foundation for a later Pysolate Lab that can eventually:

- inspect every Agent interaction and link it to exact Runtime executions;
- inspect observable Host/WASI/capability boundaries honestly;
- inspect Agent-visible filesystem evolution;
- compare runs and counterfactual branches in a GitLens-like lineage UI;
- retain very long tasks efficiently by storing each immutable semantic body once;
- support future Agent swarm timelines, causal graphs and aggregate analysis without copying the same prompts, code, tool payloads or files into every event.

The full Lab, provider adapters and GUI are **not** implemented in this Runtime megagoal. This goal must make their future implementation natural rather than forcing a redesign.

## 3. Ownership split

```text
Future Pysolate Lab / Agent Harness
  owns Agent runs, turns, model requests/responses, routing, direct tools,
  branch DAG UX, query/indexing, retention/redaction and swarm analysis
                         │
                         │ Host-only invocation/execution references
                         │ versioned evidence/content-reference contract
                         ▼
Pysolate Runtime
  owns exact executed code, fresh Guest lifecycle, artifact/profile,
  frozen capability authority, Broker operations, workspace state,
  Runtime outcome and bounded observable execution evidence
```

### Runtime may expose

- logical invocation and physical execution references supplied/created by Host;
- exact executed-code identity;
- artifact/profile/plan/grant identities;
- capability operation sequence and bounded receipts;
- initial/final workspace tree identity and bounded file-delta metadata;
- execution phase/timing/status evidence;
- optional bounded WASI-boundary event classes only where actual instrumentation exists;
- protected content references for bodies retained by an explicitly configured research recorder.

### Runtime must not claim or own

- every Python bytecode, local variable, branch or memory access;
- complete Agent semantics, prompts or provider envelopes;
- omniscient filesystem observation when only initial/final state is measured;
- workflow orchestration, model routing or swarm scheduling;
- a GUI, hosted trace SaaS or production multi-user database;
- deterministic full-Agent replay.

## 4. Identity and SHA-256 policy

SHA-256 is not automatically security, correctness or meaning. Every digest retained or added must belong to exactly one documented class:

1. **Content identity** — immutable bytes stored once and referenced many times, e.g. code, canonical JSON payload, file/blob, manifest.
2. **Authority identity** — frozen Host policy/Grant/Plan relation that controls what a Run may do.
3. **Artifact/config identity** — exact Runtime artifact, execution profile or initial workspace required for compatible execution.
4. **Evidence/relation identity** — parent/child branch, operation, result or final-state relation that must fail closed when substituted.
5. **Redundant convenience digest** — derivable from an already verified root with no independent trust or lookup value; remove or avoid it.

Rules:

- Prefer one verified content identity per immutable semantic body and references from events/relations.
- Prefer a small domain-separated root identity over repeating equivalent field-level digests.
- Keep field-level digests only when they provide independent streaming validation, bounded lookup/dedup, admission, privacy-preserving comparison or tamper localization.
- A digest proves byte identity/integrity, not semantic correctness.
- A self-hash is consistency evidence, not authentication; protected/trusted roots remain Host-owned.
- The generated Python API and Guest code must not need to submit, understand or manage these identities.
- Raw bookkeeping should not be injected into Agent context by default. The Host caller/Lab may inspect it or present semantic labels such as “same input”, “different plan”, “branched at source call 2”.
- Do not weaken existing fail-closed relations merely to reduce visible digest count.

## 5. Hard boundaries

Do not add:

- shell, subprocess, ambient network, arbitrary Host filesystem or raw sockets;
- Agent-facing generic HTTP, Agent-controlled URL/path/query/method/header/credential/budget;
- POST/write effects, Effect Plane, reconciliation, transactions or external mutation;
- interpreter/WASM-memory/heap snapshots, pinned sessions or resume-from-heap;
- mid-Run authority expansion;
- browser/Computer/VM fallback, package installation or plugin marketplace;
- raw credentials in any Bundle, trace, blob store, fixture or report;
- public-network, paid-cloud, Docker or production-account tests;
- a full Lab UI or provider-specific Agent trace implementation in Runtime core.

Branching in this goal means fresh re-execution from the original initial state with a strict recorded prefix and an explicitly Host-owned suffix policy. It does **not** mean restoring arbitrary Python heap state.

## 6. Global Definition of Done

All items must be checked with real evidence:

- [ ] Previous playback roadmap is marked closed and points to this active successor without losing evidence.
- [ ] A checked-in identity/digest inventory classifies every persisted or externally visible SHA relation and removes only proven redundancy.
- [ ] A repository-maintained real-Guest acceptance harness reproduces live capture → source shutdown → offline playback with stable machine-readable evidence.
- [ ] A versioned Lab-ready Runtime observation contract exists, including no-capability Runs, and does not accept Guest-forged Host evidence.
- [ ] The strongest honest WASI/filesystem observation level is measured and documented; unsupported internal visibility is not claimed.
- [ ] A bounded content-addressed research-store prototype stores repeated semantic bodies once, validates them on read, and has measured storage behavior on long and swarm-shaped fixtures.
- [ ] Counterfactual branch execution works from a capability-operation boundary in a fresh Guest, with strict parent-prefix validation and explicit child lineage.
- [ ] Branching never restores hidden interpreter state, never lets the Agent select authority, and never silently reuses a live external write.
- [ ] A bounded deterministic-verification profile controls or captures each claimed nondeterministic input and fails admission for unsupported ones.
- [ ] A second research-oriented curated source, `sources.benchmark_manifest()`, proves that capture/playback/branching are not hard-coded to `demo_catalog`.
- [ ] Operator research commands inspect, compare and branch using semantic summaries; users do not manually edit Bundle JSON or copy internal field digests.
- [ ] Full real E2E demonstrates parent capture, offline replay, two counterfactual children, deterministic repeatability, branch lineage and storage dedup.
- [ ] Documentation separates Current, Experimental and Proposed claims accurately.
- [ ] Full tests/vet/build/race/Python/ABI/tamper/privacy/docs gates pass on the final commit.
- [ ] Independent final review reports no blocker for identity, branching, authority, recorder privacy, determinism claims and zero-network playback.
- [ ] Only after all required items pass, signed commits are pushed once and `HEAD == origin/main` with a clean worktree.

## 7. Stop conditions

Continue automatically after every verified slice. Stop only if:

1. all executable work and checkboxes are complete;
2. a decision is required that would move Agent/Harness semantics into Runtime or introduce a full Lab product/UI now;
3. branch semantics would require heap/WASM-memory snapshot restoration rather than fresh prefix re-execution;
4. a new live authority would appear mid-Run instead of being sealed before the branch Run;
5. honest deterministic verification requires changing the CPython/WASI artifact and the artifact cannot be rebuilt or qualified locally;
6. filesystem/WASI instrumentation requires an unmaintainable wazero fork or unsupported runtime patch—record the measured boundary and implement the lower honest observation level instead where possible;
7. a storage backend choice requires an external service, production credentials or an unbounded dependency without benchmark evidence;
8. real Guest gates cannot run and no honest equivalent exists;
9. repeated gate failure exposes a product/design choice rather than an implementation defect;
10. continuing would require weakening privacy, authority or fail-closed playback invariants.

If blocked, report exact blocker, evidence, modified files, tests, Git status and safest alternatives.

---

# Autonomous Execution Queue

## Track 0 — Closeout, baseline and identity map

**Product promise:** Begin from a clean, truthful baseline and understand every identity before modifying the evidence model.

**Primary files:**
- previous and current `.hermes/plans/*.md`
- `runtime/execution_ref.go`
- `runtime/capability/{registry.go,broker.go,transcript.go,playback.go}`
- `runtime/playback/bundle.go`
- `runtime/workspace/capsule.go`
- `cmd/apyrun/{main.go,playback_binding.go}`
- all docs that describe identity or replay

### Tasks

- [ ] Verify `main`, clean state, signed baseline and exact `HEAD == origin/main` before new code.
- [ ] Convert the previous roadmap into a closed evidence record and point its maintenance handoff to this roadmap.
- [ ] Produce `docs/research/identity-model.md` with a table for every identity: producer, canonical bytes, consumer, trust owner, admission/lookup/privacy purpose, derivability and tamper test.
- [ ] Identify duplicate digests that protect no independent boundary; write RED tests before removing any externally encoded field.
- [ ] Preserve schema compatibility or version the Bundle/contract when a canonical document changes.
- [ ] Document which identities the Guest, Agent/Harness and Lab can observe.

### Gates

```bash
go test ./runtime/... ./cmd/apyrun -count=1
go test -race ./runtime/... ./cmd/apyrun -count=1
git diff --check
```

### Do not

Do not perform a cosmetic rename campaign. Do not replace domain-separated identities with one ambiguous “run hash”.

## Track 1 — Productized real acceptance harness

**Product promise:** One maintained command reproduces the core live/offline proof and emits compact evidence for later Lab ingestion.

**Primary files:**
- `integration/e2e/`
- new `cmd/` or `scripts/` acceptance entrypoint only if existing Go tests cannot provide the operator artifact
- `docs/development.md`
- `docs/playback-bundles.md`

### Tasks

- [ ] Move the temporary live→offline acceptance logic into a repository-maintained fixture/harness.
- [ ] Require an explicit real Guest artifact; absent artifact must SKIP/UNAVAILABLE truthfully, never fake semantic success.
- [ ] Emit bounded canonical JSON with source hit count, Bundle identity/path/mode, parent result/workspace identities, privacy scan and exact artifact/profile.
- [ ] Ensure all servers are loopback-only and test-scoped, with zero residual processes.
- [ ] Add failure controls for live handler construction during playback and for stale evidence after source edits.
- [ ] Add a public documented command and execute it exactly as documented.

### Gates

- focused acceptance tests;
- full real Guest acceptance under race;
- output schema/tamper/privacy tests;
- no public network.

## Track 2 — Lab-ready Runtime observation contract

**Product promise:** A future Lab can correlate Agent events with exact Runtime attempts and inspect honest bounded Runtime evidence without coupling Runtime to a trace database.

**Primary files:**
- `runtime/execution_ref.go`
- `runtime/response.go`
- `runtime/engine/wazero/engine.go`
- new narrowly scoped `runtime/observe/` or equivalent
- capability Broker and workspace lifecycle seams

### Required contract

Use separate identities:

- Harness-owned `agent_run_id`, turn/item/segment coordinates;
- logical `invocation_id` stable across retry;
- one-based `invocation_attempt`;
- unique Host `execution_id` per physical Runtime attempt;
- exact `executed_code` content reference;
- event/spans with causal parents rather than assuming one total order for future swarms.

### Tasks

- [ ] Audit existing `InvocationRef`/`ExecutionRef`; extend only missing fields and preserve no-capability Runs.
- [ ] Define `pysolate.runtime-observation.v1` events/envelopes with exact keys, bounded fields, canonical encoding and Host-only projection.
- [ ] Record execution lifecycle boundaries, capability calls, plan/artifact/profile references, terminal disposition and initial/final workspace identities.
- [ ] Ensure Guest responses cannot forge, case-fold, duplicate or override observation fields.
- [ ] Provide `off`, `best_effort` and `required` recorder modes; in required mode recorder failure invalidates research evidence, not authority.
- [ ] Make sinks optional interfaces outside engine policy; Runtime core must run without a store.
- [ ] Test append failures: no sequence gaps, no nonexistent causal parent, immutable payload copies.
- [ ] Define private body references separately from portable metadata events.

### Honest visibility matrix

- [ ] Measure what wazero exposes for WASI host-function/syscall instrumentation without a fork.
- [ ] If stable instrumentation exists, capture bounded event type/path-class/status/byte counts—not full sensitive file bodies by default.
- [ ] If it does not, expose initial/final workspace manifests and file-level deltas, explicitly label syscall order unavailable, and save a decision report.
- [ ] Never claim Python bytecode/local-variable/internal-memory tracing.

### Gates

- exact-key/duplicate/folded-alias tests;
- no-broker correlation tests;
- recorder fail-open/fail-closed mode tests;
- race tests for concurrent observations;
- real Guest lifecycle and workspace evidence tests.

## Track 3 — Content-addressed research store prototype

**Product promise:** Long and swarm-shaped studies retain immutable semantic bodies once while events and branches reference them cheaply.

**Location boundary:** Keep this outside Runtime core, for example `research/labstore/` plus a local CLI/probe. It may depend on Runtime public observation contracts; Runtime must not depend on it.

### Object model

At minimum distinguish:

- immutable blobs: prompt/provider body/code/canonical tool payload/file content;
- normalized semantic documents/manifests;
- metadata events/spans;
- run, execution and branch DAG relations;
- workspace trees whose entries reference deduplicated file blobs;
- indexes and retention metadata that are not part of content identity.

### Tasks

- [ ] Write a decision note comparing at least: CAS directory + append-only index, single-file pack/index, and SQLite metadata + external/internal blob choices.
- [ ] Define canonical content typing/domain separation so identical bytes with incompatible semantics cannot alias accidentally.
- [ ] Implement the smallest local prototype supported by measured needs; do not place a new DB dependency in Runtime core.
- [ ] Enforce `0600`, exclusive creation where applicable, bounded reads, digest validation, atomic object publication and traversal/symlink denial.
- [ ] Store one body once across repeated events, branches and agents; test reference counting/retention semantics without deleting reachable parents.
- [ ] Keep credentials forbidden; define redaction/retention and private-vs-portable export policy.
- [ ] Add read-only/query-only opening that performs no migrations or writes.
- [ ] Build synthetic long-task and swarm fixtures with repeated system prompts, code, tool results and workspace files.
- [ ] Measure raw duplicated bytes vs store bytes, object count, ingest/query latency and index growth. Report numbers; do not claim “highly optimized” without them.
- [ ] Set an explicit stop/reframe threshold if metadata/index overhead overwhelms savings on representative fixtures.

### Required benchmark shapes

- one long sequential Agent task with repeated context prefixes;
- many branch children sharing one parent prefix;
- a swarm with many agents sharing prompts/artifacts/workspace blobs but divergent events;
- one low-reuse control showing the overhead floor.

## Track 4 — Counterfactual capability-boundary branching

**Product promise:** Researchers can hold the original program and initial state constant, alter one captured external input boundary, and obtain a separately evidenced child Run.

### Semantics

```text
Parent Bundle P, fork operation N, Branch Manifest B
  → seal complete child Run plan before execution
  → start fresh Guest from parent's initial request/workspace
  → strictly replay operations [0, N)
  → operation N and suffix follow B's explicit Host-owned source policy
  → capture child status/result/final workspace/transcript
  → publish child lineage referencing P and B
```

Branch modes required:

1. `override`: Host supplies schema-validated alternative result(s) at/after N.
2. `recorded_suffix`: Host supplies a complete alternate tape.
3. `live_suffix`: only for curated `external_read`, through a new fully sealed child Plan/Grant; no hidden authority expansion.

### Tasks

- [ ] Define canonical `pysolate.playback-branch.v1` manifest with parent Bundle identity, fork operation, prefix identity, child plan/grants, suffix policy references and expected initial state.
- [ ] Keep branch manifest Host-owned and protected; Agent cannot choose fork, payload, endpoint or authority.
- [ ] Implement mixed Broker routing with strict prefix consumption and fail-poison behavior.
- [ ] Revalidate every override result against the selected capability output schema and byte limits.
- [ ] Allow the child to diverge after N; do not compare child final outcome to parent as if it were playback.
- [ ] Capture child outcome as new evidence with parent/fork lineage.
- [ ] Reject N outside the transcript, reordered prefix, changed prefix arguments, incompatible capability Spec, stale parent identity, changed initial request/workspace and unused suffix.
- [ ] Reject branch attempts on non-captured/write-effect capabilities.
- [ ] Test concurrent branches from one parent without shared mutable Broker state.
- [ ] Prove every child uses a newly created fresh Guest.

### Explicit non-goals

- arbitrary source-line breakpoint;
- restoring Python locals/stack/heap/WASM memory;
- starting from a mutated hidden mid-Run state;
- silently changing authority at the fork.

## Track 5 — Deterministic-verification profile

**Product promise:** Pysolate can make a bounded, falsifiable determinism claim for qualified workloads rather than relying on isolation alone.

### Investigation matrix

- WASI wall and monotonic clocks;
- Python `time`/`datetime`;
- WASI random and Python `random` default seed;
- `os.urandom`/`secrets` behavior and admission;
- Python hash randomization;
- timezone and locale;
- directory iteration order;
- artifact/profile/Host implementation versions;
- concurrency or floating-point cases that remain outside the claim.

### Tasks

- [ ] Build real-Guest probes that run twice and expose current divergences before implementation.
- [ ] Write a versioned deterministic profile contract binding each controlled/captured source.
- [ ] Prefer Host-controlled virtual inputs or explicit denial; never monkey-patch Agent source invisibly.
- [ ] Bind the profile to artifact and execution identity.
- [ ] Fail pre-execution when code/import requirements use unsupported nondeterminism under verification mode.
- [ ] Canonicalize filesystem enumeration only where the Runtime actually controls it.
- [ ] Test deterministic success, explicit unsupported admission and ordinary non-verification behavior unchanged.
- [ ] Run a matrix of pure computation, workspace transformation, captured-source analysis and branch workloads across multiple fresh Guests.
- [ ] Document exact claim boundary: deterministic only for the qualified profile and captured inputs.

### Stop condition

If a relevant CPython/WASI source cannot be controlled, captured or statically/admission-denied, keep the profile Experimental/Partial and name the gap; do not claim full determinism.

## Track 6 — Second curated research source

**Product promise:** A real research workload proves that typed source capture/branching generalizes beyond a flat demo catalog.

### Capability

```python
manifest = sources.benchmark_manifest()
```

The Host-private fixed endpoint returns a versioned nested structure describing a benchmark suite, for example:

- suite ID/version/title;
- bounded cases with stable IDs, task class and input artifact references;
- metrics with direction/unit;
- optional tags/categories under a strict bounded schema.

The Agent uses it to generate an experiment matrix or workspace report. It does not control URL, method, headers, credentials or transport policy.

### Tasks

- [ ] Freeze a concrete local research fixture and expected Agent analysis before defining the Spec.
- [ ] Define dedicated docs/schema/effect=`external_read`/playback=`captured` and generated `sources.benchmark_manifest()` projection.
- [ ] Implement an exact-endpoint Host adapter without copying validation logic unnecessarily.
- [ ] Bind all authority-affecting policy through its per-Run Grant and sealed Plan.
- [ ] Test nested schema bounds, duplicate IDs, unknown fields, unsupported versions, invalid metric direction/unit, transport failures and privacy.
- [ ] Capture and offline-play the source after server shutdown.
- [ ] Branch the same Agent program from the benchmark-manifest operation with an alternate manifest; prove deterministic changed result/workspace and shared-prefix lineage.
- [ ] Run a multi-source program using both `demo_catalog` and `benchmark_manifest`; test order and fork points across two capabilities.

### Do not

Do not introduce generic GET or make the adapter a generic JSON passthrough. Do not add credentials or public endpoints.

## Track 7 — Research operator UX

**Product promise:** Researchers work with semantic run/branch concepts rather than editing canonical JSON or copying hashes manually.

### Required local commands or equivalent API

- `inspect`: summarize run/execution/plan/source calls/workspace/status and content reuse;
- `compare`: parent/child or arbitrary run comparison by status, calls, workspace tree and result identity;
- `branch plan`: create/validate a Host-owned branch manifest from semantic operation selection;
- `branch run`: execute and publish child evidence;
- `store stats`: object/reference/bytes/dedup metrics;
- bounded JSON output for future GitLens-like UI consumption.

### Tasks

- [ ] Design a small coherent CLI surface; do not overload `apyrun` if a separate research CLI preserves boundaries better.
- [ ] Resolve paths/identities internally from protected artifacts; print short semantic labels by default and full identities only in JSON/detail mode.
- [ ] Add paged/bounded inspection; no unbounded dump by default.
- [ ] Open research stores read-only for inspect/compare.
- [ ] Produce branch DAG nodes/edges suitable for a future UI, including causal parents and shared content references.
- [ ] Add golden/output-contract tests and execute public examples.

### Future UI boundary

Document the future GitLens-like views—timeline, DAG, operation detail, workspace diff, content reuse and swarm lanes—but do not build the full UI in this goal.

## Track 8 — End-to-end research proof

**Product promise:** The combined system proves useful research workflows, not isolated unit mechanisms.

### Mandatory real demonstration

Using the real CPython/WASI artifact and loopback-only servers:

1. Start local `demo_catalog` and `benchmark_manifest` servers with request counters.
2. Run one fresh parent Guest that calls both sources and writes a deterministic experiment plan into mounted workspace.
3. Publish the parent Playback Bundle and observation evidence; ingest it into the research-store prototype.
4. Stop both servers.
5. Run a fresh strict offline playback; total network hits remain unchanged and parent result/workspace match.
6. Create child A at the benchmark-manifest operation with a Host-provided alternate recorded manifest.
7. Create child B from the same parent/fork with a different alternate manifest or approved live read-only suffix.
8. Prove prefix calls match parent, children have distinct lineage/outcomes, each child is a fresh Guest, and parent artifact remains unchanged.
9. Repeat parent/child executions under deterministic-verification profile and compare exact qualified identities.
10. Ingest a synthetic long/swarm fixture; prove repeated prompts/code/payloads/files are stored once and report measured savings/overhead.
11. Inspect and compare through the public research CLI/API.
12. Tamper parent, fork index, prefix, child plan/grant, override schema/result, branch lineage, content object, workspace tree and event parent; all fail closed.
13. Inspect protected artifacts for Agent source/prompt/final result/workspace/Capsule/credential leakage according to each artifact's declared privacy class.

### Evidence artifacts

Save bounded local evidence under a protected temporary or `.artifacts-private` location, including:

- machine-readable acceptance report;
- parent/child identities and semantic summaries;
- branch DAG export;
- store benchmark report;
- deterministic matrix report;
- privacy/tamper report;
- exact commands and artifact identity.

Do not commit sensitive bodies or generated binary evidence.

## Track 9 — Documentation, independent review and release

- [ ] Update README/operator/architecture/threat/playback/product-direction docs with Current/Experimental/Proposed labels.
- [ ] Add a Lab boundary document explaining what belongs in Runtime versus future Lab/Harness.
- [ ] Add Bundle/branch/observation compatibility and version-upgrade policy.
- [ ] Audit all identity language: digest ≠ security, receipt ≠ success, trace ≠ deterministic replay, branch ≠ heap restore.
- [ ] Run complete final gates on the exact final candidate.
- [ ] Obtain independent read-only review of the entire commit range and final worktree, focused on authority, lineage, dedup privacy, tamper, recorder failure, determinism overclaims and real E2E.
- [ ] Fix every real blocker with RED regression and rerun proportional/full gates.
- [ ] Push `main` once only after all required checkboxes and review pass.
- [ ] Verify every required commit signature, clean tree and exact `HEAD == origin/main`.

---

## 8. Per-slice execution discipline

For every code slice:

1. inspect current state and analogous code;
2. write a focused RED test and run it to prove failure;
3. implement the minimum coherent design;
4. run focused GREEN and proportional race/real-Guest gates;
5. update this roadmap immediately with checkbox and evidence;
6. run `git diff --check` and relevant global gates;
7. create a signed conventional local commit;
8. verify signature and clean/expected Git state;
9. continue immediately into the next safe unchecked slice.

Do not push intermediate commits. Do not stop after one successful slice.

## 9. Final global gates

At minimum:

```bash
go test ./... -count=1
go vet ./...
go build ./cmd/...
go test -race ./... -count=1
AGENT_RUNTIME_GUEST=/private/tmp/pysolate-current-artifact/dist-272811/agent-python-runtime.wasm go test -race ./integration/e2e -count=1
python3 -m unittest discover -s tests -p 'test_*.py'
python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m compileall -q guest tests
git diff --check
```

Also run:

- identity/compatibility fixtures;
- Bundle/branch/event/store duplicate-key, trailing-data, invalid-UTF8, unknown-field and folded-alias tamper tests;
- real source shutdown/zero-network tests;
- branch prefix/suffix/lineage tamper matrix;
- deterministic profile probe matrix;
- content-store corruption/privacy/dedup/retention/read-only tests;
- public CLI examples;
- changed Markdown link/fence checks;
- repository-standard sensitive-content scan without embedding its matcher into this roadmap;
- independent final review.

## 10. Commit policy

Use signed conventional commits after coherent verified stages, for example:

```text
docs: close playback roadmap and classify identities
test: productize real playback acceptance
feat: add runtime observation contract
feat: prototype deduplicated research storage
feat: branch playback at capability boundaries
feat: add deterministic verification profile
feat: add benchmark manifest source
feat: add research inspection commands
test: verify counterfactual research workflows
docs: define Pysolate Lab boundary
```

Adapt to actual slices; do not manufacture commits. Push once at the final release gate.

## 11. Reporting when finally stopping

Return:

- required signed commits;
- exact test/race/real-Guest outputs;
- identity simplifications retained/removed and why;
- parent/child branch evidence and fresh-Guest proof;
- deterministic profile coverage and explicit unsupported cases;
- second-source and multi-source evidence;
- observation level actually achieved at WASI/filesystem boundaries;
- research-store measured bytes/objects/latency/dedup across long/swarm fixtures;
- privacy/tamper results;
- independent review conclusion;
- remote synchronization status;
- exact items deferred to the future full Pysolate Lab/UI.

## 12. Short prompt to start this megagoal

Use the companion prompt file or paste:

```text
Read `/Users/yuzhe/projects/agent-python-runtime/.hermes/plans/2026-08-12_025644-research-playback-branching-determinism-megagoal.md` fully, then execute it continuously on `/Users/yuzhe/projects/agent-python-runtime` `main`. Trust live Git state, use TDD, update the roadmap after real gates, make signed local commits, do not push until the complete final review, and do not stop after one successful slice. Preserve fresh-per-Run, Host-owned authority, no shell/generic HTTP/write effects/heap snapshots, and the Runtime-vs-future-Lab boundary. Stop only at a roadmap stop condition or complete verified push.
```
