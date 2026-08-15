# Agent development trajectory v0

Status: **Implemented private recorder and static inspector; live Harness attachment pending**

## Goal

Pysolate Lab development mode follows the same core invariant as the DeepSeek Harness
Trajectory surface:

> **Model-visible means logged.**

The reference was inspected at immutable upstream commit
`47f943859bef60e4160492346772ded9b24f765a`. Pysolate adopts the append-only session and
exact model-history idea, request headers, raw assistant chunks and source-event citations;
not DeepSeek's plugin kernel, `surfaceOp` reducer or wire compatibility. DeepSeek's upstream
session file is sequence-validated but is not a cryptographic hash chain; Pysolate's private
development log deliberately adds hash chaining and a sealed materialized export.

A trajectory is the authoritative development record, not a projection reconstructed from
Runtime metrics. Model requests, future resume/fork/replay and the UI must consume one
ordered event history.

## Private storage

The recorder has two layers:

1. a mode-`0600`, append-only JSONL event stream;
2. complete bodies in the existing private content-addressed `research/labstore`.

Every event contains:

- a contiguous sequence and opaque event identity;
- the previous event SHA-256 (the first points to the sealed session header);
- a canonical event SHA-256;
- source, type, actor, turn, step and parent identity;
- optional complete private body reference;
- exact model-context event IDs for `model.request`;
- exact raw-source event IDs for assembled reasoning/output/tool calls and tool results;
- tool call, child session, Run, logical request, physical execution and span links;
- provider/model/status/finish/token/timing metadata when observed.

Bodies are immutable and domain-separated by kind (`prompt`, `provider-body`, `tool-payload`,
`code`, `file`, or canonical metadata). Credential-bearing values are rejected at admission.
Callers must make an explicit `CredentialsAbsent` declaration; the store does not pretend it
can detect every possible secret in arbitrary bytes.

## Event vocabulary

- lifecycle: `session.start`, `session.end`, `turn.start`, `turn.end`, `step.start`, `step.end`;
- model input: `context.inject`, `user.message`, `request.header`, `model.request`;
- model output: raw `assistant.chunk`, assembled `assistant.reasoning`, `assistant.output`;
- tools: `tool.call`, `tool.result`;
- agents: `subagent.dispatch`, `subagent.result`;
- execution: `runtime.event`, `workspace.change`.

The vocabulary is intentionally closed for v0. Unknown fields, event types or sources fail
closed rather than becoming compatibility bags.

## Exact model context

A `model.request` lists `context_event_ids` in the exact order submitted to the provider.
Validation requires every referenced event to:

- exist earlier in the same session;
- appear at most once;
- carry an available body;
- preserve the declared order.

The Lab shows these bodies under **Exact model context**. It does not infer context by taking
all preceding events. This permits truthful context compaction and injection while retaining
what was omitted and why elsewhere in the trajectory.

`request.header` records the effective request-level system/tool-catalog/configuration body.
Every raw provider stream item is appended as `assistant.chunk` before the assembled
reasoning/output/tool event. `source_event_ids` must cite only earlier chunks, and every tool
result must cite its matching tool-call event. The inspector therefore exposes both raw
provider order and the exact assembled object used by later history.

Provider-visible reasoning or reasoning summaries are recorded as
`assistant.reasoning`. Provider-internal chain-of-thought that was never returned remains
unobservable and must not be invented.

## Tool and Runtime correlation

Each `tool.call` mints a stable call identity. `tool.result`, `runtime.event` and
`workspace.change` may refer only to an earlier known call. Runtime events additionally carry
Host-owned Pysolate identities:

```text
tool_call_id
  -> logical_request_id
  -> run_id
  -> physical_execution_id
  -> span_id
```

This keeps Agent intent and physical effect truth separate while making the complete causal
chain inspectable.

## Browser export

`Export` first reopens and validates the JSONL hash chain and every body reference, then
materializes complete bodies into a private browser document. The closed
`public/lab-data/index.json` selects between two v0 sessions:

- `experiment.json`: the measured balanced-order real-Guest workflow experiment, projected
  from sealed v1 evidence; its provider-labelled reasoning/output remains scripted;
- `trajectory.json`: a credential-free scripted fixture that exercises memory, skill and
  subagent records.

Neither file is evidence of a live provider run or hidden chain-of-thought. The real-Guest
trajectory is evidence of the measured Guest/Runtime/tool execution and complete Harness
fixture bodies only.

The former `pysolate.lab-web-debugger.v4` and paired-workflow Lab schemas were deleted. There
is no compatibility reader, fallback or dual surface.

## Remaining integration boundary

The recorder and inspector are real; the current checked-in trajectory is scripted. A live
Harness adapter must append events at the actual boundaries before the Lab can claim complete
capture of a real agent run:

1. after final prompt/context assembly and before provider send;
2. for every provider-returned reasoning/output/tool-call chunk;
3. before and after tool execution;
4. at subagent dispatch/result and context injection;
5. at Pysolate logical/physical/workspace lifecycle boundaries.

Resume, fork, replay and live tailing remain future Harness operations. They must use this
same event stream, not introduce a second transcript database.
