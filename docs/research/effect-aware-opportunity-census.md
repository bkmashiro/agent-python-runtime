# Effect-aware opportunity census

Status: **Track A frozen; Track C overlay follow-up complete**

This report separates the original structural census from the Track C verified-overlay
follow-up. Neither result grants authority to pre-dispatch, overlap, retain or replay a
call.

## Evidence lineage

### Frozen Track A baseline

The original 18-program capability-plan v4 census is recoverable from evidence source
commit `c97f98bfbfbb0dd23aaab29fb4fe5c3b04f6dac4`. Its Guest analyzer source was
`950249a92eaec648b88850300c5653ab62aff888`, with analyzer identity
`sha256:3836f6a91bd52a5d03c29043c0f9f034ccdfdbddacf974e0ab44c3a8666463ff`.
That checkout is the immutable Track A record; later files intentionally contain the
versioned Track C follow-up and do not masquerade as the old census.

### Track C bound evidence

- Corpus generator: [`research/effectgraph/`](../../research/effectgraph/)
- Bound corpus: [`research/effectgraph/corpus.json`](../../research/effectgraph/corpus.json)
- Machine census: [`docs/evidence/effect-aware-opportunity-census.json`](../evidence/effect-aware-opportunity-census.json)
- Overlay contract: [`verified-semantic-overlay-v0.md`](verified-semantic-overlay-v0.md)
- Guest artifact source commit:
  `76a4efeabcb3344f354845dd3c7700b46e42a539`
- Guest artifact SHA-256:
  `ff6d2e3dfa424e9d1f064711b6845b6cd13383f337fb9c153e206c4b7c6f7c5e`
- Analyzer identity:
  `sha256:99b45fb47682547354048af86e701cb7ed929f446219de58292a59b863dfa6e2`
- Corpus identity:
  `sha256:ee773aa9cc6e2d06c5e11372c8b7dcdb3d82b2603848fcdefb1a8c3e1d5be241`
- Corpus file SHA-256:
  `96a97186f38fc2f078679adad22f82d456d2f81ac07753e92e09eb0cd3948ed7`
- Report file SHA-256:
  `e4c77dde645add8a23e776f03eee04a800dea89ec02d6e40f6f2391c8a100402`

The raw CPython/WASI Guest and toolchain were built on Linux x86-64 from the signed
implementation lineage. The review-fix commit changed only Guest Python, Go and docs;
its exact `semantic.py` was installed into the deterministic VFS and the unchanged raw
Guest was repacked and validated with pinned `wasi-vfs` and `wasm-tools`. The initial
full build's optional Go probe step could not run on that build node because Go was not
installed; the resulting validated artifact was therefore downloaded, hash-checked
and exercised through the repository's Go/Wazero E2E and census commands instead.

Two complete local census runs were byte-identical. A cross-compiled ARM64 census
binary then analyzed the same artifact on Linux
`6.12.0-202.76.4.1.el9uek.aarch64` and reproduced both file hashes exactly.

## Corpus

The Track C denominator is 19 checked-in programs:

- 16 project-owned synthetic/adversarial or mechanism fixtures;
- 3 exact copies of canonical public runtime workloads in `research/workloads`.

Track C adds only `module-entry-read`, a body-safe positive mechanism fixture whose
first executable statement is one exact literal source read. The explicit addition
lets the overlay prove one necessarily-reached occurrence without reclassifying the
18-program Track A baseline silently.

Three private-history seeds remain body-free SHA-256 labels under the local
`pysolate.effectgraph-history.v0` convention. Their preimages are intentionally absent;
validation proves digest shape and body-free status, not derivation. They remain
`not_generated`, require prospective replay and are excluded from the denominator.

## Track C results

| Measure | Result |
|---|---:|
| Programs analyzed | 19 |
| Analyzer-unclassifiable programs | 0 |
| Programs with opaque/unknown semantics | 10 (52.6%) |
| Programs without barriers | 9 (47.4%) |
| Currently whole-Run reusable | 2 (10.5%) |
| Pysolate/WASM placement | 16 (84.2%) |
| Native placement | 3 (15.8%) |
| Distinct direct function/capability references | 7 |
| Bounded exact overlay call sites | 4 |
| Overlay calls necessarily reached | 1 |
| Barrier instances | 39 |

Structural annotations remain non-authoritative:

| Opportunity kind | Static occurrences | Legality/equivalence |
|---|---:|---|
| Exact pre-dispatch call sites | 11 | Not evaluated |
| Useful overlap windows | 5 | Not evaluated |
| Exact repeated-region candidates | 2 | Not evaluated |
| WASM placement candidates | 16 | Not evaluated |
| Native placement candidates | 3 | Not evaluated |

The verified Guest overlay narrows 11 source annotations to four exact, literal,
source-bound call records. Only the added first-statement fixture satisfies the v0
necessarily-reached rule. The three other records are retained as exact occurrences
but are not promoted to must-reach facts. Conditional, looped, aliased and dynamic
calls remain absent or opaque.

## What changed relative to Track A

1. The Host can now distinguish exact source occurrences and canonical literal
   arguments instead of seeing only aggregate function capability references.
2. Exact call IDs bind source digest, span and capability; control-region IDs bind the
   exact source and module-entry domain. Host validation recomputes both and joins
   every call to the frozen capability projection.
3. The unchanged original Python remains executable authority. The overlay is strict
   metadata, not an IR, scheduler, cache key or executable plan.
4. Opaque evidence remains material: 10 programs and all 39 previous barriers are
   preserved rather than widened into the accepted subset.
5. The gap from 11 structural annotations to four exact calls and one must-reach call
   is the useful result. It quantifies why shared Host legality must remain fail-closed.

## G1 decision

**Continue to Track D, but keep the runtime consumer disabled.** The minimal overlay
answers a question the old report could not: which source annotation is an exact,
canonical, source-bound call occurrence, and which of those is necessarily reached in
the accepted v0 subset. The corpus yields one positive and multiple negative cases
without AST rewriting or a general CFG/IR.

This is sufficient to build and differentially test a shared legality engine. It is
not sufficient to dispatch anything. Track D must still join Host-owned effect,
resource, freshness, authority, budget, exception and terminal-disposition contracts,
and must reject every fact absent from the exact overlay.

## Historical frontend defect

The first Track A census exposed duplicate `dynamic_import` barriers for
`import csv,json`. The analyzer deduplicates exact `(function, code, span)` barriers
before enforcing the bound and retains an explicit `unclassifiable` denominator path.
Track C preserves that behavior under analyzer v2 and reports zero unclassifiable
programs.

This remains mechanism evidence, not a population-level claim about natural
agent-generated Python.
