# Curated Information Source Capture/Playback Megagoal — Closed Evidence Record

> **Status:** Closed at signed commit `38e06f2bf0904aa2d890fae940aaaf220c795685`; the verified implementation was pushed and synchronized with `origin/main`. This file preserves the implementation decisions and evidence. Do not add new work tracks or rerun its completed worker prompts.

**Goal:** Deliver Pysolate's first complete auditable capability-computer vertical: a Host-granted, typed curated information source backed privately by an exact HTTP endpoint, captured into a minimal protected Playback Bundle, replayed offline in a fresh CPython/WASI Run, and verified by result and workspace identities.

**Architecture:** Agent Python calls a documented source method such as `sources.demo_catalog()`; it never receives a generic HTTP primitive. A canonical CapabilitySpec describes the typed source while a separate per-Run CapabilityGrant binds the Host-owned endpoint policy. The existing generic WASI `host_call` remains the only Guest/Host capability boundary. Broker middleware validates, captures, or plays back the same capability protocol, while HTTP remains a private live-handler implementation.

**Tech Stack:** Go 1.25+, wazero, real CPython/WASI Guest, `net/http`, existing jsonschema/v6 dependency, canonical JSON/SHA-256, existing Workspace Capsule and disposition machinery. Do not add a runtime dependency unless a concrete blocker proves the standard library insufficient.

---

## Source of truth and execution authority

- Repository: `/Users/yuzhe/projects/agent-python-runtime`
- Branch: `main`
- Product direction: `docs/product-direction.md`
- Threat model: `docs/threat-model.md`
- Workspace semantics: `docs/workspace-capsules.md`
- Closed evidence roadmap: this file
- Active successor: `.hermes/plans/2026-08-12_025644-research-playback-branching-determinism-megagoal.md`
- The user authorizes signed commits after each coherent verified stage.
- Push `main` only once, after the complete required megagoal passes the full gate and independent final review.
- Do not use Docker, paid cloud, production credentials, or public network tests.
- Do not reset or discard a live worktree. Inspect it and continue from the actual state.

## Product boundary

### Required product promise

```text
Host-owned curated source grant
  → generated documented Python source method
  → exact private HTTP endpoint adapter
  → validated structured result
  → Host-authored receipts and transport evidence
  → minimal Playback Bundle
  → network-disabled fresh playback Run
  → matching result and final-workspace identities
```

### This is not generic HTTP

The Agent must not submit a URL, path, query string, method, headers, redirect policy, credential, timeout, or byte budget. The first source is a dedicated, named, documented internal tool. HTTP is private transport behind its Host adapter.

### Explicitly out of scope

- Agent-facing `http.get`, generic URL access, domain allowlists, arbitrary path/query, POST/PUT/PATCH/DELETE;
- credentials, cookies, arbitrary headers, browser automation, shell, subprocess, ambient sockets or Host paths;
- write effects, retries after ambiguous outcomes, Effect Plane, reconciliation, transactions;
- plugin marketplace/discovery, package installation, VM fallback, pinned interpreter sessions;
- putting Agent source, prompts, final result bodies, workspace bodies or credentials into the Playback Bundle;
- claiming arbitrary Python determinism or generic external-effect replay.

### Effect classification rule

Never infer side-effect class from HTTP method or status. The live source is explicitly declared `external_read`. HTTP status is outcome evidence only. Any future POST/write prototype must declare effect class, idempotency, ambiguous-outcome behavior and reconciliation independently; it remains exploratory until separately approved.

## Definition of Done

All boxes below must be checked with real output before the required megagoal is complete:

