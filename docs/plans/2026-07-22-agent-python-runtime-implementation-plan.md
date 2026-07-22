# Agent Python Runtime Implementation Plan

> **For Hermes:** Use subagent-driven-development only for bounded implementation/review lanes. The controller owns architecture, scope, provenance decisions, final review, and all gates.

**Goal:** Build and verify a standalone capability-controlled CPython/NumPy WASI runtime for AI agents, with a neutral ABI, a Go/wazero host, one read-only host capability, and Linux CI that produces and consumes a provenance-bound guest artifact.

**Architecture:** Keep the Python guest producer and Go runtime in one private repository, but preserve a strict build-time/runtime boundary. GitHub Actions builds the guest from pinned official sources, emits a WASM artifact plus manifest/checksums, then passes that exact artifact to Linux host E2E jobs; the binary is not committed to Git. The host owns grants, limits, lifecycle, state reset, tool execution, and receipts, while generated Python receives no ambient host authority.

**Tech Stack:** Go 1.24.x, wazero, CPython 3.14.x, NumPy WASI subset, WASI SDK 33, `wasm32-wasip1`, C/Python guest bootstrap, JSON Schema, GitHub Actions on Ubuntu, `wasm-tools`, `actionlint`.

---

## 0. Decisions frozen by this plan

1. Create `bkmashiro/agent-python-runtime` as a **private** GitHub repository unless the user explicitly changes visibility.
2. Push over SSH. Use signed, small commits.
3. GitHub Actions may build/test/package artifacts. Do not deploy, publish a package, create a public release, or use paid infrastructure.
4. Keep producer and host in this repository for v1. Do not create a separate artifact service or producer repository.
5. Do not commit the large canonical `.wasm`; transfer it between CI jobs as an Actions artifact. Keep only tiny synthetic ABI fixtures in Git.
6. Do not copy `webassembly-language-runtimes/python/reactor/py_reactor.c`: it contains Shimmy/Lambda Feedback semantics and compatibility exports.
7. Do not copy `wasi-wheels` build scripts while its repository lacks an explicit root license. Prefer a clean implementation from official CPython/NumPy/WASI SDK sources. If unavoidable build knowledge cannot be independently reproduced, stop for a provenance decision.
8. CI must never consume mutable `latest` assets without an independently pinned SHA-256. Source tags/commits, tool versions, action SHAs, and downloaded asset hashes are lock data.
9. Start with pure CPython. Add NumPy only after the neutral guest and host path pass.
10. The canonical product claim is capability-mediated composition with fresh run-local state. Performance is secondary and must come from this repository's evidence.

## 1. CI topology

### Automatic cheap workflow: `.github/workflows/ci.yml`

Triggers:

- pull requests;
- pushes to `main`.

Jobs:

- contract/schema fixtures;
- Go formatting, tests, race test, vet, build;
- Python/C source contract tests that do not pretend to execute WASI;
- shell syntax, `actionlint`, `git diff --check`;
- dependency/provenance lock validation.

### Path-scoped guest workflow: `.github/workflows/guest-artifact.yml`

Triggers:

- `workflow_dispatch`;
- pull requests and `main` pushes touching `guest/**`, `abi/**`, `runtime/abi/**`, the workflow, or producer lock files.

Jobs:

1. `build-guest-core` — build pure CPython guest.
2. `verify-guest-contract` — exact WASM magic/import/export/manifest/hash checks and pure-Python smoke.
3. `build-guest-numpy` — activated after core is green; build canonical CPython+NumPy artifact.
4. `host-e2e` — download the artifact from this run and execute real Go/wazero integration and denial tests.
5. `package-evidence` — upload one bundle containing artifact, manifest, checksums, notices, and raw test report.

Use a 45-minute timeout. Cache only by complete lock identity; a cache hit is not provenance evidence.

### Manual release-strength workflow: `.github/workflows/reproducibility.yml`

