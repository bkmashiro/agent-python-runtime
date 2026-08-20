# Operator CLI

Status: `cmd/apyrun` is the **Current** Run entry point. The separate
`cmd/pysolate-research` surface is an **Experimental** local artifact
inspection/planning/measurement tool. It does not execute branch Runs.

`apyrun` reads one `RunRequest` from stdin and writes one `RunResponse` to
stdout.

## Flags

```text
-artifact  required path to agent-python-runtime.wasm
-manifest  required when execution_profile is configured
-config    optional Host-owned JSON configuration
```

## Host configuration

```json
{
  "timeout_ms": 20000,
  "max_request_bytes": 1048576,
  "max_response_bytes": 1048576,
  "memory_limit_pages": 8192,
  "program_surface": "programmatic",
  "execution_profile": {
    "id": "base",
    "allowed_imports": ["csv", "json"]
  },
  "workspace_files": {
    "input.txt": "hello"
  },
  "max_tool_calls": 8
}
```

All fields are optional. Unknown fields and trailing JSON are rejected. Resource fields default to the values in `runtime.DefaultRunConfig`.

`execution_profile` and `-manifest` must appear together. The CLI reads the manifest-selected import inventory and qualification sidecars, verifies the artifact, then replaces any Agent compatibility bookkeeping with Host-derived static imports.

## Experimental deterministic verification

An exact artifact/profile may opt into the bounded Experimental/Partial profile:

```json
{
  "execution_profile": {
    "id": "base",
    "allowed_imports": ["datetime", "sys"]
  },
  "deterministic_verification": {
    "status": "experimental_partial",
    "random_seed": "study-1"
  }
}
```

The `-manifest` distribution verification is mandatory. The Host binds the
seed and exact artifact into the deterministic-profile identity; Agent input
cannot choose either. Mounted `workspace` config is rejected, as are statically
identified concurrency and locale import classes. This controls the wazero
random and clock interfaces only within the documented qualified boundary; it
does not make arbitrary Python, live external reads, floating-point behavior
across platforms, or a complete Agent deterministic. See
[research/deterministic-verification.md](research/deterministic-verification.md).

When `workspace_files` is present, the Host seals the workspace capabilities but
does **not** expose Python wrappers by default. Programmatic tool calling is an
independent, Host-owned opt-in:

```json
{
  "program_surface": "programmatic",
  "workspace_files": {"README.md": "example"}
}
```

`program_surface` accepts `direct | programmatic | both` and defaults to
`direct`. `direct` projects only Agent-facing tool schemas; `programmatic`
projects only parent-bound Python wrappers into the WASM Guest; `both` projects
both from the same sealed Plan. `programmatic` and `both` are rejected unless
Host tools are configured. They enable neither approval, playback, caching nor
cold continuation.

The programmatic workspace projection provides:

```python
read_text(path)
write_text(path, content)
list_files()
```

The workspace is in memory. `max_tool_calls` defaults to eight. It does not grant a Host path, socket, subprocess or package installation.

## Mounted workspace and complete capsule storage

As an alternative to `workspace_files`, the Host config may provision a rooted `/workspace` from a validated Host directory snapshot or a complete capsule:

```json
{
  "workspace": {
    "input_capsule": "/absolute/host/state.pwc",
    "output_capsule": "/absolute/host/next-state.pwc",
    "disposition": "export_on_success"
  }
}
```

`source_directory` may replace `input_capsule`; omitting both creates an empty workspace. `disposition` is mandatory: `export_on_success` and `export_on_response` require `output_capsule`, while `discard` forbids it. All configured paths must be clean and absolute. `workspace` and `workspace_files` are rejected together rather than creating two inconsistent state planes. The Agent request cannot select input/output paths, disposition policy, or workspace limits.