- [x] CapabilitySpec v2 worktree is independently post-fix reviewed, signed, and committed without losing current changes (`d36ef5f2a41f9023244b5494f41c7589a79948b0`).
- [x] A distinct per-Run grant/policy identity is canonicalized and bound by `pysolate.capability-plan.v3`; changing policy changes the plan identity.
- [x] CapabilitySpec generates module/object Python projections and defensive direct Agent-facing tool schemas without creating a second authority path (`pysolate.capability-plan.v4`).
- [x] One dedicated `sources.demo_catalog()` works through a private exact-endpoint HTTP adapter with GET-only, redirect denial, status/content-type, timeout and byte limits.
- [x] Source capability coexists with a Host-mounted workspace; typed in-memory workspace tools remain mutually exclusive only with the mount.
- [x] A canonical minimal Playback Bundle is staged `0600`, synced, validated, and atomically published only after a bounded Host response and successful runner close.
- [x] Playback mode performs zero network calls and strictly consumes the recorded sequence.
- [x] Plan/grant/request/call order/capability/request digest/result/tail/unused-entry tampering fails closed.
- [x] Live capture followed by server shutdown and fresh playback yields the same Agent result digest and final workspace identity.
- [x] Bundle inspection proves it contains no Agent source, prompt, final result body or workspace body.
- [x] Current/Partial Current/Proposed documentation is accurate.
- [x] Full Go, race, real Guest, Python, ABI/document and diff gates pass on final candidate `38e06f2bf0904aa2d890fae940aaaf220c795685`.
- [x] Independent final review reports no blocker. Post-fix review at `38e06f2bf0904aa2d890fae940aaaf220c795685` independently re-authored status with a recomputed self-hash and confirmed the original trusted bundle identity rejects it before Guest/network use.
- [x] Only then is `main` pushed, with local HEAD equal to `origin/main` and a clean worktree (`38e06f2bf0904aa2d890fae940aaaf220c795685`).

## Stop conditions

Stop and report instead of weakening the design if any of these occur:

1. A product decision is required outside the frozen boundary.
2. Live and playback paths would require two different authority semantics or plan identities.
3. Endpoint/target policy cannot be identity-bound without exposing credentials or secrets.
4. Bundle publication would overwrite a prior valid artifact on failure.
5. Supporting simultaneous output Capsule and Playback Bundle would require pretending two file renames are transactional. For v1, prohibit the conflicting publication combination or use workspace `discard` while still comparing Host-computed final identity; do not invent false atomicity.
6. Offline playback would still initialize or contact the network.
7. A real CPython/WASI check cannot be run and no honest local alternative exists.
8. A blocker cannot be fixed within the agreed architecture.

## Commit and release policy

Use signed conventional commits after coherent verified stages. Suggested sequence (adapt if the live state already contains a stage):

1. `feat: define canonical capability specs` — close the current CapabilitySpec v2 slice.
2. `feat: bind per-run capability grants` — plan v3 authority closure.
3. `feat: generate capability module projections`.
4. `feat: add curated information source adapter`.
5. `feat: capture capability playback bundles`.
6. `feat: replay captured capability calls`.
7. `test: verify live source playback transitions` or fold tests/docs into the relevant feature commits.

Run targeted tests before every commit. Do not push intermediate commits. At the final gate, review the complete commit range from the starting remote baseline, then push once.

---

## Track 0 — Close the live CapabilitySpec v2 slice

**Product promise:** Start the megagoal from a reviewed, clean canonical-spec baseline rather than stacking new work on an unresolved diff.

**Primary files:**
- `runtime/capability/registry.go`
- `runtime/capability/broker.go`
- `runtime/capability/spec_test.go`
- `runtime/capability/workspace.go`
- `cmd/apyrun/main.go`
- current changed docs/tests

### Tasks

- [x] Inspect `git status`, complete diff, and current targeted/full test evidence; completed from live state.
- [x] Obtain or rerun a narrow read-only post-fix review for invalid UTF-8 canonicalization/envelopes/results and Python builtin/helper/`inputs`/`result` projection collisions; `deleg_9b6f2ff0` passed with no blocker.
- [x] For each real blocker, write a failing regression test first, verify RED, implement the minimum fix, then verify GREEN and race.
- [x] Run the full required baseline gate: Go test/vet/build/race, real Guest E2E race, Host 20 and Guest 50 Python tests, compileall and diff checks passed.
- [x] Sign and commit the complete current slice; verified signature on `d36ef5f2a41f9023244b5494f41c7589a79948b0`. Not pushed.

### Definition of Done

- [x] Invalid UTF-8 cannot be normalized into colliding capability identities or accepted at the Broker/result boundary.
- [x] Generated flat compatibility wrappers cannot shadow bootstrap helpers, Agent `inputs`/`result`, or Python builtins.
- [x] Existing workspace wrappers work under real CPython/WASI.
- [x] Commit signature is valid.

---

## Track 1 — Separate CapabilitySpec from per-Run CapabilityGrant

**Product promise:** The sealed plan identifies both what a capability means and exactly which Host-owned target policy the Run received.

**Likely files:**
- Create: `runtime/capability/grant.go`
- Modify: `runtime/capability/registry.go`
- Modify: `runtime/capability/plan_test.go`
- Modify: `runtime/capability/spec_test.go`
- Modify: `docs/architecture.md`
- Modify: `docs/product-direction.md`
- Modify: `docs/threat-model.md`

