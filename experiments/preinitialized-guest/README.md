# Build-time Python preinitialization spike

## Question

Given the exact locked base Guest, when Wasmtime 44.0.1 Wizer executes the reactor initializer and `runtime_init("{}")` at build time, does the rewritten standard Wasm artifact execute unchanged under the current wazero Host while eliminating per-instance CPython initialization?

## Candidate boundary

The experimental input exports a build-only `runtime_preinitialize` function which calls the reactor `_initialize` and then `runtime_init("{}")`. Wizer captures the resulting linear memory and mutable globals, removes that build-only entry point, and renames a build-only no-op export to the normal runtime `_initialize` export so the existing Host does not rerun reactor constructors.

The custom `agent_runtime_v1.host_call` import is satisfied during transformation by `host-call-stub.wat`, which always returns rejection. The build-time initializer must not acquire Host capabilities.

The production artifact, ABI header, manifest, and default runtime path are unchanged. The candidate is a separate artifact and is never selected automatically.

## Falsifiable gates

1. The locked Wasmtime Wizer transform succeeds twice against one exact input.
2. Both transformed outputs are byte-identical.
3. The candidate passes the existing artifact ABI verifier and real wazero E2E suite.
4. Exact baseline/candidate runtime evidence uses the same clean Host revision, environment, fixture, backend, and sample count.
5. `runtime_init` median improves by at least 10x and fresh-run total median by at least 2x.
6. The candidate remains at or below 512 MiB.

## Verdict: PENDING

No production recommendation is made until the Linux artifact job produces and independently validates the raw transform receipt, both runtime evidence files, and the comparison report.