Trigger: `workflow_dispatch` only.

- cold-build the canonical guest twice in isolated directories;
- compare artifact SHA-256 and manifest after removing only explicitly documented run metadata;
- run the full host E2E and benchmark smoke;
- upload both build records and a comparison report;
- do not create a GitHub Release automatically.

### Action/tool pinning

At implementation time, resolve current action tags to immutable commit SHAs and record the human-readable tag in comments. The current observed majors are `checkout@v7`, `setup-go@v7`, `setup-python@v7`, `upload-artifact@v7`, and `download-artifact@v8`; do not trust these observations indefinitely.

Pin at minimum:

- WASI SDK 33 x86_64 Linux asset and SHA-256;
- `wasm-tools` version and SHA-256;
- CPython source version/URL/SHA-256;
- NumPy source version/URL/SHA-256;
- wasi-vfs version/source/asset digest and license;
- Go and wazero versions;
- every GitHub Action by commit SHA.

## 2. Artifact bundle contract

Every successful guest build must upload:

```text
dist/
├── agent-python-runtime.wasm
├── manifest.json
├── SHA256SUMS
├── THIRD_PARTY_NOTICES.md
├── sbom.spdx.json
└── test-report.json
```

`manifest.json` must include:

- schema and guest ABI versions;
- repository commit and workflow run identity;
- CPython, NumPy, WASI SDK, wasi-vfs, compiler, and linker identities;
- source URLs/commits and SHA-256 values;
- exact compile/link flags;
- exact imports and exports;
- bundled Python packages and known unsupported surfaces;
- artifact byte size and SHA-256;
- build profile (`core` or `numpy`);
- smoke-test names and outcomes;
- provenance limitations.

A filename or successful upload is not sufficient evidence.

## 3. Implementation tasks

### Task 1: Create the remote and establish CI-safe repository policy

**Objective:** Make the existing local history a private GitHub repository without publishing runtime artifacts.

**Files:**

- Modify: `README.md`
- Create: `.github/dependabot.yml`
- Create: `.github/workflows/ci.yml`
- Create: `docs/development.md`

**Steps:**

1. Verify the worktree and signed local HEAD.
2. Create private `bkmashiro/agent-python-runtime` with no generated README/license/gitignore.
3. Add SSH `origin`, push `main`, and verify remote HEAD.
4. Add cheap CI with least-privilege permissions (`contents: read`).
5. Resolve Actions tags to immutable SHAs.
6. Run `actionlint` and YAML parsing locally where available.
7. Commit and push; inspect the real workflow run.

**Gate:** Remote is private, HEAD matches locally/remotely, no secret or large artifact is committed, and cheap CI is green.

### Task 2: Freeze provenance and runtime boundaries

**Objective:** Complete Tranche 0 before runtime code exists.

**Files:**

- Create: `NOTICE.md`
- Create: `docs/architecture.md`
- Create: `docs/threat-model.md`
- Create: `docs/adr/0001-runtime-boundaries.md`
- Create: `docs/adr/0002-guest-abi-v1.md`
- Create: `docs/adr/0003-artifact-provenance.md`
- Create: `guest/build/sources.lock.json`
- Create: `tools/verify_sources_lock.py`
- Test: `tests/test_sources_lock.py`

**TDD steps:**

1. Write failing tests for missing hashes, mutable `latest` URLs, absent license metadata, duplicate source IDs, and unsupported architectures.
2. Run tests and record RED.
3. Implement the smallest strict lock verifier.
4. Populate only verified official/pinned sources.
5. Run tests and record GREEN.

**Provenance decision:** `wasi-wheels` currently has no root LICENSE/NOTICE despite an MIT statement in README. Do not copy its scripts. If clean official-source builds cannot reproduce required CPython/NumPy inputs, stop rather than silently importing those scripts.

**Gate:** All source inputs have immutable identity, digest, license/notice treatment, and a documented role.

