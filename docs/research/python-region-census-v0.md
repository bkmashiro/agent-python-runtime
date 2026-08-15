# Python region census v0

Status: **analysis-only measurement; every region consumer remains disabled**

`pysolate.python-region-census.v0` measures the verified candidate graph produced by
`pysolate.semantic-analysis.v3`. It consumes opaque `semantic.VerifiedAnalysis`
handles and emits only source digests, exact region fingerprints, counts, and a sealed
go/no-go decision. It does not execute, cache, schedule, retry, or publish a region.
The report binds the corpus digest plus analyzer, Guest artifact, execution-profile,
import-closure and capability-plan identities observed in every verified analysis; any
identity drift fails the entire census.

## Measurement unit

Each corpus program ID is treated as one independent logical agent submission. This
makes repeated fingerprints within one program a **same-agent proxy** and repeats
across program IDs a **cross-agent proxy**. The checked-in public corpus is deliberately
small and synthetic/runtime-derived; these counts establish mechanism opportunity, not
production prevalence.

An exact fingerprint binds canonical JSON containing:

- SHA-256 of the exact UTF-8 source slice selected by the Host-verified span;
- candidate kind, canonical live-in/live-out names and flags, data-dependency names,
  and effect summary;
- typed capability, canonical arguments, reachability, and dynamic occurrence facts;
- barriers and rejection reasons.

It excludes source position, whole-source identity, and source-bound region/call IDs so
that byte-identical region contracts can match across otherwise different programs.
The raw source slice is never emitted.

## Static materializability

A region is counted as `statically_materializable` only when the whole verified module
summary is effect-free and the region itself is straight-line, has canonical live-ins
and live-outs, produces at least one live-out, is effect-free, has no capability
occurrence, barrier, or rejection reason. Requiring a pure whole-module summary is
intentionally conservative: v0 distinguishes deferred function-body effects from
candidate evaluation but has no independently Host-parsed top-level effect coverage,
so it prefers false negatives over counting an under-approximated candidate.

This is an opportunity classification, not executable legality. It does not prove that
runtime input values can be captured, that output publication is safe, or that reuse is
equivalent. No consumer may treat the count as authority.

## Pre-registered decision

The report returns `go_for_executable_region_spike` only if the corpus contains both:

1. at least one statically materializable region; and
2. at least one exact statically materializable fingerprint appearing across distinct
   program IDs.

Otherwise it returns `no_go` with canonical reason codes. Either result keeps
`consumer_admitted=false`. A go result permits only a separate bounded executable-form
spike; it does not enable reuse. A no-go result retains whole-Run-only reuse.

## Frozen v0 result

The exact 19-program corpus produced 69 candidate regions. None passed the conservative
static-materializability screen because no whole-module summary was effect-free. Two
fingerprints repeated across program IDs, but both were independently blocked forms:
one non-canonical live-in/live-out straight-line region and one rejected declaration.
No materializable fingerprint repeated across programs and no fingerprint repeated
within a program. The pre-registered decision is therefore `no_go`, with both
`no_statically_materializable_regions` and
`no_cross_program_exact_materializable_overlap`; whole-Run-only reuse remains the only
reuse consumer.

## Reproduction

The existing effectgraph command analyzes every bound corpus source once with the exact
Guest artifact and writes both reports:

```bash
go run ./research/effectgraph/cmd/effectgraph-census \
  -artifact /absolute/path/to/agent-python-runtime.wasm \
  -artifact-source-commit <40-hex-commit> \
  -region-report-output docs/evidence/python-region-census-v0.json
```

The command fails closed on a source-commit claim that is not a local Git commit,
missing verified analyses, source-digest drift, analyzer identity drift, malformed spans,
output validation, or atomic-file write failure. Artifact bytes are independently hashed
and bound through every verified analysis and the generation marker. It
replaces the corpus and three reports individually, then replaces
`effectgraph-census-bundle-v1.json` last. The generation marker binds the SHA-256 of the
corpus plus effect, region, and placement reports; an interrupted mixed generation
therefore fails `-verify-bundle` and the
Track F release check rather than appearing current.
