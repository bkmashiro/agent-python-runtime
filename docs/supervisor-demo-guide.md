# Supervisor demo guide

Status: **Local rehearsal guide.** The execution examples use the real `apyrun`
and a verified CPython/WASI Guest. The Lab Web viewer is a read-only consumer of
canonical Lab v1 fixtures; it is not a live ingestion service.

## Goal

In five minutes, show one bounded Agent program crossing typed Host authority,
then use Lab views to explain the resulting evidence. The argument is:

> Pysolate lets an Agent use ordinary Python for control flow while the Host
> freezes authority, records every external interaction, and owns the durable
> state transition.

Do not frame the demo as a faster Python runtime, a replacement for every VM, or
a production-ready experiment service.

## Preflight

Use a verified Guest distribution containing the WASM artifact and its three
sidecars. Keep its absolute path in a local environment variable; do not paste
private Host paths into slides, screenshots or committed output.

```bash
cd /Users/yuzhe/projects/agent-python-runtime
export AGENT_RUNTIME_GUEST=/absolute/path/to/agent-python-runtime.wasm

test -f "$AGENT_RUNTIME_GUEST"
test -f "$(dirname "$AGENT_RUNTIME_GUEST")/manifest.json"
test -f "$(dirname "$AGENT_RUNTIME_GUEST")/import-inventory.json"
test -f "$(dirname "$AGENT_RUNTIME_GUEST")/import-qualification.json"

go test ./integration/e2e -run '^TestRealGuest' -count=1
```

Build the two local CLIs once before the meeting:

```bash
mkdir -p /private/tmp/pysolate-demo-bin
go build -o /private/tmp/pysolate-demo-bin/apyrun ./cmd/apyrun
go build -o /private/tmp/pysolate-demo-bin/pysolate-research ./cmd/pysolate-research
```

Run the teaching examples once before presenting:

```bash
python3 examples/controller-boundaries/run.py \
  --artifact "$AGENT_RUNTIME_GUEST"
```

Expected call/receipt pairs are `0/0`, `1/1`, and `2/2`.

The Lab Web worktree has its own start sequence:

```bash
cd /Users/yuzhe/projects/agent-python-runtime-lab-web/apps/lab-web
npm test
npm run build
npm run serve -- --port 4173
```

Open `http://127.0.0.1:4173`. Confirm the page labels itself as a canonical
fixture-backed, read-only viewer rather than live Runtime integration.

## Five-minute route

### 0:00–0:40 — The tension

Show `examples/controller-boundaries/03-two-sources.py`.

Suggested script:

> An agent often needs program control flow, but giving it a general computer
> also gives it ambient authority: a shell, arbitrary processes, network and
> persistent hidden state. Pysolate separates those concerns. The agent submits
> ordinary Python; the Host selects the runtime, workspace, limits and exact
> capabilities before a fresh Guest starts.

Point out that the program names `sources.demo_catalog()` and
`sources.benchmark_manifest()` but contains no URL, credentials, transport
method or Host path.

### 0:40–1:40 — Execute the program

Run:

```bash
cd /Users/yuzhe/projects/agent-python-runtime
python3 examples/controller-boundaries/run.py \
  --artifact "$AGENT_RUNTIME_GUEST"
```

Focus on the third line:

```text
capability_calls=2 · receipts=2 · exact result accepted
```

Suggested script:

> The outer controller submits one Run. Inside that Run, the Guest performs two
> separately authorised Host reads and joins them with ordinary Python. The two
> calls do not disappear: both remain schema-checked and individually
> receipted.

Do not present the first two lines as benchmarks. They are teaching controls:
pure local computation and a one-source tie.

### 1:40–3:00 — Inspect one Run in Lab

Open the ordinary Run detail and timeline.

Show these as independent fields:

- task status;
- oracle status;
- evidence completeness;
- artifact, execution-profile and capability-plan references;
- ordered lifecycle and capability events;
- private/unavailable object references.

Suggested script:

> A normal runner tells us the returned JSON. The Lab projection relates that
> outcome to the admitted artifact, profile, authority plan, ordered Host calls
> and workspace identities. Success and evidence completeness are deliberately
> separate: a task can complete while some research evidence is unavailable.

State the viewer boundary once:

> This browser is currently a read-only consumer of canonical Go-produced Lab
> v1 fixtures. The execution I just ran is live; automatic ingestion into the
> browser remains future Lab work.

### 3:00–4:10 — Compare placement

Open the comparison view or show the evaluation table:

| Workload shape | Direct boundaries | Guest boundaries | Typed calls |
|---|---:|---:|---:|
| one Host source | 1 | 1 | 1 in each condition |
| two Host sources | 2 | 1 | 2 in each condition |

Suggested script:

> Pysolate does not make loops or filtering inherently cheaper. The one-source
> control is a tie. The change appears when one admitted program coordinates
> multiple Host capabilities: outer orchestration is consolidated, while the
> underlying authority crossings remain visible.

### 4:10–4:45 — Replay and branch

Show the branch DAG briefly.

Suggested script:

> A captured external-read transcript can drive a second fresh Guest offline.
> A counterfactual branch can replace a Host-owned result at a capability
> boundary and compare the resulting state. This is re-execution from the
> original state, not a Python heap or VM snapshot. Playback is Current for the
> two curated reads; branching is Experimental.

### 4:45–5:00 — Close

> The contribution is not another Python sandbox UI. It is a capability-native
> execution boundary where ordinary programs produce inspectable,
> evidence-bound state transitions. The current prototype establishes that
> mechanism; broader capability coverage and effect handling are the next
> product questions.

## Demonstration truth table

| Surface | Demonstration status | Accurate wording |
|---|---|---|
| Three Python examples | Live, local real Guest | Real `apyrun` execution with loopback Host fixtures |
| Capability calls and receipts | Live | Host-authored counts and receipts from the Run |
| Lab Web views | Canonical fixture-backed | Strict read-only Lab v1 consumer |
| Playback acceptance | Live local command available | Fresh live capture followed by fresh offline Guest |
| Branch DAG | Experimental canonical relation | Counterfactual research view, not source-level debugging |
| Evaluation v2.1 | Frozen local study | Five pinned workloads, one repetition, mechanism-only |

## Questions and short answers

### Why not use Docker, a VM or Jupyter?

Those are useful compatibility environments, but their default abstraction is a
persistent general computer. Pysolate begins with a fresh program execution,
explicit file state and a frozen set of typed Host capabilities. A separately
governed Computer remains appropriate for irreducibly native, interactive or
long-lived work.

### Why not let the Agent call tools directly?

It can, but then the controller owns each orchestration step. Pysolate permits a
bounded program to compose several typed calls internally while the Host still
owns targets, credentials, budgets and receipts. The evaluation shows this
placement difference only for qualified multi-call workloads.

### Are capability calls reduced?

No. The two-source example makes two calls in both conditions. The change is
who coordinates them: two outer Direct boundaries versus one Guest Run. This is
an orchestration-placement result, not a cost or latency result.

### What does a digest prove?

Equality with the canonical bytes later supplied under the same domain. It does
not prove semantic correctness, authorship, authorization or safe export.

### Is replay deterministic?

Playback strictly supplies captured, schema-validated capability results to a
fresh Guest and checks final identities. It does not repeat real-world effects
or establish universal determinism. The separate deterministic-verification
profile is Experimental/Partial.

### Can the Guest request more authority?

No capability or grant is added after startup. A Python exception cannot trigger
an automatic broader rerun. A later Run may receive a different Host-approved
plan, but that is a new admission decision.

### Is branching a snapshot?

No. A child starts a fresh Guest from the original request and initial
workspace, strictly replays the prefix, then follows a Host-owned suffix policy.
No Python heap, stack, module globals or WASM memory become continuation state.

### Is Lab already integrated with live Runs?

The Runtime contracts, research store/operator, canonical Lab v1 projection and
fixtures exist. The browser viewer is fixture-backed and read-only. Durable
live ingestion, transport, query and access control remain Proposed.

### Why no SQLite database?

The current bounded prototype did not demonstrate indexed-query or atomic
multi-record needs sufficient to justify another backend and migration policy.
LabStore therefore remains a local typed filesystem CAS. A future Lab service
may revisit SQLite when concrete query/concurrency requirements exist.

### What has not been demonstrated?

Production readiness, broad workload coverage, latency or token savings,
economic advantage, model-quality improvement, universal replay, arbitrary
external-write safety and multi-user Lab operation.

## Offline fallback

If live execution fails:

1. do not improvise a different artifact or broaden permissions;
2. show the previously verified expected output in
   `examples/controller-boundaries/README.md`;
3. continue with the canonical Lab fixture viewer;
4. state that the live prerequisite failed and that the displayed viewer remains
   fixture-backed.

If the browser fails, use `pysolate-research` CLI inspection and the checked-in
canonical JSON fixtures. Do not describe either fallback as a live connected
Lab.