### Contract

Keep these concepts distinct:

- `CapabilitySpec`: stable name/version, documentation, input/output schemas, Python/tool projection, handler implementation identity, playback treatment and declared effect class.
- `CapabilityGrant`: per-Run Host policy identity. For the source adapter this binds the exact endpoint policy, redirect behavior, accepted content type/status, time/byte limits and source contract. It must not carry Guest-authored authority.
- `Plan`: sorted specs + sorted grants + global call budget under a new schema version (expected `pysolate.capability-plan.v3`).

The plan may bind a canonical grant digest rather than disclose policy contents, but the Host must retain and verify the canonical policy used to derive it. Do not overload `handler_identity` with per-Run endpoint configuration.

### TDD tasks

- [x] Write failing tests: same Spec + different grant policy identity produces different plan identity.
- [x] Verify registration order does not change identity; missing/ambiguous grants reject.
- [x] Write failing tests: Guest call cannot choose or override grant identity.
- [x] Verify grant slices are defensive and concurrent Register/Seal remains atomic.
- [x] Implement opaque `Grant`, canonical policy hashing and plan v3 canonical document.
- [x] Migrate workspace registrations to a non-target grant identity without binding workspace contents into authority.
- [x] Run capability/full Go tests, vet and targeted race; inspect canonical plan behavior.
- [x] Update Current documentation and sign the stage commit.

### Gate

```bash
go test ./runtime/capability -count=1
go test -race ./runtime/capability -count=1
git diff --check
```

---

## Track 2 — Generate module/object projection and direct tool schema

**Product promise:** One canonical definition produces both Agent Python ergonomics and Agent tool discovery while Host admission remains unchanged.

**Likely files:**
- Modify: `runtime/capability/registry.go`
- Create or modify: `runtime/capability/projection.go`
- Create: `runtime/capability/projection_test.go`
- Modify: `runtime/capability/workspace.go`
- Modify: `cmd/apyrun/main.go`
- Modify: `integration/e2e/agent_poc_test.go`

### Required behavior

- Generate a namespaced object such as `sources.demo_catalog()` from the sealed plan.
- Preserve current `read_text`, `write_text`, `list_files` compatibility aliases.
- Generate a deterministic direct Agent tool schema from the same Spec; do not introduce a second manually maintained schema.
- Validate module/method/alias identifiers, collisions and bootstrap/builtin names before sealing.
- Bind all projection and Agent-description fields that affect Agent behavior into the plan identity.
- Keep the generic WASI `host_call` unchanged.

### TDD tasks

- [x] Write RED tests for deterministic module/method projection across registration order.
- [x] Write RED tests for module/method/alias collisions and code-injection strings.
- [x] Write RED tests proving tool-schema input/output/documentation exactly matches the registered Spec and is returned defensively.
- [x] Implement minimal projection types and deterministic generator; canonical plan shape advanced to v4.
- [x] Add a real Guest E2E using both legacy workspace aliases and namespaced workspace methods.
- [x] Run targeted Go/race/real Guest gates and sign the stage commit.

---

## Track 3 — Add one curated structured information source

**Product promise:** Agent code consumes a documented structured source while all transport authority remains Host-private.

**Likely files:**
- Create: `runtime/capability/source.go`
- Create: `runtime/capability/source_test.go`
- Modify: `cmd/apyrun/config.go`
- Modify: `cmd/apyrun/main.go`
- Create or modify: `cmd/apyrun/source_binding.go`
- Modify: `cmd/apyrun/main_test.go`
- Modify: `integration/e2e/agent_poc_test.go`
- Modify: operator and threat-model docs

### v1 source contract

Use one dedicated demo source capability, named and documented like `sources.demo_catalog()`. The exact result schema should be small and meaningful (for example a bounded list of `{id,title,value}` records). The Host config owns one exact endpoint. The Agent submits no network coordinates.

Required transport policy:

- GET only, exact endpoint only;
- default client created with explicit timeout;
- redirects rejected;
- no cookies, environment proxy, credentials or Guest headers;
- accepted status explicitly declared (normally 200 only);
- accepted media type explicitly declared (`application/json` with controlled charset handling);
- bounded compressed and decoded bytes; avoid decompression surprises or disable automatic compression for v1;
- strict UTF-8 and duplicate/trailing JSON rejection;
- source response validated against the registered output schema;
- generic errors returned to Guest; URL and sensitive Host configuration not leaked;
- receipt/transport evidence records source capability, policy identity, status, byte count and digests—not URL secrets.

