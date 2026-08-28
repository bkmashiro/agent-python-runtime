# PLM V1 production refactor

Status: implemented, exact-Guest verified and still default-off.

## Result

The matched five-run fixture now measures:

| profile | sequential median | PLM median | delta |
|---|---:|---:|---:|
| cold end to end | 4.125 s | 4.314 s | +4.57% |
| Engine precompiled | 2.736 s | 2.912 s | +6.42% |

Exact evidence: [`plm-v1-production-economics.json`](../evidence/plm-v1-production-economics.json).
It is attributed to target `57b8f3a0894cdd8a29b318096e09f32e9c37731b` and artifact
`sha256:fc0f034ba35ca11d87e18aeef2a93ee1fc33dd228a9c72c8708c2745b70a89a8`.

The original Gate 6 fixture measured `+32.01%` cold and `+48.27%` with Engines
precompiled. Absolute PLM overhead fell from about 1.29 seconds to 0.19 seconds cold
and 0.18 seconds precompiled. The old measurement remains an attributed historical
record; it is not rewritten as if it came from the production-refactored target.

## What was removed

The final Guest no longer:

- compiles the original tree before compiling the selected tree;
- replays the complete transform when selecting a patch it just produced;
- parses the original source or derived source more than once;
- computes source-patch AST digests that the Host cannot independently verify;
- serializes a derived source body for the PLM-only same-Guest selection;
- copies an expanding visible-name set for every statement;
- rescans the whole tree to repair locations for two inserted helper nodes.

The active path is:

```text
parse and stage original once
→ transform one Run-private AST once
→ Host validates source/pass/registration/projections
→ validate and compile the selected tree once
→ execute
```

A metadata-only source-patch v3 message crosses the Guest/Host boundary. The selected
AST remains private to the same final Guest. Generic authority-free source-patch plugins
continue to carry derived source because they cross a different compiler boundary.

## Checks that remain

The refactor retains checks that enforce a real authority or semantic boundary:

- the Host recomputes the original source digest and validates pass, registration and
  capability projections;
- the selected tree receives the ordinary source contract exactly once;
- compiler-generated PLM helper names carry a private marker, while user binding or use
  of the same reserved names is rejected;
- projection receivers and methods cannot be rebound, imported under a conflicting name or
  changed through direct dynamic mutation;
- code, frame, traceback and tracing observation makes PLM not applicable because transformed
  execution would otherwise be observable;
- runtime initialization clears any pending same-Guest selected tree, preventing cross-Run reuse;
- unsupported source executes the unchanged original path;
- temporal, authority, provider/session and uncertain-outcome checks remain at the
  original logical call.

These checks are not responsible for the remaining latency. Original-point temporal
validation remains microsecond-scale. The remaining fixture delta is compiler-pass work,
while the available prepare-to-linearize window is only about 0.3 ms.

## Decision

This refactor brings the compiler path to bounded production-quality overhead, but the
controlled fixture is still slower and does not support default enablement. PLM remains
experimental and default-off until a representative workload demonstrates positive net
economics. No Future projection, second analyzer Guest, cross-Run mutable cache or Host
AST rewriter is introduced.
