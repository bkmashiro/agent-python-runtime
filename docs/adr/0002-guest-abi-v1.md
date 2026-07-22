# ADR 0002: Guest ABI v1

- Status: Accepted direction; identifiers freeze when fixtures and real artifact tests pass
- Date: 2026-07-22

## Context

The Host needs a small core-WASM ABI that is language-neutral at the boundary, bounds all memory exchange, and supports trusted preparation separately from untrusted execution.

## Export direction

```text
_initialize()
runtime_init(ptr: i32, len: i32) -> i32
runtime_prepare(ptr: i32, len: i32) -> i32
alloc(len: i32) -> i32
dealloc(ptr: i32)
execute(ptr: i32, len: i32) -> i32
```

`execute` returns a pointer to:

```text
[u32 little-endian payload length][UTF-8 JSON payload]
```

The Host validates pointer arithmetic, memory bounds, configured maximum length, UTF-8, and response schema before accepting the result.

`runtime_prepare` is trusted and optional at the Host API level. It may load approved evaluator/package state before the prepared snapshot. Generated code cannot invoke it through `RunRequest`.

## Capability import direction

The initial broker import uses guest-allocated bounded request and response buffers:

```text
host_call(req_ptr: i32, req_len: i32, resp_ptr: i32, resp_cap: i32) -> i32
```

A non-negative return value is the response byte length. Versioned negative values represent bounded protocol/policy errors. Exact module/name identifiers and error codes will be frozen with capability fixtures.

The Host does not allocate through a re-entrant guest export during an import.

## Memory ownership

- Guest owns buffers returned by `alloc` and frees them with `dealloc`.
- Host writes only within validated guest-provided buffers.
- The response returned by `execute` remains guest-owned and valid only until the next guest call/reset.
- V1 has explicit request, result, Host-call request, and Host-call response byte maxima.

## JSON behavior

- UTF-8 only;
- unknown fields rejected at stable boundaries;
- no NaN/Infinity extension values;
- errors are structured and tracebacks bounded;
- authority-bearing request fields rejected;
- Host validates final output against the response contract and optional caller output schema.

## Compatibility policy

V1 contains no `py_exec`, `resp_buf`, `resp_len`, evaluator/preview names, or product-specific operation semantics. ABI changes require a version change or explicit compatibility contract.

## Reset boundary

Snapshot capture occurs only after `_initialize`, `runtime_init`, and optional `runtime_prepare` return. A trap, cancellation, pointer violation, or unsupported memory/global/table mutation makes the instance unhealthy.