The Guest accesses this state with ordinary Python file APIs under `/workspace`. `/tmp` remains per-Run scratch. Every bounded response includes a Host-authored disposition receipt with request, initial state, final state and optional exact capsule identities. Output capsules are complete, deterministic storage artifacts and are atomically published with mode `0600`; they are not mounted in place or backed by SQLite. See [workspace-capsules.md](workspace-capsules.md).

## Bounded developer operations

Agent code uses ordinary Python filesystem APIs against the durable
`/workspace` mount and the separate per-Run `/tmp` scratch mount. These calls
lower through CPython/WASI into bounded Host mounts; they are not typed Broker
calls and do not expose a shell or subprocess.

A Host may separately configure `git_read` with an opaque repository ID, one
clean absolute local repository path, and explicit entry/patch/blob bounds.
This generates `git.status/diff/log/show/list_refs/resolve_revision`; the private
repository path never appears in the Guest projection, spec, grant or result.
The adapter uses `go-git` in-process and exposes no hooks, external filters,
credential helpers, remote URL or network operation. See
[developer-tools.md](developer-tools.md) for the current snapshot-coherence
boundary.

## Request

```json
{
  "run_id": "demo",
  "code": "result = inputs['value'] + 1",
  "inputs": {"value": 41}
}
```

Agent-facing callers should omit `compatibility`; the Host derives it. `run_id` is an untrusted diagnostic label, not an authority identifier.

When typed Host tools are configured, the Host canonicalizes their versioned `CapabilitySpec` definitions, compiles strict input/output schemas, derives opaque `CapabilityGrant` identities from Host-owned per-Run policy documents, generates trusted Python module/method objects plus optional aliases and direct tool schemas, and seals the sorted specs, grants and total call budget as `pysolate.capability-plan.v7` before Guest startup. The response carries `capability_plan_sha256` even when no tool is called, and every capability receipt binds the same identity. Guest-authored plan or grant evidence is rejected.

## Current curated information sources

`information_sources.demo_catalog` and
`information_sources.benchmark_manifest` configure two dedicated,
credential-free structured sources:

```json
{
  "program_surface": "programmatic",
  "information_sources": {
    "demo_catalog": {
      "endpoint": "http://127.0.0.1:8081/catalog",
      "timeout_ms": 1000,
      "max_response_bytes": 65536
    },
    "benchmark_manifest": {
      "endpoint": "http://127.0.0.1:8082/manifest",
      "timeout_ms": 1000,
      "max_response_bytes": 262144
    }
  },
  "max_tool_calls": 2
}
```

Each Host config must provide an exact `http` or `https` endpoint, positive
timeout and bounded response size. The generated Agent surfaces are
`sources.demo_catalog()` and `sources.benchmark_manifest()`; no URL or
transport argument crosses the Guest boundary. Adapters perform GET only,
disable environment proxies and compression, refuse redirect following, and
require status 200 plus UTF-8 `application/json`. The benchmark source applies
its dedicated nested version/schema, semantic-ID, metric and size checks. Both
may coexist with a mounted workspace; neither provides generic HTTP.

## Capture and strict playback

Capture can additionally configure `"playback":{"mode":"capture","output_bundle":"/absolute/run.playback.json"}`. The Host stages the canonical minimal bundle with mode `0600` and publishes it atomically only after runner close, final response validation and workspace inspection. A fresh offline Run uses `"playback":{"mode":"playback","input_bundle":"/absolute/run.playback.json","expected_bundle_sha256":"sha256:<capture identity>"}` with the same Host source policy and request. The independently supplied identity anchors the Host-protected artifact; playback constructs no HTTP adapter, consumes the sealed transcript through the Broker, rejects unused/mismatched records, and verifies response status plus final result/workspace identities. Playback Bundle and Workspace Capsule outputs are rejected together. See [playback-bundles.md](playback-bundles.md).

## Experimental branch execution

`apyrun` can consume a Host-protected branch manifest and publish a distinct
child Bundle:

