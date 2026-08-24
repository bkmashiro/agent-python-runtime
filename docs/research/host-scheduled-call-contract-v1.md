# Host-scheduled synchronous calls contract v1

Status: **Implemented Experimental, default-off; exact-Guest economics pending**

Baseline source: `fde07939f3d1865a83bfb59b16a3ac16abf46e8f`

This contract narrows the first implementation slice of the
[Host-Scheduled Calls and Immutable Value Reuse Mega-Goal](../plans/2026-08-24-host-scheduled-python-reuse-autonomous-megagoal.md).
It is not a Future API, a planner, a general Python optimizer or a capability batching
system.

## Phase 0 decision

The first split-phase adapter is the existing fixed research capability family
`sources.read(path)` used by the prepared-data and semantic-effect fixtures. It is admitted
only when the sealed Plan provides all of the following:

- `effect_class` is `external_read` or `pure`;
- playback is `live_only`;
- the Python projection is exactly `sources.read(path)` with result field `body`;
- a `PreDispatchContract` qualifies the static resource, plan-epoch freshness, private
  partition, discard disposition, cost and result-byte bounds;
- arguments are one canonical static string path;
- no approval, write, captured-playback or programmatic-parent path is selected.

This is an implementation/evidence adapter, not a new public dataset API. Its purpose is to
prove that ordinary synchronous Python can overlap two qualified reads without exposing a
Future or charging a logical call that Python never reaches.

The first Stratum-derived data operation is separately fixed to the existing
`numpy_npy_c_v1` prepared-data research family:

```python
import io
import numpy as np
dataset = np.load(io.BytesIO(open('/workspace/input.npy', 'rb').read()), allow_pickle=False)
result = int(dataset.sum())
```

The candidate data-local law is the exact C-contiguous little-endian signed-int64 sum over
the immutable, revision-bound NPY body. It must reproduce NumPy's signed-int64 wraparound
and Python `int(...)` projection. This candidate is not admitted until its adapter law and
pass-off/pass-on controls are executable. Arbitrary NumPy, Pandas and Polars remain opaque.

The first cross-Run value candidate is the same immutable NPY body because it already has a
sealed Host object, fixed revision/dtype/shape/layout bounds and fresh-consumer evidence.
Prior copy/private-COW fan-out economics were negative; Phase 6 must not claim improvement
unless a new matched workload or data-local consumer changes that result.

## Live ownership map

| Concern | Current owner | Successor change |
|---|---|---|
| synchronous Guest call | generated `_capability_call` and `_agent_runtime_host.call` in `runtime/capability/registry.go` and `guest/src/runtime.c` | derived source alone may call reserved submit/materialize helpers |
| blocking Host import | `hostCall` in `runtime/engine/wazero/engine.go` | add bounded `submit_call` and blocking `materialize_call` imports |
| logical call budget, operation index and receipt | `capability.Broker.call` | remains unchanged and runs only at materialization |
| qualified physical read | `Plan.PreparePreDispatch` and `PreparedPreDispatch.Call` | reused by a Run-private multi-entry pending-call table |
| existing physical/logical split | one-call `semantic.SemanticPreDispatch` plus Broker `StagedClaimer` | retain it; add a separate call-ID-targeted table rather than widening it into a scheduler |
| source transform | exact-Guest `guest/bootstrap/agent_runtime/source_pass.py` plus `sourcepatch.Patch` | add one static `split_phase_sources_read` patch and an effect-owning execution adapter |
| final source validation | Guest `_validate_request_source` then `_install_derived_tree` | original source must validate first; exact Guest regenerates and revalidates the patch |
| prepared scalar hole | `PreparedRegionTable` and `materialize_value` | remains separate from pending tool work in Phases 1-3 |
| immutable NPY object | `runtime/prepareddataset`, `research/prepareddataset` and prepared NumPy ingress | candidate input to later ValueSlot/data-local/reuse work, not a split-call token |

## Source-to-derived contract

### Admitted source lane

The v1 transform accepts one or two leading top-level assignments of the form:

```python
first = sources.read("alpha")
second = sources.read("beta")
result = [first, second]
```

The complete module must be a closed straight-line lane:

- each read target is a fresh simple name;
- each path is a static UTF-8 string inside the adapter schema bound;
- one read is accepted for the adjacency proof and two for the overlap proof;
- the remaining statements only assemble `result` from admitted names and bounded
  constants/list/tuple/dict values;
- there are no imports, aliases, functions/classes, branches, loops, `try`, context
  managers, comprehensions, mutation, other calls, dynamic namespace/frame/source/code
  introspection, tracing or unknown attributes.

Every rejection executes the unchanged original request if no Host work has started.

### Derived form

For one call, the exact Guest emits same-line adjacent operations:

```python
_pysolate_call_submit("slot-1", request_1); first = _pysolate_call_materialize("slot-1")["body"]
result = first
```

For two calls, both physical submissions appear on the first original call line, while each
blocking materialization remains on its original source line:

```python
_pysolate_call_submit("slot-1", request_1); _pysolate_call_submit("slot-2", request_2); first = _pysolate_call_materialize("slot-1")["body"]
second = _pysolate_call_materialize("slot-2")["body"]
result = [first, second]
```

This preserves line count and the source line on which each synchronous failure becomes
visible. The second submit is physical-only preparation. If the first materialization
fails, Python never reaches the second logical occurrence; the Host cancels or discards
slot 2 and emits no second receipt.

`_pysolate_*` names remain illegal in original Agent source. They are accepted only in a
patch regenerated by the exact Guest from the registered pass. No token is assigned to an
Agent variable, returned, compared, printed, inspected or stored in a Python container.