### Task 3: Define and validate ABI v1 schemas

**Objective:** Separate untrusted run requests from Host-owned authority and freeze wire behavior.

**Files:**

- Create: `abi/v1/request.schema.json`
- Create: `abi/v1/response.schema.json`
- Create: `abi/v1/tool-request.schema.json`
- Create: `abi/v1/tool-response.schema.json`
- Create: `abi/v1/fixtures/valid/*.json`
- Create: `abi/v1/fixtures/invalid/*.json`
- Create: `runtime/abi/v1/schema_test.go`
- Create: `go.mod`
- Create: `go.sum`

**Contract:**

- `RunRequest`: run ID, code, JSON inputs, optional output schema reference/data only.
- `RunConfig`: never serialized from the model request; contains capability grants and budgets in Go.
- unknown fields fail;
- request cannot contain capabilities, credentials, network destinations, environment variables, filesystem mounts, or budget overrides;
- response has bounded `status/result/receipts/metrics/error` fields;
- pointer/length integers and max sizes are defined in the ABI ADR.

**TDD steps:** Add valid fixtures first, then negative authority-bearing and malformed fixtures, prove RED, implement schema loading/validation, prove GREEN.

**Gate:** JSON schemas parse; every positive fixture passes; every negative fixture fails for the intended reason.

### Task 4: Build a neutral pure-CPython WASI guest in CI

**Objective:** Produce a real callable reactor artifact without Shimmy/evaluator-domain semantics.

**Files:**

- Create: `guest/include/agent_runtime_v1.h`
- Create: `guest/src/runtime.c`
- Create: `guest/bootstrap/agent_runtime.py`
- Create: `guest/build/build-cpython.sh`
- Create: `guest/build/build-guest.sh`
- Create: `guest/build/write-manifest.py`
- Create: `guest/tests/test_source_contract.py`
- Create: `.github/workflows/guest-artifact.yml`

**Initial exports (subject to ADR review):**

```text
_initialize
runtime_init(ptr, len) -> status
runtime_prepare(ptr, len) -> status
alloc(len) -> ptr
dealloc(ptr)
execute(ptr, len) -> response_ptr
```

The response pointer addresses `[u32 little-endian length][JSON bytes]`. No export or Python handler may mention Shimmy, Lambda Feedback, `evaluation_function`, `preview_function`, or their request shape.

**Behavior tests:**

- expression and JSON input;
- syntax/runtime exception with bounded traceback;
- invalid UTF-8 and malformed JSON;
- missing/unknown fields;
- output-size rejection before unbounded buffering;
- unsupported import with a clear error;
- no runtime package installation.

**CI artifact checks:**

- WASM magic and `wasm-tools validate`;
- exact export set and reviewed import allowlist;
- no `_start` command-mode dependency;
- artifact/manifest SHA match;
- pure-Python smoke under the real target runtime.

**Gate:** Real Linux/WASI artifact executes the neutral contract. Native Python tests alone do not satisfy this task.

### Task 5: Implement the minimal Go/wazero host

**Objective:** Execute the exact CI-produced guest with no ambient authority.

**Files:**

- Create: `runtime/runtime.go`
- Create: `runtime/request.go`
- Create: `runtime/config.go`
- Create: `runtime/result.go`
- Create: `runtime/engine/wazero.go`
- Create: `runtime/engine/abi.go`
- Create: `runtime/engine/limits.go`
- Create: `runtime/engine/engine_test.go`
- Create: `integration/e2e/core_guest_test.go`

**TDD cases:**

- valid request and JSON response;
- invalid pointer/length;
- excessive response length;
- timeout/cancellation;
- memory growth above configured maximum;
- no inherited args, env, stdio, filesystem, network, or subprocess authority;
- close after cancellation/trap; unhealthy instance is never reused.

