# Source-bound pass workload evidence v1

Status: **closed negative opportunity result; retained mechanisms remain valid**

## What was frozen

[`source-bound-pass-authored-workload-preregistration-v1.json`][prereg]
was signed and pushed before the census. It fixes six authored coding-shaped
categories:

1. repeated repository reads;
2. bounded projection after a read;
3. comprehension-shaped batch reads;
4. independent reads;
5. read followed by pure parsing; and
6. prepared array setup.

The source bodies are authored fixtures in
`research/sourceboundpasses/workload_preregistration.go`. The committed
preregistration is body-free and binds their source digests, sizes, imports,
treatments, matched dimensions and oracles.

The separate natural anchor is the previously frozen 36-event, 30-source
coding-agent census in [`source-prefix-opportunity-census-v1.json`][natural].
Its private bodies remain outside the repository.

## Exact-Guest authored census

[`source-bound-pass-authored-workload-evidence-v1.json`][census] was produced
with:

- artifact source: `3cb8d255d8603fb341a35945295596977d957e12`;
- artifact source tree: `0a87b3ac43cb9b12093a0b512fe513f811250606`;
- NumPy-core artifact: `sha256:5381f601e88c8d196d1069de15629267a783372fe5ee7bb8ae786fdf48e2210e`;
- artifact manifest: `sha256:060182e507a69ac38a1b148a377f03366edb1a7cac7d5e81f77783e522f456b6`;
- harness source: `7114598360b192d7e669b93cc3237e79d073e936`;
- one sealed Plan, execution profile and in-memory workspace for all six cases.

The bounded gpu31 build completed in 474,863 ms. The census found:

| Measure | Count |
|---|---:|
| authored cases | 6 |
| candidate regions | 16 |
| locally reusable regions | 1 |
| exact capability call sites | 6 |
| `semantic_pre_dispatch` admitted | 0 |
| `semantic_pre_dispatch` rejected | 6 |

This is an exact-Guest structural census, not an execution benchmark. A locally
reusable region is not automatically a `prepared_pure_region` decision. The
prepared pass still needs its sealed decision, scratch capsule, target-Guest
patch and final selection lifecycle. Therefore the report labels prepared-only
execution as candidate-census-only and labels all-admitted execution not
applicable without a shared qualified fixture.

The natural corpus remains a stronger prevalence check: 0 of 36 frozen READ
events had eligible source-prefix overlap, and no timing was recorded.

## Retained mechanism controls

Current exact-Guest controls against the artifact above passed:

- semantic analyzer default-off: 3.32 s;
- three-read streaming prefix pre-dispatch: 23.31 s;
- prepared-region target-Guest patch emission: 10.69 s;
- baseline/derived prepared-region selection: 28.64 s;
- exception-before, exception-after and pre-cancel adversarial selection suite:
  66.27 s.

The exact-Guest run exposed a stale integration fixture: it had sealed the
prepared decision with a placeholder AST digest, while the target Guest emitted
the real final AST digest. Production validation correctly rejected the join.
The test now obtains the AST digest from the same target Guest before sealing the
decision. No runtime contract or production execution path changed.

Historical matched synthetic evidence remains useful only for mechanism
capacity:

- [`source-prefix-overlap-v1.json`][overlap] records three matched pairs with
  identical result, logical-call and physical-dispatch oracles. Median wall time
  was 2,950,046,792 ns for generate-then-execute and 1,533,922,917 ns for
  streaming under its fixed 1.5 s Host delay. This supports overlap in that
  synthetic coordinate, not natural coding-workload uplift.
- [`semantic-speculation-phase5-selection-evidence-v1.json`][prepared] records
  baseline and derived result `42`, one formal Guest execution, one capsule
  claim, no Broker or workspace, and fail-closed drift controls. It does not
  supply optimizer economics.

## Decision

Phase 8 closes with a negative opportunity result:

- retained mechanisms still satisfy their exact semantic and lifecycle gates;
- neither authored nor natural evidence supports a new coding-workload speedup
  claim;
- no shared fixture admits both retained passes, so an all-admitted performance
  row would be synthetic bookkeeping rather than useful evidence;
- deferred scalar CSE, projection, batch, parallel, array and cohort passes are
  not counted as implemented or measured.

The evidence supports keeping the thin pipeline and existing mechanisms. It does
not justify widening the runtime, Broker or source-patch ABI.

[prereg]: ../evidence/source-bound-pass-authored-workload-preregistration-v1.json
[census]: ../evidence/source-bound-pass-authored-workload-evidence-v1.json
[natural]: ../evidence/source-prefix-opportunity-census-v1.json
[overlap]: ../evidence/source-prefix-overlap-v1.json
[prepared]: ../evidence/semantic-speculation-phase5-selection-evidence-v1.json
