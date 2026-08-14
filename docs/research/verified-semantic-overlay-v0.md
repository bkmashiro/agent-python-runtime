# Target-Guest verified semantic overlay v0

Status: **Track C implementation; real-Guest evidence pending**

The v0 overlay extends `pysolate.semantic-analysis.v1` with a bounded `call_sites`
array. It is analysis-only metadata. Original Python remains the sole executable
authority.

## Accepted call shape

The target-Guest CPython AST emits a call site only when all of the following hold:

- the call is the direct value of a top-level expression or name assignment;
- the callable is an exact Host-projected module/method or global alias;
- every projected argument is supplied exactly once as a bounded JSON scalar literal;
- there is no `*args`, `**kwargs`, missing argument or unknown keyword;
- the source occurrence is unique and bounded.

`necessarily_reached=true` is narrower: the call must be the first executable
module statement, ignoring only a module docstring. A second call, calls under
branches/loops/functions, aliases, data-dependent arguments and all complex call
shapes remain absent or non-necessarily-reached. Absence means unknown, not safe.

## Canonical call-site record

```text
id = SHA256(domain + exact source digest + capability + exact span)
span
capability
control_region_id = SHA256(domain + exact source digest + module-entry)
necessarily_reached
arguments_canonical = true
canonical_arguments = sorted compact JSON object
dynamic_occurrence = 1
```

The overlay is capped at 256 records. Arguments are scalar JSON literals capped at
4 KiB each; the Host caps the complete canonical argument object at 64 KiB. Schema
and analyzer identities advance to `semantic-analysis.v1` and
`semantic-analyzer.v2`.

## Host validation and provenance

The Host strict decoder rejects unknown fields, malformed spans/digests, duplicate or
unsorted call IDs, non-canonical argument JSON and any occurrence other than one.
`Analyze` then rebinds each record to:

- exact source, artifact, execution profile, import closure and capability plan;
- one exact capability projection and its ordered argument names;
- recomputed source occurrence and module-entry control-region IDs;
- conservative module effect coverage for workspace/external reads.

Only `AnalyzeVerified` can mint opaque `VerifiedAnalysis` from a target Runner whose
properties stay unchanged and expose neither workspace nor capability Broker. Track C
adds no legality predicate and starts no physical request. Track D must join these
verified program facts with capability-plan v5 and return typed rejection reasons.

## Explicit non-goals

v0 has no SSA, CFG execution, general alias analysis, heap/resource algebra,
conditional reachability, loops, nested-function occurrence model, exception motion,
coalescing or Python rewrite. It does not execute the overlay and does not claim that
structural call sites are legal pre-dispatch opportunities.