## Host ABI and state

The target Guest/WASI ABI is bounded text transport:

```text
submit_call(slot_id, canonical_call_request) -> success/failure
materialize_call(slot_id) -> canonical Broker response
```

`submit_call`:

1. validates the static slot and call envelope;
2. resolves the same sealed Plan used by the Broker;
3. requires an eligible `PreDispatchContract` and reserves the table limits;
4. starts at most one `PreparedPreDispatch.Call` in Host-owned work;
5. creates no Broker operation index, logical call count or receipt.

`materialize_call`:

1. claims the slot once;
2. submits the original canonical request through `Broker.Call`;
3. lets the Broker validate Plan, call budget, call ID, schemas and logical order;
4. lets the table's call-ID-targeted staged claimer wait for or consume the physical
   outcome;
5. returns the ordinary Broker response for the Guest helper to decode and raise/return as
   the original projection would.

The Run-private Host lifecycle is:

```text
submitted -> running -> ready | failed | cancelled
ready     -> consumed | discarded
failed    -> observed | discarded
```

A slot is single-submit and single-materialize. The table binds slot ID, call ID,
capability, canonical arguments and Plan identity. A normal non-targeted Broker call may
fall through to its ordinary live handler; a mismatch for a targeted call fails closed and
never starts a second physical request.

## Two ledgers

The evidence surface reports separate counters/timestamps.

### Physical-work ledger

- accepted submissions;
- physical starts and finishes;
- ready, failed and cancelled outcomes;
- physical result bytes and provider cost units;
- discarded/unclaimed terminal states;
- submit/start/finish timestamps.

### Logical-call ledger

- materialization attempts;
- Broker dynamic calls and operation indices;
- logical claims/consumes;
- source-order results/errors;
- receipts and terminal dispositions.

The invariant is:

```text
physical_ready != logical_call_occurred
```

No unmaterialized slot may increment Broker calls or create a receipt.

## Exception, effect and observation boundaries

- Only qualified pure/external immutable reads enter v1.
- Writes, approvals and captured playback are rejected before submit.
- Handler and output-schema failures are stored physically and raised only when the
  original materialization line runs.
- A first-call failure may coexist with completed later physical work, but the later work
  is discarded without a receipt.
- Cancellation/timeout stops pending work and joins it before Run teardown returns.
- Duplicate/missing/mismatched slots, calls or arguments fail closed without replay.
- Original source cannot access the helpers; transformed code cannot use introspection.
- Python controls branches, loops and `try`; these are rejected in v1 and added only by
  later dynamic-occurrence slices.
- Compensation never qualifies an early write.

The registered `split_phase_sources_read` pass is now v2. It keeps the v1 straight-line
lane and adds only two source-shaped dynamic forms:

- a top-level `if` whose condition reads only bounded `inputs` values and whose active
  branch is itself a closed read/result block;
- a top-level result-list loop over a bounded `inputs` iterable with one literal-path read
  and one append per iteration.

The submit helper owns a fresh per-Run occurrence counter and a private queue from each
static source slot to its dynamic Host slot. Generated source still receives no token.
An unselected branch and a zero-iteration loop submit nothing; a repeated loop occurrence
gets a distinct call identity; `try`, dynamic paths, unknown calls, and arbitrary nested
control flow remain rejected.

## Measurement and retention gates

The split-phase matched fixture uses two independent local deterministic handlers with the
same fixed delay, result schema and fresh Guest path. Report:

- complete Run latency;
- submit/start/finish/materialize timestamps;
- maximum concurrent physical calls;
- physical starts/finishes/discards;
- Broker call count, receipts and result/error parity;
- cleanup after success, first failure and cancellation.

Phase 1 is retained on semantic and lifecycle parity even though adjacent split has no
expected speedup. Phase 2 remains Experimental and default-off unless:

1. both physical reads overlap;
2. source-order result/error observation and receipt counts match the synchronous source;
3. the later unmaterialized read is discarded after an earlier failure;
4. matched end-to-end treatment latency is lower after analysis, Guest startup, scheduling,
   materialization and cleanup are included.

A correct slowdown closes the performance claim and stops scheduler expansion; it is not a
reason to add batching, a planner or a richer IR.

The fixed data-local candidate separately reports source bytes read, Host-to-Guest bytes,
Guest peak memory where available, exact result/error parity and complete Run latency. The
cross-Run candidate additionally reports producer attempts, sealed bytes, fresh consumer
count, copies/COW faults where applicable and teardown.

## P0 no-go matrix

| Candidate | Decision | Reason |
|---|---|---|
| arbitrary `workspace.read_text` | no-go for early staging | current mutable in-memory workspace has no immutable root/revision join in this adapter |
| generic Git reads | no-go for v1 | read semantics vary by repository state and no measured repeated high-latency lane is frozen |
| Tau2/provider tools as the first implementation | evidence-only incidence source | provider state and task-specific schemas are not the fixed adapter law used by this local mechanism proof |
| AWO/meta-tool trace synthesis | out of scope | traces may count patterns but do not define semantics or policy |
| LLM-Tool/AAFLOW batch/fusion | out of scope | split-phase keeps one physical request per logical call and does not define per-item batch outcomes |
| Stratum DataOp graph | out of scope | Pysolate has ordinary Python plus selected adapter facts, not a declarative pipeline IR |
| further scalar folding/CSE | closed negative lane | retained evidence shows transform/runtime overhead dominates the cheap work |

## Phase 0 exit

Implementation may begin only after the companion preregistration
[`host-scheduled-call-preregistration-v1.json`](../evidence/host-scheduled-call-preregistration-v1.json)
is present, relative links validate and the live baseline tests remain green.
