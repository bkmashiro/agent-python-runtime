# Source-bound pass workload evidence v2

Status: **synthetic positive mechanism control; coding-shaped prevalence remains negative**

## Why v2 exists

The v1 closeout treated six coding-shaped fixtures as if their zero admissions were a
useful stopping result for the optimizer study. That was too restrictive. A paper can and
should use authored synthetic workloads to establish that a mechanism works under the
conditions it was designed for. Natural or coding-shaped corpora answer a different
question: how often those conditions appeared in that sample.

The v1 evidence validator also tried to authenticate the one endorsed artifact by
rejecting any internally consistent variation of descriptive fields and by defending a
small internal census against `uint32` overflow. That threat model does not apply here.
The repository generator, signed Git history and reviewed commit identify the endorsed
artifact. The v2 decoder checks schema shape, digest syntax, row accounting and internal
consistency; it is not an artifact-signature system. Aggregate counters use `uint64`
instead of adding a special overflow gate.

## Corpus

[`source-bound-pass-authored-workload-preregistration-v2.json`][prereg] contains:

- the six v1 coding-shaped cases for repository reads, projection, batching, independent
  reads, parsing and prepared array setup; and
- `S01`, an intentionally synthetic one-call `sources.read` case whose capability contract
  declares exact Plan-epoch freshness, private partitioning, discard-with-disposition and
  no coalescing.

`S01` is a positive control. It is not sampled from a natural workload and is not used to
estimate prevalence.

## Exact-Guest result

[`source-bound-pass-authored-workload-evidence-v2.json`][evidence] was generated with:

- Guest artifact source `3cb8d255d8603fb341a35945295596977d957e12`;
- Guest artifact `sha256:5381f601e88c8d196d1069de15629267a783372fe5ee7bb8ae786fdf48e2210e`;
- harness source `4e43f98f23f7620b915b6b6e95f460bf9bd6d83b`.

| Measure | Count |
|---|---:|
| total cases | 7 |
| candidate regions | 18 |
| locally reusable regions | 1 |
| capability call sites | 7 |
| semantic pre-dispatch admitted | 1 |
| semantic pre-dispatch rejected | 6 |

The six coding-shaped cases remain at zero admitted decisions. The new synthetic `S01`
control has one call site and one admitted decision with no rejection. This establishes a
real positive source-bound planning path on the exact Guest while preserving the negative
prevalence observation for the sampled coding-shaped cases.

## Existing synthetic timing evidence

The repository already contains two matched synthetic source-overlap campaigns. Their raw
rows independently give:

| Fixture | Baseline median | Streaming median | Reduction | Speedup |
|---|---:|---:|---:|---:|
| `source-prefix-overlap-v1` | 2,950,046,792 ns | 1,533,922,917 ns | 48.00% | 1.923x |
| `source-prefix-day-trip-v2` | 2,942,787,333 ns | 1,537,471,584 ns | 47.75% | 1.914x |

Both fixtures use a 1.5-second deterministic read and roughly 1.4 seconds of source-tail
generation. They demonstrate overlap capacity in that constructed coordinate. They do not
show that ordinary coding workloads frequently contain the same opportunity.

## Claim boundary

The combined result supports three separate statements:

1. exact-Guest positive control: the retained semantic pre-dispatch pass admits a fixture
   that satisfies its contract;
2. synthetic performance capacity: matched source generation and read latency can overlap
   in the two checked-in timing fixtures;
3. sampled prevalence: the six coding-shaped fixtures and the separate 36-event natural
   sample did not contain an admitted source-prefix opportunity.

It does not support a general production speedup or any claim about deferred scalar CSE,
projection, batching, parallelization, array or cohort passes.

[prereg]: ../evidence/source-bound-pass-authored-workload-preregistration-v2.json
[evidence]: ../evidence/source-bound-pass-authored-workload-evidence-v2.json
