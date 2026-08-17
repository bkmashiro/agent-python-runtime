# Natural Source-Prefix Opportunity Census v1

Status: **structural opportunity census; not performance evidence**

## Question

How often does the frozen tau2 T2 remediation-v2 READ cohort contain a complete top-level source region after the region that contains its Host-mediated external READ?

This is the natural-workload fit check for Source-Prefix Execution Overlap. It does not replay model generation, synthesize chunk timing, or measure latency.

## Frozen protocol

- Parent cohort: 36 frozen remediation-v2 READ events.
- Duplicate source bodies remain separate denominator events.
- Source bodies, task/turn labels, arguments, content and private paths remain private.
- Each source is analyzed by the exact CPython/WASI Guest semantic analyzer.
- Guest `candidate_regions` are the top-level structural units; the Host does not parse Python.
- One verified `external_read` call site is mapped to exactly one candidate region.
- `structurally_eligible` means that region has at least one trailing candidate region.
- `structurally_ineligible` means the READ is in the final or only candidate region.
- Generation/chunk timing is not present, so every event is `timing_status=not_recorded`.

No Agent program was executed, no capability was dispatched, and no provider was called.

## Exact identities

```text
Parent remediation report SHA  sha256:5f504a138084f933ba0fd4f3bec7aede7076924ec3c2a5cfb8f05db3dd9a513f
Preregistration SHA             sha256:f824f307a9fc4deaceca150c6f236686b1718c81820cf5362a79c7f256efe3e7
Guest source commit             501daef99796c1af7cd7bab1e0ab712a199820b9
Guest artifact SHA              sha256:a443042fb080d22f8e352aca0d0c8a5c87a7801e8afcc603e174d75fbe11c69b
Guest manifest SHA              sha256:c3bae8db19e0a372101dea11c6873f71ce849dd992b92ac3eba4a4352ddb4045
Census harness commit           61112106e20959e5894414ca991f8bac2699dd92
Public evidence identity        sha256:13120c7ec8565fe7599c0c3f362a0ae90deeb67cafdd986dafa4a8cac70d714a
Public evidence file SHA        sha256:cfedf4adfe63051d9e7b233ef8b36031fb4fda360a7d32e0e634cdce31da5604
```

Preregistration: `docs/evidence/source-prefix-opportunity-census-preregistration-v1.json`

Accepted aggregate: `docs/evidence/source-prefix-opportunity-census-v1.json`

## Result

```text
Frozen READ events                         36
Unique source bodies                       30
Structurally eligible                       0
Structurally ineligible                    36
Timing not recorded                        36

Candidate regions per event                 1 × 36
READ region index                           0 × 36
Trailing candidate regions                  0 × 36
Reason: READ is final or only region        36 × 36
```

All source bodies were 48–57 bytes, but only aggregate bounds are reported here; bodies are not included in public evidence.

## Interpretation

The frozen tau2 T2 READ cohort contains **no natural Source-Prefix Execution Overlap admission opportunities** under the preregistered structural definition. Every event consists of one Guest-confirmed top-level candidate region containing the READ, with no later source region to generate concurrently.

This does not refute the authored mechanism result. It bounds it:

- the authored mechanism fixture shows that reach-gated source-prefix overlap can reduce the mechanism window when an early READ is followed by a source-generation tail;
- this frozen natural cohort does not contain that workflow shape;
- therefore the authored mechanism result must not be presented as tau2 or general natural-workload uplift.

## Decision gate

Do **not** run trace-derived timing replay for this cohort: there are zero structurally eligible cases, so timing replay cannot answer an overlap question.

Any future natural validation must begin with a different, independently frozen corpus containing multi-region Agent programs. It must not select cases by observed speedup, and it must retain the same zero-provider structural census before timing analysis.

## Claim boundary

Supported:

- structural opportunity frequency in these 36 frozen remediation-v2 READ events;
- exact-Guest classification that all 36 READs occur in their final or only candidate region;
- absence of provider/chunk timing evidence for the cohort.

Not supported:

- latency or speedup;
- provider generation timing;
- natural benchmark performance uplift;
- dynamic DAG scheduling;
- production external effects;
- prevalence outside this frozen cohort.