```json
{
  "program_surface": "programmatic",
  "information_sources": {
    "demo_catalog": {
      "endpoint": "http://127.0.0.1:8081/catalog",
      "timeout_ms": 1000,
      "max_response_bytes": 65536
    },
    "benchmark_manifest": {
      "endpoint": "http://127.0.0.1:8082/manifest",
      "timeout_ms": 1000,
      "max_response_bytes": 262144
    }
  },
  "playback": {
    "mode": "branch",
    "input_bundle": "/absolute/protected/parent.playback.json",
    "expected_bundle_sha256": "sha256:<trusted parent identity>",
    "input_branch_manifest": "/absolute/protected/child.branch.json",
    "expected_branch_sha256": "sha256:<trusted manifest identity>",
    "output_bundle": "/absolute/protected/child.playback.json"
  }
}
```

The request, artifact/profile, initial workspace, child capability Plan and
Grants must match the manifest before Guest startup. The child is a fresh Guest
that strictly consumes the parent prefix and the manifest's Host-owned suffix
policy. Only captured external reads are branchable. The child result/workspace
may diverge from the parent and is captured as a new Bundle. Workspace Capsule
output remains mutually exclusive with child Bundle output. This is not a
source-line breakpoint or heap/WASM-memory restore.

## Current observation API boundary

The Runtime exposes `pysolate.runtime-observation.v1` to Go Hosts through an
`observe.Session` attached to Run context alongside a matching Host
`InvocationRef`. `apyrun` does not currently expose Recorder/session config and
does not persist an observation stream. A Harness embedding Runtime can select
`off`, `best_effort`, or `required`; see
[research/runtime-observation.md](research/runtime-observation.md).

## Experimental local research CLI

`pysolate-research` reads protected canonical artifacts and defaults to
semantic human output. `-json` emits bounded full-identity records for a future
UI. Inputs and outputs must use canonical absolute paths and protected regular
files/directories.

```text
pysolate-research inspect -bundle PARENT [-max-calls N] [-json]
pysolate-research compare -left PARENT -right CHILD [-max-calls N] [-json]

pysolate-research branch plan \
  -parent PARENT -fork 1 -mode override \
  -override-result ALTERNATE_RESULT_JSON -output NEW_MANIFEST [-json]

pysolate-research branch dag \
  -parent PARENT -manifest MANIFEST -child CHILD \
  [-max-nodes N] [-json]

pysolate-research store stats -root EXISTING_STORE [-json]
pysolate-research store benchmark -root NEW_DESTINATION [fixture flags] [-json]
```

`branch plan` uses a zero-based capability operation. It authors a canonical
`0600` manifest without overwriting an existing path. Override JSON is
canonicalized when planning and is revalidated against the sealed capability
schema when the branch executes. `recorded_suffix` uses `-suffix-bundle`; a
different already sealed authority binding can be selected with
`-child-binding-bundle`. `live_suffix` embeds no result tape.

`branch dag` displays caller-supplied parent/manifest/child relations. It
validates child admission identities and Grants, the exact parent prefix, and
the complete suffix tape for override and recorded-suffix branches. A live
suffix exposes no sealed result tape, so only admission and prefix can be
validated. This rejects unrelated child Bundles but does not independently
prove that the child's result was produced by executing the manifest. The
protected branch outcome relation remains Host evidence. Fresh branch
execution is available through `research/operator.RunBranch`, not this CLI.

`store stats` opens an existing LabStore read-only and performs no creation,
migration or repair. `store benchmark` deliberately creates a new synthetic
destination and measures long, branch, swarm and low-reuse fixtures. Neither
command is a production research database or orchestration service. See
[research/lab-boundary.md](research/lab-boundary.md).

## Exit behavior

- `0`: a structured Guest response was written;
- `1`: runtime or I/O failure;
- `2`: invalid request, config, artifact or source admission;
- `3`: a requirement requires escalation outside Pysolate.

Diagnostics are short and do not include credentials, Host file contents, private model traces or Python source.
