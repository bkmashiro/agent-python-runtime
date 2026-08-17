# Agent Output and Workflow Evidence Contract v1

Status: implemented contract; performance claims remain unmeasured.

## Model-facing output

Pysolate now separates a canonical JSON result from bounded narrative logs:

```python
print("selected 12 records")
return {"count": 12, "records": records[:5]}
```

The Guest emits:

```json
{
  "logs": ["selected 12 records"],
  "result": {"count": 12, "records": []},
  "result_present": true,
  "result_source": "return"
}
```

`return` is canonical for one-shot execution. `print` is ordered, model-facing text with a 64 KiB/256-line bound and an explicit `[pysolate stdout truncated]` marker. Byte overflow aborts with `output_limit_exceeded` while preserving a UTF-8-safe bounded prefix; line overflow keeps the terminal result and returns at most 255 content lines plus the marker. Legacy `result = value` remains accepted as `result_source=legacy_result`. Falling through without either is `result_source=missing` and differs from explicit `return None`. The append-only streaming executor uses the same persistent bounded log capture across chunks; because completed chunks execute incrementally, its terminal value remains explicitly `legacy_result`/`missing` rather than pretending to support a deferred top-level `return`.

The implementation follows the useful boundary in DeepSeek Harness Code Mode: only outer logs and the completion value re-enter model context, while intermediate tool values remain execution-local. Reference: `deepseek-ai/deepseek-harness@47f943859bef60e4160492346772ded9b24f765a`, `packages/core/tools/README.md`, lines 118–125 and 158–163.

## Source identity

The exact model source remains separate from the Host-owned execution wrapper. Every successful new-Guest response includes:

- `model_source_sha256`;
- `effective_ast_sha256`;
- `wrapper_contract_sha256`.

The wrapper preserves original statement line numbers, module-global binding behavior for top-level assignments, supports real top-level `return`, and executes the model body once. Late `from __future__` imports and all `_pysolate_*` binding forms are rejected. Module docstrings and legal `from __future__` imports remain in the module preamble. The Host independently recomputes and joins `model_source_sha256` to `RunRequest.Code`; effective-AST and wrapper digests remain Guest execution facts, not Host receipts or evaluation evidence. Wrapper metadata does not widen authority.

## Three evidence layers

The executable schema is `research/workflowbench/evidence_layers.go`.

### 1. Natural opportunity census

Examples: tau2, Open-SWE.

Purpose:

- compatibility and task-oracle preservation;
- absolute isolation cost;
- observed optimization admission/ineligibility/fallback rates.

Natural workloads may not require optimization and may not support causal speedup claims merely because Pysolate ran them.

### 2. Trace-derived DAG workloads

A private real-Agent trace is projected into a body-safe DAG of model turns, typed tool calls, local computation, dependencies, effect classes, logical-agent ownership and import shards. The public workload binds the private trace digest; raw bodies remain private.

Purpose:

- preserve realistic workflow shape without model variance;
- compare matched scheduling and placement lanes;
- measure fan-out/fan-in, local joins, write barriers and multi-Agent sharing.

Claims are bounded to the frozen trace shape.

### 3. Mechanism stress workloads

Authored, falsifiable cases target named mechanisms such as prepared runtime, memory COW, compile single-flight, bounded parallel dispatch and logical-Agent multiplexing.

Purpose:

- prove admission and fallback behavior;
- measure mechanism-specific latency, throughput, CPU and memory;
- exercise cancellation, timeout and exclusive-effect barriers.

Claims remain fixture-bounded and cannot be presented as natural benchmark uplift.

## Required matched metrics

Every workload preregisters immutable case, independent-oracle and full lane-configuration digests. The validator requires a fail-closed oracle authority appropriate to the layer: upstream official for natural benchmarks, a private projection validator for trace-derived DAGs, and an independent fixture for mechanism stress cases. A `task_oracle: true` boolean alone is never sufficient.

Every layer records task oracle, effect/receipt equivalence, admission/fallback, logical versus physical calls, wall and tail latency, Guest starts and compile counts. Agent-level cohorts additionally record model turns and provider calls; resource studies record CPU and peak RSS.

A workload DAG rejects cycles, missing nodes, concurrency-safe external writes and unbound capabilities. A valid suite must contain all three layers, preventing one microbenchmark from silently standing in for natural, trace-derived and mechanism evidence simultaneously.
