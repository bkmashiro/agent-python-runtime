# Target-Guest verified semantic overlay v0

Status: **Track C complete; runtime consumer remains disabled**

The v0 overlay extends `pysolate.semantic-analysis.v2` with a bounded `call_sites`
array and required `call_site_coverage="positive_only"` marker. It is analysis-only
metadata; an empty array never proves that the source contains no capability call.
Original Python remains the sole executable authority.

## Accepted call shape

The target-Guest CPython AST emits a call site only when all of the following hold:

- the call is the direct value of a top-level expression or name assignment;
- the callable is an exact Host-projected module/method or global alias;
- every projected argument is supplied exactly once as a bounded JSON scalar literal;
- there is no `*args`, `**kwargs`, missing argument or unknown keyword;
- the source occurrence is unique and bounded.

`necessarily_reached=true` is narrower: after successful trusted-prelude setup under
the exact bound profile, the call must be the first executable module statement,
ignoring only a module docstring. A second call, calls under branches/loops/functions,
aliases, data-dependent arguments and all complex call shapes remain absent or
non-necessarily-reached. Absence means unknown, not safe.

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
4 KiB each; the Host caps the complete canonical argument object at 64 KiB. Values
whose Python and Host encoders do not agree byte-for-byte are rejected by the Host.
Schema and analyzer identities advance to `semantic-analysis.v2` and
`semantic-analyzer.v3`.

## Host validation and provenance

The Host strict decoder rejects unknown fields, malformed spans/digests, duplicate or
unsorted call IDs, non-canonical argument JSON and any occurrence other than one.
`Analyze` then rebinds each record to:

- exact source, artifact, execution profile, import closure and capability plan;
- the exact expected target-Guest analyzer identity; occurrence truth remains supplied by
  that bound Guest because the Host deliberately does not implement a second Python parser;
- one exact capability projection and its ordered argument names;
- recomputed source occurrence and module-entry control-region IDs;
- conservative module effect coverage for workspace/external reads and workspace writes.

Only `AnalyzeVerified` can mint opaque `VerifiedAnalysis` from a target Runner whose
properties stay unchanged and expose neither workspace nor capability Broker. Track C
adds no legality predicate and starts no physical request. Track D must join these
verified program facts with capability-plan v5 and return typed rejection reasons.

## Evidence

The signed implementation commit is
`44d060d3ff9b4fc5cdd9b2bad56170182bab840c`. A target-Guest artifact built from that
commit has SHA-256
`370ebb7d680d166123928be9b584b13679db34aab6f846303d75b4005d9dbb5f`.
Real-Guest regressions prove an exact first-statement source call emits one bound
necessarily-reached site while the corresponding conditional call emits none.
Independent review then tightened the boundary: top-level shadowing/import rebinding is
tracked before extraction, only the first string statement is a docstring, write effect
coverage is mandatory, structured argument values are rejected, Python/Go canonical
string encoding is aligned, analyzer identity is exact and coverage is explicitly
`positive_only`.

The 19-program corpus reports four exact overlay call sites and one
necessarily-reached site, with zero analyzer-unclassifiable programs. Two local runs
were byte-identical; Linux ARM64 reproduced the same corpus and report file hashes.
See the [census follow-up](effect-aware-opportunity-census.md) for the exact identities
and G1 decision.

## Explicit non-goals

v0 has no SSA, CFG execution, general alias analysis, heap/resource algebra,
conditional reachability, loops, nested-function occurrence model, exception motion,
coalescing or Python rewrite. It does not execute the overlay and does not claim that
structural call sites are legal pre-dispatch opportunities.
