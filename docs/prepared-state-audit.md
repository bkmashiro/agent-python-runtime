# Prepared-state safety audit

- Date: 2026-07-22
- Guest source commit: `18a1f4d05446d6091122cb306934db6928a1bb80`
- Guest SHA-256: `187831a0649615b061a7ad7a679df751c4b8cc2da2e41977e61099dfabe9bebe`
- Artifact: `wasm32-wasip1` reactor, 56,008,018 bytes
- Backend audited: wazero `v1.11.0`
- Locked structure tool: wasm-tools `1.254.0`

## Decision

Do not implement linear-memory snapshot/restore for this artifact and backend. The artifact has mutable state outside exported linear memory, and wazero's public API does not provide a complete module-instance clone/restore primitive.

The smallest safe performance spike is an **optional bounded pool of single-use preinitialized instances**:

1. instantiate a new module from the compiled artifact;
2. run trusted `_initialize` and `runtime_init` before admission to the pool;
3. attach per-Run context and broker only when an instance is checked out;
4. run request-specific trusted `runtime_prepare` and `execute` once;
5. close and discard the instance after every success, structured error, oversized output, Host-call failure, timeout, trap, or cancellation;
6. use the existing synchronous fresh-instance path on pool miss or any preparation uncertainty.

No dirty instance returns to the pool. This preserves the fresh-instance safety property and does not claim `prepared-restore`.

## Exact artifact census

`wasm-tools validate` succeeded. A machine-filtered WAT census found:

| State class | Exact artifact evidence | Restore consequence |
|---|---|---|
| Linear memory | one exported memory, initial 2,048 pages (128 MiB), maximum 8,192 pages (512 MiB) | byte copy alone is insufficient; growth changes memory size and cannot be shrunk through the public API |
| Mutable globals | two globals total; global 0 is unexported mutable `i32`, initialized to `16,777,216`; global 1 is immutable | global 0 has 8,415 static `global.set` instruction sites and cannot be read/set through `api.Module.ExportedGlobal` |
| Tables | one unexported fixed table, 5,953/5,953 `funcref`, one element segment | no `table.set`, `table.grow`, `table.fill`, or `table.copy` instructions were present; it is static for this exact artifact, but wazero's public `api.Module` still exposes no table accessor (`TODO: Table`) |
| Data | 10,002 data segments | initial state is reproduced only by new instantiation |
| Memory mutation | one `memory.grow`, 789 `memory.copy`, and 343 `memory.fill` instruction sites | arbitrary workloads may grow or heavily mutate memory; static initial size is not a reset proof |

A dynamic probe of the exact artifact recorded 2,048 pages after instantiation, `_initialize`, `runtime_init`, and a small `runtime_prepare`. This means one idle preinitialized instance currently retains at least 128 MiB of guest linear memory. It does **not** prove later workloads cannot grow memory.

## Host and WASI state

The manifest contains one custom import, `agent_runtime_v1.host_call`, plus 40 WASI Preview 1 imports. The WASI set includes clocks, random, environment, file-descriptor/path operations, polling, and socket-shaped Preview 1 functions. Current `ModuleConfig` grants no environment, preopened filesystem, or sockets, and ambient-authority E2E remains the authority gate.

A module instance also owns or references Host-side state not represented by linear memory:

- WASI file-descriptor/resource tables and clock/random providers;
- module lifecycle/exit state;
- the per-instance stderr buffer;
- call-stack and trap/cancellation state;
- the per-Run capability broker obtained from call context.

The broker is not stored in guest memory or module configuration. `host_call` resolves it from the current invocation context, which allows a preinitialized single-use module to receive a fresh Host-owned broker at checkout. Preparation must never run request capability calls.

## wazero capability report

| Capability | wazero v1.11 evidence | Verdict |
|---|---|---|
| Reuse compiled artifact | `CompiledModule` is reusable for new instantiations | supported and already used |
| Read/write exported memory | `api.Module.Memory` / `ExportedMemory` | supported, but not complete state |
| Shrink memory | no public operation | unsupported |
| Restore mutable global | only exported globals can be obtained; artifact mutable global is unexported | unsupported |
| Restore table | `api.Module` has `TODO: Table`; artifact table is unexported | unsupported |
| Clone full module instance | no public API | unsupported |
| Experimental `Snapshot` | captures/restores execution stack state inside a function invocation | not a module-state clone and unsuitable for prepared restore |
| Close uncertain instance | `api.Module.Close` / Runtime close | supported and required |

The experimental checkpoint API's own contract says `Snapshot` holds execution state at a `Snapshotter.Snapshot` call and restores the execution stack through a Host function. The implementation clones call-stack state; it does not supply a durable clone of linear memory, unexported globals, tables, WASI resources, or module lifecycle state.

## Backend-neutral conformance matrix

A backend may claim `prepared-restore` only if every row is proven for the exact artifact and backend. `single-use-preinitialized` avoids restoration by consuming each prepared instance once.

| Boundary | Fresh instance | Single-use preinitialized | Prepared restore claim |
|---|---:|---:|---:|
| New guest state per Run | required | required; instance has never served a Run | must prove exact restore |
| Linear memory bytes and size | new instantiation | no prior request mutation | restore bytes and shrink/grow state |
| Mutable globals | new instantiation | no prior request mutation | restore every mutable global |
| Tables/elements | new instantiation | no prior request mutation | restore every mutable table entry/size |
| WASI/Host resources | new instantiation | only trusted initialization occurred | recreate or restore all resources |
| Per-Run broker/receipts | new broker | attach fresh broker at checkout | never snapshot or reuse broker evidence |
| Trusted request prepare | inside request instance | after checkout, once | restore post-request to pre-request boundary |
| Success/structured error | close | close | restore or discard on uncertainty |
| Timeout/cancellation/trap | close | close | always discard |
| Oversized output/Host-call failure | close | close | restore only with proof; otherwise discard |
| Pool/preparer failure | not applicable | discard candidate; use synchronous fresh fallback | discard |
| Shutdown | close runtime/modules | close queued modules, then runtime | close snapshots/resources |

## Spike constraints

- Opt-in only; default capacity is zero.
- Host-owned hard capacity and memory budget; initial implementation should cap capacity at four (at least 512 MiB of guest linear memory at the exact current initial size).
- A queued instance must have completed only `_initialize` and `runtime_init`; request-specific prepare is never pooled.
- Checkout must be exclusive. Returned/failed/canceled instances are closed, never re-enqueued.
- Pool miss must execute the current safe fresh path, not wait indefinitely.
- Background refill must stop on Engine close and must not outlive the wazero Runtime.
- Properties continue to report `fresh-instance` until a distinct, evidence-backed neutral mode is defined. The optimization changes when trusted initialization occurs, not the one-Run lifetime.
- Benchmark warm hit, pool miss, refill cost, memory footprint, and failure recovery separately.

## Remaining proof before enabling capacity above zero

The spike must add RED/GREEN conformance for success, structured error, timeout, trap, cancellation, oversized output, Host-call success/failure, trusted-prepare isolation, exclusive checkout, pool miss fallback, refill failure, and Engine shutdown. Any uncertainty closes the candidate and preserves capacity-zero behavior.