### Architecture guard

Register the source Broker even when `workspace_files` is absent. A source capability must coexist with a mounted workspace. Keep mounted workspace vs typed in-memory workspace mutually exclusive; do not make the source capability mutually exclusive with either.

### TDD tasks

- [x] Write config admission tests for unknown source fields, credential-bearing URLs and Host-only endpoint policy.
- [x] Write `httptest.Server` tests for valid structured result, wrong status, redirect, wrong content type and oversized body; Broker output schema rejects malformed structure.
- [x] Prove the Agent request cannot author source catalog, URL or budgets through the strict RunRequest/Broker envelopes.
- [x] Implement the exact endpoint policy and adapter with `net/http` only, environment proxy disabled and automatic compression disabled.
- [x] Register the source Spec/Grant and generated `sources.demo_catalog()` method.
- [x] Add real Guest E2E with a mounted workspace writing selected source data to `/workspace` and exactly one GET.
- [x] Verify zero ambient/public network and sign the stage commit.

---

## Track 4 — Define and publish Playback Bundle v1

**Product promise:** A bounded Host artifact contains exactly what is needed to replay capability results, without becoming a source/prompt/workspace archive.

**Likely files:**
- Create: `runtime/playback/bundle.go`
- Create: `runtime/playback/bundle_test.go`
- Create or modify: `runtime/capability/transcript.go`
- Modify: `runtime/capability/broker.go`
- Modify: `cmd/apyrun/config.go`
- Create: `cmd/apyrun/playback_binding.go`
- Modify: `cmd/apyrun/main.go`
- Create: `docs/playback-bundles.md`

### Minimal bundle contents

A canonical versioned bundle should bind, at minimum:

- bundle schema version and identity;
- capability plan and grant identities;
- Host request digest;
- artifact/profile identities needed to reject incompatible playback;
- initial workspace identity digest when present, but no workspace body;
- ordered operation index, capability, canonical request/digest, validated structured capability result and result digest;
- bounded transport outcome evidence needed for attribution;
- expected Agent result digest and final workspace identity digest if used for verification, but not their bodies.

Do not include Agent code/source, prompts, provider responses, credentials, final result body, workspace files or Capsule payload.

### Publication rules

- Validate and stage the complete canonical bundle with mode `0600`.
- Sync the staged file.
- Publish only after Guest response validation, successful runner close, complete transcript finalization and final Host response byte validation.
- On timeout, infrastructure error, close error, malformed/over-budget final response or bundle validation failure: do not publish and do not overwrite an existing valid bundle.
- Do not claim cross-file atomicity with output Capsule. For v1 either reject simultaneous publication or use workspace `discard` while still emitting final identity.

### TDD tasks

- [x] Write canonical round-trip, order, identity, quota, integer-bound and trailing-byte tests.
- [x] Write privacy tests scanning the bundle for sentinel Agent source/result/workspace strings that must not appear.
- [x] Write Broker capture tests for successful captured calls and verify denied/error/live-only/zero-call paths add no transcript entries.
- [x] Write publication failure tests preserving a prior bundle.
- [x] Implement staged/atomic no-overwrite publication and strict Host config admission.
- [x] Sign the stage commit after targeted tests and diff review.

---

## Track 5 — Offline playback through the same Broker semantics

**Product promise:** Playback supplies recorded validated results without loading live transport or changing capability authority.

**Likely files:**
- Modify: `runtime/capability/broker.go`
- Create or modify: `runtime/capability/playback.go`
- Modify: `runtime/playback/bundle.go`
- Add focused tests in both packages
- Modify: `runtime/engine/wazero/engine.go` only if finalization must be surfaced there
- Modify: `cmd/apyrun/main.go`

### Required invariants

- Same sealed Spec/Grant plan semantics in live and playback modes.
- Playback mode must not construct or invoke the HTTP adapter.
- Match the next record by operation index, capability, canonical request and request digest.
- Revalidate the recorded result against the current output schema before returning it.
- Preserve normal Broker budget, duplicate call ID, receipt and response validation behavior.
- Finalization rejects unused records; missing/extra/reordered records fail closed.
- Bundle plan/grant/request/artifact/profile/initial-state mismatch rejects before Guest execution where possible.

### TDD tasks

