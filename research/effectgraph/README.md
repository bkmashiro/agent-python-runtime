# Effect-aware generated-program corpus

This package owns the Track A body-safe corpus and opportunity census for the
unified effect-aware runtime roadmap.

## Safety and provenance

- `public_synthetic` programs are project-owned adversarial or mechanism fixtures.
- `public_runtime_workload` programs are exact copies of the three canonical public
  workloads in `research/workloads`; a test rejects drift.
- private Hermes-history seeds contain only SHA-256 identity labels produced locally
  under the `pysolate.effectgraph-history.v0` convention, coarse task shape and replay
  status. No prompt, generated body, input, output, channel, user or credential is
  checked in. Because preimages are intentionally absent, validation proves digest
  shape and body-free status—not the declared preimage/domain derivation.
- `manifest.json` intentionally contains no source body. The census builder reads
  only relative bounded files, computes exact SHA-256 identities and emits
  `corpus.json` bound to the artifact, execution profile, import closure, frozen
  capability plan and contract set.

Historical seeds are `not_generated` and `prospective_replay=true`; they are not
counted as analyzed programs or evidence of opportunity until a consented replay
produces a body-safe program.

## Interpretation

Manifest candidate labels are structural annotations counted in static source
occurrences. They are not legality proofs. Every opportunity row therefore records
`proved_legal=0`, `legality=not_evaluated` and
`observational_equivalence=not_evaluated` in Track A.

An analyzer rejection remains in the denominator as `unclassifiable`; it is never
silently dropped. Report v1 also counts bounded verified-overlay call sites and the
strict subset marked necessarily reached. These remain structural Guest facts, not
Host legality proofs. The report contains digests, bounded classifications and
aggregate counts, but no program source.

The target-Guest CPython AST remains the frontend authority and the original Python
remains executable authority. This package performs no source rewriting, graph
execution, automatic parallelization or request pre-dispatch.

## Reproduce

Given an exact Guest artifact:

```bash
go test ./research/effectgraph/... -count=1
go run ./research/effectgraph/cmd/effectgraph-census \
  -artifact /absolute/path/to/agent-python-runtime.wasm \
  -artifact-source-commit <40-hex-source-commit>
```

The command strictly decodes `manifest.json`, reconstructs the Host-owned fixture
capability plan, analyzes every exact source with that artifact, runs the existing
pre-execution placement oracle, and atomically rewrites:

- `research/effectgraph/corpus.json`
- `docs/evidence/effect-aware-opportunity-census.json`

A second run with the same artifact and source tree must be byte-identical.