CI passes the artifact path explicitly, for example `AGENT_RUNTIME_GUEST_WASM=$RUNNER_TEMP/...`; tests must fail clearly when it is absent rather than falling back to native Python.

**Gate:** `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` pass on Ubuntu using the actual guest.

### Task 6: Prove quiescent preparation, snapshot, restore, and pool lifecycle

**Objective:** Reuse initialized CPython while proving request-local freshness.

**Files:**

- Create: `runtime/snapshot/snapshot.go`
- Create: `runtime/pool/pool.go`
- Create: `runtime/pool/instance.go`
- Create: `runtime/pool/pool_test.go`
- Create: `integration/e2e/freshness_test.go`
- Create: `integration/fixtures/state_canary.py`

**Required tests:**

- trusted `runtime_init` returns before snapshot;
- optional trusted `runtime_prepare` returns before prepared snapshot;
- Python globals, mutable module state, random state, and temporary buffers do not cross runs;
- reset runs after success and structured error;
- trap/cancel causes discard, not restore-and-reuse;
- memory-size drift causes discard/replacement;
- bounded pool concurrency and shutdown race;
- mutable globals/tables are statically audited and any unsupported reset surface fails closed.

Do not infer that restoring linear memory resets all WASM/Host state. Record what is naturally unwound, snapshotted, absent by capability, and unsupported.

**Gate:** Freshness canary passes repeatedly under concurrency and after failures; race detector is green.

### Task 7: Add NumPy to the canonical artifact

**Objective:** Extend the already-proven neutral guest with a verified NumPy subset without changing the public run contract.

**Files:**

- Create: `guest/build/build-numpy.sh`
- Modify: `guest/build/build-guest.sh`
- Modify: `guest/build/sources.lock.json`
- Create: `guest/tests/numpy_smoke.py`
- Create: `integration/e2e/numpy_guest_test.go`
- Create: `docs/numpy-support.md`

**Process:**

1. Pin official NumPy source and all patches by digest.
2. Independently implement or explicitly license every build patch/script.
3. Link only the required static extension surface.
4. Pack required pure-Python files.
5. Record supported/unsupported APIs; do not stub failures as success.
6. Test deterministic array creation, shape, sum, matrix operation, and repeated-run freshness.

**Stop condition:** If NumPy requires copying unlicensed build scripts or cannot be reproduced in native Linux CI, keep the pure-CPython artifact canonical and report the blocker.

**Gate:** Canonical artifact imports NumPy and passes deterministic operations under Go/wazero; manifest names the exact subset and limitations.

### Task 8: Add the Host capability broker and Python SDK

**Objective:** Let generated Python call one Host-owned read-only batch capability without direct network access.

**Files:**

- Create: `runtime/capability/broker.go`
- Create: `runtime/capability/grant.go`
- Create: `runtime/capability/fetch_many.go`
- Create: `runtime/receipt/receipt.go`
- Create: `guest/bootstrap/agent_runtime/tools.py`
- Modify: `guest/include/agent_runtime_v1.h`
- Modify: `guest/src/runtime.c`
- Create: `integration/e2e/fetch_many_test.go`

**Import ABI direction:** Guest supplies a bounded request buffer and a Host-configured bounded response buffer. The Host returns a non-negative response length or a versioned negative error code. The guest never supplies credentials and cannot ask for an arbitrary URL outside the Host allowlist.

**TDD cases:**

- no grant denied;
- matching grant succeeds;
- wrong capability name denied;
- arbitrary destination denied;
- per-call and total-call budgets enforced;
- response cap enforced before overflow;
- partial failures stable and structured;
- timeout cancels and discards unhealthy instance;
- deterministic receipt identity;
- direct guest network attempt fails.

Use a deterministic local HTTP fixture in automatic CI. Put one allowlisted external HTTP/JSON proof behind explicit `workflow_dispatch` so third-party availability cannot make every PR flaky.

**Gate:** Every internal operation has a bounded receipt, while secrets and sockets remain Host-owned.