- [x] RED tests for successful ordered playback and zero live-handler invocations.
- [x] RED tamper matrix: plan, grant, request, capability, operation order, request digest, result content/digest, extra record, missing record, unused record, trailing bytes.
- [x] RED concurrency/race tests for Broker playback calls.
- [x] Implement minimal capture/playback middleware around the existing validate/receipt path.
- [x] Verify playback result schema and receipt identity parity.
- [x] Sign the stage commit (`1192b698c219dd181e25b2e4d93f4fb02f89916b`; follow-up charset hardening `d7b8a3069a4f759f81fdc4a89db9fe5c5cc4b660`).

---

## Track 6 — Fresh live/capture/playback verification

**Product promise:** Demonstrate a real evidence-bound state transition rather than merely unit-testing serializers.

**Likely files:**
- Create or modify: `integration/e2e/playback_test.go`
- Modify: `integration/e2e/agent_poc_test.go`
- Modify: `docs/operator-cli.md`
- Modify: `docs/product-direction.md`
- Modify: `README.md`

### Required E2E

1. Start a local test HTTP source and count requests.
2. Prepare a Host-owned initial workspace/Capsule or mounted snapshot.
3. Run a fresh real CPython/WASI Guest in live capture mode.
4. Agent calls `sources.demo_catalog()`, selects data and writes a deterministic workspace file.
5. Confirm exactly one HTTP request and publish Playback Bundle.
6. Stop the HTTP source.
7. Run a second fresh real Guest with the same request/artifact/profile/initial state in playback mode.
8. Confirm no network request, same Agent result digest and same final workspace identity.
9. Confirm expected receipt/plan/grant identities.
10. Tamper with each protected dimension and confirm fail-closed behavior.
11. Inspect bundle to prove excluded contents are absent.

### Honest claim

Document this as deterministic verification within the captured capability profile. Do not claim arbitrary Python determinism: clock, randomness, locale and uncaptured inputs remain outside the strong claim unless explicitly controlled.

### Final gates

```bash
go test ./... -count=1
go vet ./...
go build -o /private/tmp/apyrun-megagoal ./cmd/apyrun
go test -race ./... -count=1
AGENT_RUNTIME_GUEST=/private/tmp/pysolate-current-artifact/dist-272811/agent-python-runtime.wasm \
  go test -race ./integration/e2e -count=1
python3 -m unittest discover -s tests -p 'test_*.py'
python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m compileall -q guest tests
git diff --check
```

Also run repository Markdown link/fence checks and a static secret/shell/subprocess/pickle scan. Review the complete commit range and current worktree independently. Fix real blockers and rerun proportional gates. Verify signed commits, clean status, then push once and confirm `HEAD == origin/main`.

---

## Track 7 — Optional spare-time spikes only

This track starts only after Tracks 0–6 are complete, final reviewed, pushed, and the main worktree is clean. It is not part of Definition of Done.

### Rules

- Maximum two spikes, maximum 45 minutes each.
- Use an isolated temporary worktree rooted under `/private/tmp`; never merge or push spike code.
- Save only a concise local report under `.hermes/` with hypothesis, demo command, observed result, risks and recommendation.
- Label every claim `Exploratory`; do not update Current docs or production APIs.
- No credentials, public network, Docker, paid cloud or external writes.
- Do not turn a successful demo into production implementation without explicit user confirmation.

### Candidate spikes, in priority order

1. **Second curated source adapter demo:** prove whether another source can reuse Spec/Grant/projection/playback without framework changes.
2. **Generic GET design spike:** local-only demo comparing exact source contracts with a narrowly allowlisted generic GET. Do not expose it in production.
3. **Explicit effect classification spike:** model `pure`, `external_read`, `idempotent_write`, `conditional_write`, `non_idempotent_write` plus ambiguous outcomes. HTTP status is evidence, never the classifier. No live POST implementation.

### Spike stopping rule

A spike ends after one falsifiable question is answered. Report what would require user confirmation for complete implementation; do not continue into hardening, integration, docs-as-Current or release.

---

## Closed maintenance handoff

Reopen only a focused bug slice when a reproducible regression violates the Current curated-source capture or strict offline-playback contract. New branching, deterministic-verification, research-store, observation and additional-source work belongs exclusively to `.hermes/plans/2026-08-12_025644-research-playback-branching-determinism-megagoal.md`.

Final evidence is recorded above: the real CPython/WASI gate passed with `/private/tmp/pysolate-current-artifact/dist-272811/agent-python-runtime.wasm`; independent post-fix review found no blocker; signed commit `38e06f2bf0904aa2d890fae940aaaf220c795685` was synchronized to `origin/main` with a clean worktree.
