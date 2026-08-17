# Pysolate Runtime Optimisation Examples

This note provides paper-ready teaching examples for three runtime decisions. The examples are intentionally small: each isolates one causal mechanism while preserving Pysolate's Host-owned authority and evidence boundaries.

## 1. Reach-gated source-prefix execution

### Optimisation point

An agent normally produces a complete Python program before the runtime begins execution. This serialises source generation and a slow external read, even when the first complete top-level suite already contains all information required to issue that read.

Pysolate instead delivers source incrementally. Once the Guest Python compiler confirms that a top-level suite is complete, the Guest executes that suite in its private namespace. If ordinary Python control flow reaches a qualified read, the Host dispatches it while the producer continues generating the remaining source.

### Example code

```python
record = slow.lookup('alpha')
label = record['label'].upper()
result = {'label': label}
```

The first line is a complete suite. Reaching `slow.lookup` starts the Host-mediated read. The second and third lines are then generated while that read is in flight.

### Execution shape

```text
generate-then-execute
source generation  |==============================|
Host READ                                           |================|
run complete                                                          |

stream-while-generating
source generation  |==============================|
Host READ          |================================|
run complete                                         |
```

Only source-chunk delivery timing differs between the matched lanes. They use the same source, Guest artifact, capability Plan, authority grant, workspace baseline, handler, oracle, timeout and generation schedule.

### Observed result

| Lane | Median wall time |
|---|---:|
| Generate, then execute | 2,950 ms |
| Stream while generating | 1,534 ms |
| Mechanism-window ratio | 1.923× |

Both lanes recorded one logical call, one physical dispatch, no fallback, the same accepted terminal result, and unchanged workspace digests. The improvement therefore comes from overlapping the generation tail with the already-reached read, rather than deleting work or changing the result.

### Simple analysis

The mechanism removes avoidable serialisation at a semantic boundary already recognised by the Guest compiler. It does not construct a dynamic DAG, infer dependencies, or pre-dispatch a call from metadata. The read begins only after normal Python execution reaches it. This preserves Python ordering and Host authority while allowing source production and external latency to overlap.

The measured comparison is an authored mechanism demonstration with a fixed slow-read fixture. It shows that the overlap exists and can be measured; it is not an estimate of prevalence or end-to-end benefit across natural agent workloads.

## 2. Exact request sharing

### Optimisation point

Two logical agents can occasionally submit the same immutable computation under the same authority and isolation context. Starting a second Guest would repeat physical work without changing either logical result.

Pysolate compares the complete sealed request identity: source, inputs, artifact, execution profile, capability Plan, privacy partition and workspace root. Only an exact match may attach to an in-flight physical execution.

### Example code

Agent A:

```python
result = {'square': inputs['value'] * inputs['value']}
```

Agent B submits the byte-identical program with the same bound inputs and execution context:

```python
result = {'square': inputs['value'] * inputs['value']}
```

### Execution shape

```text
logical request A ───────────────┐
                                 ├── one sealed physical Guest ── result A
logical request B ── attach ─────┘                              └─ result B
```

Agent B's duplicate physical run is skipped, but its logical lifecycle and terminal result remain explicit. Both logical requests point to the same physical execution identity.

### Observed result

| Quantity | Result |
|---|---:|
| Logical requests | 2 |
| Physical Guest executions | 1 |
| Accepted oracle results | 2 / 2 |

### Simple analysis

This optimisation changes the mapping between logical and physical work, not the program semantics. A logical request is never erased: it receives its own recorded disposition and result, while the Host records that both were satisfied by one physical execution.

This is **exact request sharing**, not a general cache. Equality of tool name and arguments is insufficient, especially for external reads whose freshness or snapshot may change. Sharing requires the complete bound identity and an applicable effect/freshness contract.

## 3. Source-mismatch fresh-execution control

### Safety point

A useful optimisation must also make its rejection boundary visible. Two programs may look similar while differing in source identity. Reusing a physical result across that difference would make the optimisation an unauthorised semantic substitution.

### Example code

Existing exact request:

```python
result = {'square': inputs['value'] * inputs['value']}
```

Source-mismatch request:

```python
result = {'square': pow(inputs['value'], 2)}
```

### Execution shape

```text
exact request       ── physical Guest A ── result A
source mismatch     ── fresh Guest B    ── result B
                         ↑ sharing gate rejects before execution
```

### Observed result

| Quantity | Result |
|---|---:|
| Logical requests | 2 |
| Physical Guest executions | 2 |
| Unsafe reuse | 0 |

### Simple analysis

The second source receives a fresh physical execution because its source digest differs. This negative control shows that sharing is subordinate to bound identity: similarity does not widen authority, and an optimisation miss preserves ordinary fresh-execution semantics.

Together, the positive and negative sharing examples make the safety argument concrete. The positive case demonstrates eliminated duplicate physical work; the negative case demonstrates that the same mechanism refuses reuse when its preconditions do not hold.

## Recommended code-centric visual encoding

Use code as the primary visual object rather than placing source beside an unrelated architecture diagram.

| Colour | Meaning | Suggested annotation |
|---|---|---|
| Teal | Host-mediated effect is reached | `READ starts after Guest compiler closes this suite` |
| Blue | Source generated concurrently | `generated while the READ is in flight` |
| Green | Physical Guest owner | `this logical request owns the physical execution` |
| Purple hatch | Shared logical request | `same sealed identity; duplicate physical run skipped` |
| Orange | Fresh fallback | `source identity differs; sharing rejected` |

For the sharing case, avoid labelling the purple region simply as `cached`: that word suggests arbitrary memoisation. `Shared result; physical run skipped` accurately describes the measured mechanism.

## Suggested paper placement

A compact three-panel figure can teach the complete story:

1. **Overlap:** highlight line 1 in teal and lines 2–4 in blue; place baseline and streaming timelines below the code.
2. **Share:** show the same source twice; mark one copy green as the physical owner and hatch the second purple as the attached logical request.
3. **Reject:** highlight the changed source in orange and route both requests to separate physical Guests.

Suggested caption:

> **Code-centred views of Pysolate's execution decisions.** Reach-gated streaming begins a Host-mediated read after the Guest compiler closes the first suite and overlaps the remaining source generation with read latency (left). Exact request sharing maps two fully identical logical requests to one sealed physical Guest execution (centre). A source-identity mismatch fails closed to a fresh execution (right). Colours encode measured execution roles rather than source-level dependency inference.

## Evidence boundary

The source-prefix result is a matched authored mechanism fixture, not a natural-workload performance estimate. The exact-sharing and mismatch examples are selected real-Guest campaign cases. Guest stdout is not used to establish calls, physical execution identity, workspace disposition or oracle acceptance; those facts come from Host-owned typed evidence.
