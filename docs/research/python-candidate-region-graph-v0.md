# Python candidate region graph v0

Status: **analysis-only implementation; no region consumer is enabled**

Track F extends the verified Guest CPython semantic overlay to
`pysolate.semantic-analysis.v3`. The Guest's existing CPython `ast` parser emits a
bounded candidate graph for module-entry statements. The original Python program
remains the only execution authority: the graph cannot run, rewrite, split, retry,
place, cache, or pre-dispatch work.

## Contract

The fixed analyzer identity is `pysolate.semantic-analyzer.v6`. Every accepted
analysis carries `candidate_region_coverage="module_top_level_complete"` and an
explicit, non-null `candidate_regions` array. More than 256 module-level statements
is rejected rather than represented incompletely.

Each source-ordered candidate contains:

- an identity bound to source identity, ordinal, and exact source span;
- kind: `straight_line`, `opaque_control`, or `declaration`;
- the module-entry control-region identity and exact predecessor;
- source-name live-ins and live-outs, with separate canonicality claims;
- data dependencies bound to a prior producer region and live-in name;
- conservative effect summary and exact positive capability call-site occurrences;
- control/effect barriers and explicit rejection reasons.

Candidate boundaries are deliberately one top-level statement in v0. Internal
control flow is opaque; v0 does not pretend that Python heap state can be captured or
resumed between statements. Augmented assignment is modeled as both a read and a
write; `del` reads and then removes the binding from subsequent producer state.
Attribute/subscript mutation rejects the candidate as `heap_mutation`.
Explicit/non-trivial exception paths are rejected as `may_raise`;
unknown calls/effects, declarations, non-canonical boundaries, and opaque control all
remain visible rather than disappearing from the graph.

## Host verification

The Host rejects the complete analysis unless all of these hold:

- schema, analyzer, source, artifact, execution-profile, import-closure, and
  capability-plan identities match the request;
- arrays are present, bounded, sorted where canonical ordering is required, and
  source spans are ordered and contained by the module;
- region identities exactly match the source/ordinal/span derivation;
- the first candidate has no predecessor and every later candidate names exactly the
  previous source-ordered candidate;
- every data edge names a canonical live-in and an already-seen producer;
- every capability occurrence names a verified positive module-entry call site contained
  by the candidate, every such positive call site appears exactly once, and the
  candidate effect summary covers its typed capability effect;
- `candidate_region_count` exactly seals the list and every reported span fits the exact
  UTF-8 request source;
- candidate effects cover evaluation of each top-level statement only. `module_effects`
  additionally summarizes deferred function/class bodies, so their union is deliberately
  not required to equal or cover the whole-module summary. Because the Host does not
  parse Python to prove top-level effect completeness independently, v0 consumers must
  fail closed on candidate purity whenever the whole-module summary is effectful;
- opaque/non-canonical/unknown candidates carry the corresponding rejection reason.

This is validation of a versioned Guest analysis contract, not a second Python
parser. The Host does not infer missing regions or reconstruct Python semantics.
A comment-only nonblank source therefore fails closed at the Host rather than asking
it to distinguish Python comments from executable statements.

## Consumer boundary

No runtime consumer accepts candidate regions in v0. Existing whole-Run reuse and
semantic pre-dispatch retain their prior narrow legality and default-off mechanism
boundaries. A future consumer requires a separate gate and must prove exact state,
effect, exception, cancellation, freshness, authority, privacy, and resource
semantics for its own accepted surface.

The Lab consumes only `pysolate.lab-semantic-regions.v0`, a bounded privacy-safe
projection minted by `labview.ProjectSemanticRegionGraph` from opaque
`semantic.VerifiedAnalysis`. Portable projections omit source text; private local
projections and the browser re-hash private source before rendering it. Portable
projections never fall back to the surrounding debugger scenario source. The Lab Web
`Regions` panel renders
source highlighting, control order, data dependencies, effect classes, capability
occurrences, barriers, and rejection reasons. Its renderer does not infer effects,
liveness, or eligibility from source, and UI actions cannot become dispatch
authority.
