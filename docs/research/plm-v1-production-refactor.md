# PLM V1 production refactor

Status: implemented, exact-Guest verified and still default-off.

## Result

The matched five-run fixture now measures:

| profile | sequential median | PLM median | delta |
|---|---:|---:|---:|
| cold end to end | 4.054 s | 4.141 s | +2.15% |
| Engine precompiled | 2.716 s | 2.801 s | +3.12% |

Exact evidence: [`plm-v1-production-economics.json`](../evidence/plm-v1-production-economics.json).
It is attributed to target `5d13d5c5ce8e6bfdb3bf2dde77ad4003b948fe4a` and artifact
`sha256:02825c67cd1cd3bd8cfe333965e248c7b6ef41684bca73f42be25f5274b1bc9b`.

The original Gate 6 fixture measured `+32.01%` cold and `+48.27%` with Engines
precompiled. Absolute PLM overhead fell from about 1.29 seconds to about 0.09
seconds in both profiles. The old measurement remains an attributed historical
record; it is not rewritten as if it came from the production-refactored target.

## What was removed

The final Guest no longer:

- compiles the original tree before compiling the selected tree;
- replays the complete transform when selecting a patch it just produced;
- parses the original source or derived source more than once;
- computes source-patch AST digests that the Host cannot independently verify;
- serializes a derived source body for the PLM-only same-Guest selection;
- copies an expanding visible-name set for every statement;
- rescans the whole tree to repair locations for two inserted helper nodes;
- loads unrelated generic source passes on the PLM hot path;
- walks the full AST a second time for checks that narrow lexical admission can reject;
- copies admitted source back into the same Guest or reparses the same patch;
- exposes a second generic `Transform` owner for the Host-scheduled PLM pass.

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
- lexical admission uses Python-compatible NFKC normalization and AST byte offsets, so Unicode
  identifier aliases and non-newline separators cannot bypass fallback;
- code, frame, traceback and tracing observation makes PLM not applicable because transformed
  execution would otherwise be observable;
- runtime initialization clears any pending same-Guest selected tree, preventing cross-Run reuse;
- unsupported source executes the unchanged original path;
- temporal, authority, provider/session and uncertain-outcome checks remain at the
  original logical call.

These checks are not responsible for the remaining latency. Original-point temporal
validation remains microsecond-scale. The final lifecycle records about 87–88 ms of PLM
lowering and a matched end-to-end delta of 85–87 ms. The roughly 338–344 ms selected-tree
validation and compilation stage replaces baseline compilation; it is not all incremental
PLM cost. The available prepare-to-linearize window in this fixture is only about 0.3 ms.

## Decision

This refactor brings the compiler path to bounded production-quality overhead, but the
controlled fixture is still slower and does not support default enablement. PLM remains
experimental and default-off until a representative workload demonstrates positive net
economics. No Future projection, second analyzer Guest, cross-Run mutable cache or Host
AST rewriter is introduced.
