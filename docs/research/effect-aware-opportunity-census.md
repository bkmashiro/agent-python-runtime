# Effect-aware opportunity census

Status: **Track A complete — structural census only**

This report records the first body-safe corpus and opportunity census for the
[unified effect-aware runtime roadmap](../plans/2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md).
It does not claim that any call is legal to pre-dispatch, overlap, retain or replay.

## Bound evidence

- Corpus manifest and generator: [`research/effectgraph/`](../../research/effectgraph/)
- Exact generated corpus: [`research/effectgraph/corpus.json`](../../research/effectgraph/corpus.json)
- Machine-readable census: [`docs/evidence/effect-aware-opportunity-census.json`](../evidence/effect-aware-opportunity-census.json)
- Guest source commit used to build the analyzer artifact:
  `950249a92eaec648b88850300c5653ab62aff888`
- Guest artifact SHA-256:
  `a62ae62b13a502152673e1c40c7bee80412d1724302bf8922eb7e3d86ce70473`
- Corpus identity:
  `sha256:bd97d3695d3e218c8de9774f132c06e38a1e3dd853f29ec9ea3b6a3423d5ec64`
- Analyzer identity:
  `sha256:3836f6a91bd52a5d03c29043c0f9f034ccdfdbddacf974e0ab44c3a8666463ff`

The artifact was relinked and repacked on Linux x86-64 from the pinned existing
CPython/WASI build inputs plus the exact source commit above, validated with
`wasm-tools`, downloaded, hash-checked locally, and then exercised by the census.
Two complete local census runs were byte-identical. A cross-compiled census binary
then ran the same Guest artifact on Linux ARM64 (`6.12.0-202.76.4.1.el9uek.aarch64`)
and produced byte-identical corpus/report SHA-256 values
`1ff41480116f9259c7481585f284899786687fdf253e043c163bd06edce64de3` and
`96820d6be4c13e12b24a05ce6be49358eb6b50cb4e5d733d3e8446cbf63940cb`.

## Corpus

The analyzed denominator is 18 checked-in programs:

- 15 project-owned synthetic/adversarial programs;
- 3 exact copies of the canonical public runtime workloads in
  `research/workloads`.

The corpus also records three private-history seeds as SHA-256 identity labels made
under the local `pysolate.effectgraph-history.v0` convention and coarse task shapes
only. Their preimages are intentionally absent, so validation proves only digest shape
and body-free status, not the stated derivation. They are `not_generated`, require
prospective replay, contain no source body or content-derived result, and are excluded
from the 18-program analyzed denominator.

The corpus covers independent reads, read/write conflicts, conditional and looped
reads, exception order, clock/random observations, aliases, `eval`, dynamic imports,
subprocess placement, ordinary WASI file access, pure/repeated computation, and the
three public structured/stateful/planning workloads.

## Results

| Measure | Result |
|---|---:|
| Programs analyzed | 18 |
| Analyzer-unclassifiable programs | 0 |
| Programs with opaque/unknown semantics | 10 (55.6%) |
| Programs without barriers | 8 (44.4%) |
| Currently whole-Run reusable | 2 (11.1%) |
| Pysolate/WASM placement | 15 (83.3%) |
| Native placement | 3 (16.7%) |
| Distinct direct function/capability references | 7 |
| Barrier instances | 39 |

Structural annotations identify:

| Opportunity kind | Static occurrences | Legality/equivalence |
|---|---:|---|
| Exact pre-dispatch call sites | 10 | Not evaluated |
| Useful overlap windows | 5 | Not evaluated |
| Exact repeated-region candidates | 2 | Not evaluated |
| WASM placement candidates | 15 | Not evaluated |
| Native placement candidates | 3 | Not evaluated |

The 10 pre-dispatch annotations are source-level opportunities, not qualified
requests. They include adversarial cases such as conditional execution,
read-after-write and exception ordering that later legality should reject unless all
required facts are explicitly proven.

## What the census says

1. **There is enough opportunity to justify one minimal semantic-overlay experiment.**
   Ten statically identified call sites and five potential overlap windows are enough
   for a falsifiable Track B/C slice without introducing Python rewriting or an IR
   executor.
2. **The current whole-Run report is too coarse for that question.** It reports seven
   direct capability references inside function summaries but no exact call
   occurrence, canonical argument dependency, control containment or claim identity.
   Module-level direct calls are not represented as call occurrences at all.
3. **Conservatism is material, not incidental.** Ten programs are opaque and all three
   public runtime workloads hit barriers. The 39 barriers are mostly dynamic calls
   (21), unsupported control flow (12) and dynamic imports (5). Track C must preserve
   those as rejection evidence rather than widen the accepted Python subset casually.
4. **Whole-Run reuse is not the main opportunity.** Only two pure fixtures satisfy the
   existing exact zero-Host-call whole-Run retention rules.
5. **Placement remains useful and separate.** Static import/requirements placement
   selects 15 programs for WASM and three for native without executing either backend.

## Discovered frontend defect

The first census attempt exposed duplicate `dynamic_import` barriers for
`import csv,json`: both aliases shared one source span, so the strict Host validator
rejected the whole report instead of recording an opaque program. The target-Guest
analyzer now deduplicates exact `(function, code, span)` barriers before enforcing the
bound, increments its analyzer identity, and has a regression test. The final census
contains zero unclassifiable programs. The census implementation still retains an
explicit `unclassifiable` denominator path for future analyzer failures.

## Decision

Proceed to Track B and then a minimal Track C verified semantic overlay, with these
limits:

- the overlay is source-indexed analysis metadata, not executable IR;
- original Python remains execution authority;
- Track A candidate labels grant no authority and prove no legality;
- the first legality target is exact request pre-issue/claim identity, not sibling
  scheduling or AST rewriting;
- no pre-dispatch implementation begins before the shared legality and differential
  oracle gate;
- private-history seeds remain excluded until an explicit body-safe replay exists;
- if the overlay cannot qualify a useful subset of the ten annotated sites without
  weakening opaque/control/exception/freshness boundaries, G1 should stop the runtime
  consumer rather than broaden the Python subset.

This is evidence to continue a narrow experiment, not evidence for a population-level
claim about natural agent-generated Python.