### Task 9: Add CLI and canonical Agent workflow evidence

**Objective:** Provide one runnable entry point and evidence for the intended compound workflow.

**Files:**

- Create: `cmd/apyrun/main.go`
- Create: `integration/e2e/agent_workflow_test.go`
- Create: `integration/fixtures/fetch_many/`
- Create: `tools/run-agent-workflow.sh`
- Create: `docs/evidence/README.md`
- Generate: `docs/evidence/<date>-agent-workflow.json`
- Generate: `docs/evidence/<date>-agent-workflow.md`

**Workflow:**

```text
one outer Python run
→ five allowlisted Host reads
→ Python/NumPy filters and combines results
→ intermediate payloads stay outside model context
→ bounded final JSON + per-call receipts
→ next run proves freshness
```

**Measurements:** model-visible tool calls, underlying HTTP operations, intermediate bytes, final bytes, guest time, end-to-end time, first/steady request, server-ready time, correctness, and next-run freshness. Do not claim fewer underlying operations when only model-visible calls decrease.

**Gate:** Raw JSON is canonical; Markdown is generated from it; exact source/artifact digests are linked.

### Task 10: Reproducibility, hardening, and release decision

**Objective:** Decide whether the project is ready for an immutable private release artifact, without publishing automatically.

**Files:**

- Create: `.github/workflows/reproducibility.yml`
- Create: `tools/compare-builds.py`
- Create: `docs/evidence/<date>-reproducibility.json`
- Create: `docs/release-checklist.md`

**Checks:**

- two cold builds;
- artifact and normalized manifest comparison;
- complete import/export diff;
- SBOM/notices review;
- full Go/race/denial/freshness/E2E gates;
- artifact size and pool memory footprint;
- source secret/license scan;
- no unsupported security or speed claims.

**Gate:** Stop for user approval before creating a tag, GitHub Release, public repository, package, deployment, or hosted service.

## 4. Commit sequence

Use coherent signed commits rather than one large implementation commit:

```text
docs: freeze runtime and provenance contracts
ci: add cheap contract gates
ci: build neutral Python WASI guest
feat: execute guest through minimal wazero host
feat: restore prepared guest state across runs
feat: add verified NumPy guest profile
feat: add read-only capability broker
feat: add canonical agent workflow proof
ci: add reproducibility gate
```

Push after each verified coherent slice. A pushed commit is not complete until its corresponding Actions run has been inspected.

## 5. Stop conditions

Stop and report instead of weakening the design when:

- source/build-script licensing is unclear;
- an input is available only through an unpinned mutable asset;
- exact ABI imports/exports cannot be verified;
- the guest needs ambient filesystem/network/env authority;
- a requested hard limit is unsupported by wazero;
- mutable state exists outside the reset contract and cannot be rejected or reset;
- NumPy cannot be reproduced from licensed, pinned inputs;
- CI succeeds only through a warm cache and fails cold;
- reproducibility differs without a documented, normalized source of nondeterminism;
- the work expands into an Agent planner, MCP marketplace, write-side transaction system, distributed scheduler, or general Linux sandbox;
- publication, deployment, paid infrastructure, or account-sensitive configuration beyond the approved GitHub repository is required.

## 6. Definition of done for the first vertical slice

The first useful milestone is not “CI uploaded a wasm.” It is complete only when:

- the private GitHub repository and least-privilege workflows exist;
- ABI/schema/provenance fixtures pass;
- CI builds a real neutral pure-CPython WASI guest from pinned licensed inputs;
- manifest, SHA-256, notices, and SBOM accompany it;
- a Go/wazero host consumes that exact Actions artifact;
- real Linux E2E proves execution, bounded errors, denial of ambient authority, cancellation, and next-run freshness;
- signed commits are pushed and the corresponding workflow run IDs are recorded.

Only then proceed to NumPy and `fetch_many`.