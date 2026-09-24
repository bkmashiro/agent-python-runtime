# AppWorld development acceptance

The optional [AppWorld bridge](../examples/appworld-bridge/README.md) keeps Python
execution in Pysolate and AppWorld applications/evaluation in a trusted local
worker. This is reference-program and adapter validation, **not an agent score**
or an official leaderboard submission. No model calls were made.

## Frozen inputs

- AppWorld package `0.1.3.post1`, release source
  `66ad8099e12188ece0d3fe45e661dbc01880813b`.
- Official data `0.1.0`; encrypted bundle SHA-256:
  `fd9f9608c2ec71ed0ac25c3633a738b9129a318a129e31230425b9188e508250`.
- Pysolate core baseline `615b986652de349b0ab0c1349fe63fa5a4e9af61`;
  Guest SHA-256 `2c63ce407eae626c89fcd1dd918c713b1f8b75a35a6cf6ca6861466168d0faf8`.
- Local macOS/arm64; native Host Python 3.11, isolated Guest CPython 3.14.
- Only the **dev** split was inspected. No test-set task inspection or tuning.

Upstream main was a different development version and was not used as the runtime
baseline. Installed public environment/requester/task/evaluator files were checked
against the release tag. Package versions and raw evidence remain private.

## What passed

1. **Native environment integrity:** all 57 original dev reference programs
   passed the original evaluator, starting from states that did not already pass.
   Their 3,192 API requests were captured with paired start/end observations.
2. **Unmodified portability probe:** three preselected, distinct task families
   passed natively but failed in Guest before API calls because the default Guest
   does not contain AppWorld's package imports. These failures were retained.
3. **Annotation-only adaptation control:** reference imports used only by type
   annotations were removed after AST checks, annotations erased, and a verified
   unused requester argument supplied as `None`. The solution-body AST remained
   unchanged. Adapted programs were independently evaluated in native AppWorld.
4. **Predeclared portable subset:** all 33 dev references with no runtime imports
   or only standard-library `re` were included, selected before Guest outcomes.
   All 33 adapted native controls and all 33 Guest executions passed the original
   evaluator. Their 1,994 API requests matched exactly across original native,
   annotation-adapted native and Guest execution.
5. **Negative/reset/playback controls:** a no-op program failed task grading;
   a successful portable program passed again in a fresh world. Strict private
   captured-response playback matched the recorded request sequence and Guest
   output with no AppWorld imports or live network backend. Its recorded grade
   is playback data, not a new evaluation.

The other **24 dev references** require runtime helper-library support and were
explicitly excluded from the portable subset. They are not counted as Guest
passes. The initial three-task probe and the 33-program cohort are separate
stages, not interchangeable denominators. No tasks were replaced after failure.
An initial adaptation using a future import was rejected by the native syntax
guard; that attempt was retained and the guard was not relaxed.

The portable cohort recorded two host-source hashes caused by removal/restoration
of one blank line during collection. Both variants were archived and their ASTs
verified identical. This is correctness acceptance, not a timed comparison; no
latency claim is made from that cohort.

## What this does not establish

- No model's task-solving ability was scored. Reference programs contain privileged
  solution knowledge and must not be treated as unbiased agent trajectories.
- The full AppWorld Python environment is not available inside Guest. In
  particular, persistent IPython globals across agent turns are not emulated.
- Removing annotation-only dependencies is a disclosed adaptation, not proof that
  unmodified AppWorld reference programs run inside the default Guest.
- No early-read, preflight, streaming, eviction or new recovery behavior was added.
  AppWorld tools are not implicitly approved as stable snapshot reads.
- Native AppWorld APIs use the local application environment. Their durations are
  not production-network I/O latency, and this study does not establish scheduling
  or memory-density improvements.
- Full local recording adds overhead. Replaying captured responses validates
  observations, not physical backend timings or a fresh task score.

## Verification and privacy

The new Go bridge passed real-Guest protocol tests and targeted race tests,
including cancellation while a worker is not reading stdin. Python fake-world
checks cover allowlisting, reserved controls, catchable API failures, original
evaluator invocation, cleanup and no-overwrite. Full repository `make check`
passed; the optional Python host tests were also run separately.

Full tasks, solution sources, state, detailed grades, API payloads and traces stay
outside Git. The upstream bundle redistribution restrictions apply to extracted
and derived benchmark material. This document contains only aggregate acceptance
results and generic integration details. Follow the upstream license and split
usage guidance before publishing other artifacts.

The next research step should use external tasks with an explicit execution
contract and fresh model traces. That requires a decision about multi-turn Python
state and library availability, rather than adding benchmark-specific shims or
claiming that this adapter is already a complete AppWorld agent.
