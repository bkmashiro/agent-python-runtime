# Execution core

The runtime is being consolidated around one Guest attempt and one Host-owned tool path. This document maps the current code; it does not describe a new facade or an additional framework.

## Start here

1. `runtime/request.go`, `runtime/config.go`, `runtime/response.go`: the inputs, execution limits and result envelope.
2. `runtime/engine/wazero/engine.go`: create an isolated Guest, run Python, collect its result and release attempt resources.
3. `runtime/capability/broker.go`: admit a tool call, obtain its outcome, and return the validated response to Python.
4. `runtime/durable/runner.go` and `store.go`: own a logical Run across fresh Guest attempts, replay recorded calls, and persist waits and final outcomes.

```text
Optional durable Run
  -> fresh or explicitly prepared Guest attempt
  -> Python computation
  -> Host Broker -> tool handler or saved outcome
  -> result, durable park, cancellation or execution error
  -> release attempt resources
```

A durable Run survives its Guest. Ordinary execution does not require SQLite. Durable execution currently uses fresh deterministic Guests; PLM and COW are not enabled in that mode.

## Optional mechanisms and their owners

Configure mechanisms directly with `RunConfig.Mechanisms` and provide their Host resources to the Factory. Real source transforms live in `runtime/sourcepatch`; runtime configuration no longer passes through a plugin catalog. See [direct configuration and typed transforms](source-pass-plugins.md).

- **PLM:** `runtime/capability/split_phase.go` owns prepared calls and their original-point linearization. `runtime/semantic/split_phase_preissue.go` connects source-prefix admission to that same owner. Prefix admission must not create a second execution controller.
- **Prepared execution and COW:** `runtime/engine/wazero/prepared_*` and `cow_*` own prepared state, reusable images and private memory. They reuse the normal attempt lifecycle.
- **Memory residency during waits:** `runtime/engine/wazero/cold_io*` handles the existing attempt's linear-memory advice. It does not own logical Run recovery.
- **Workspace:** `runtime/workspace` owns mounted workspace state and its publication boundaries.

This is still a larger codebase than Spine. Removing duplicate execution owners is the first structural step; the remaining package count is not a claim that consolidation is finished.

## Retired paths

The following APIs and experiments are removed rather than hidden behind compatibility flags:

- The `runtime/passplugin` runtime catalog, runtime-pass configuration hashes, `Factory.Passes`, and the prepared-value pass wrapper.
- The unused `runtime/passpipeline` outcome ledger, its semantic outcome adapter, and registration binding-name lists. They validated descriptions of other checks and had no execution consumer.
- `Factory.LegacyResearchExecution`, retained-prefix `RunStream`, and the separate semantic pre-dispatch controller. Product source streaming continues through PLM's shared split-phase owner.
- The old eager/independent-semantic comparison drivers and campaigns which depended on those execution paths.

Historical implementations are available at Git revision `df191bf1`. Frozen evidence files and their validation anchors are not rewritten. The research viewer can still decode historical campaign records without linking the retired Guest executors. Re-running those old experiments requires their historical revision; current benchmark commands must exercise current mechanisms.

The code retained for artifact loading, Guest/Host admission, operation identity, cancellation, durable commit ordering and ownership is not replaced by telemetry or proof records.

## Verification of the retired-execution slice (`a1805843`)

- `go test -race -p 4 ./runtime/...`, `go vet ./...`, and `go build ./...` passed.
- Real NumPy Guest tests passed on macOS for prepared execution, invalid-parent branch discard, PLM control flow and validation/failure paths, uncapturable durable park, approval reopen, stable error replay, and 11 cross-process determinism cases.
- A 2-vCPU/2-GiB Linux arm64 VM passed real COW selection/isolation, cold-wait state retention, and fresh PLM prefix-analysis sessions. The VM was shut down afterward.
- `go test -p 4 ./...` passed 63 packages; three pre-existing `research/labview` evidence-anchor tests still fail. Their anchors and frozen data were not changed.

This is a removal of parallel execution paths, not a claim that the remaining runtime is as small as Spine. The native backend, workflow evaluator, semantic planner and prepared-family layers still exist.

## Direct configuration and typed source transforms

The next slice removes the runtime optimization catalog. All command, research and integration callers now set mechanism fields directly. Pure transforms and PLM use typed `sourcepatch` entry points; ValueSlot uses its existing prelude function.

Validation passed for runtime race tests, whole-repository vet/build, 16 real-Guest tests on macOS, and five real-Guest prepared/COW/cold-wait tests on Linux arm64. The full Go suite passed 62 packages with only the same three historical Lab anchor failures. No Guest artifact, frozen evidence, or dependency versions changed.
