# Observable workflow-boundary evaluation v0

Status: **Bounded research result; final claim frozen**
Date: 2026-08-15

## Frozen subjects

- Paired workload seed: `20260815`
- Harness source: `1eebbb95a75f1ae6d29277ab54e97062b0932fb4`
- Guest source: `955b780db5aee60e6cec3f869509857600471e01`
- Guest artifact: `sha256:80d69d364d0c5d58b5fd4972d04b2b8c9d6f395a3543fd80d9dd074923384c51`
- Workloads: 14 prepared explicit workflows; 4 positive classes, 8 one-dimension
  near-match negatives and 2 ordinary controls.
- Evidence: `docs/evidence/workflow-benchmark-evidence-v0.json` (191,081 bytes).

The baseline and optimized treatments used the same sealed manifest and shuffled order.
Model request/output intervals are deterministic local **replay fixtures**; Host tool and
Guest WASM intervals are measured. There was no paid or live model provider.

## Paired result

| Workload class | Tasks | Baseline physical | Optimized physical | Admitted | Rejected | Baseline elapsed | Optimized elapsed |
|---|---:|---:|---:|---:|---:|---:|---:|
| preissue | 1 | 1 | 1 | 1 | 0 | 2,190.35 ms | 2,199.69 ms |
| declared parallel | 1 | 2 | 2 | 1 | 0 | 2,206.21 ms | 2,199.32 ms |
| coalesced | 1 | 2 | 1 | 1 | 0 | 2,336.16 ms | 2,336.05 ms |
| retained reuse | 1 | 2 | 1 | 1 | 0 | 2,208.67 ms | 2,212.62 ms |
| near match | 8 | 16 | 16 | 0 | 8 | 17,774.39 ms | 17,733.45 ms |
| ordinary | 2 | 2 | 2 | 0 | 0 | 4,387.09 ms | 4,438.67 ms |
| **Total** | **14** | **25** | **23** | **4** | **8** | **31,102.87 ms** | **31,119.80 ms** |

The 14 sealed task reports contain 132 spans, 50 logical requests, 48 physical
executions and 12 optimization decisions. Every paired terminal output/effect oracle
matched; unclassifiable divergence was zero. The median task elapsed interval was
2,205.90 ms baseline and 2,206.28 ms optimized.

These elapsed values are actual measurements of this local prepared fixture, but Guest
startup dominates them and the sample is one fixed run per task. Optimized elapsed was
16.92 ms higher in aggregate; that difference is **not** a representative latency or
throughput claim. The defensible
mechanism result is the exact 25→23 physical-read reduction plus observed overlap/preissue
relations under the qualified contracts.

## Build-cache result

On `gpu31`, exact source `eb08ae9` established the full cold/warm pair:

- cold `refresh`: 342.800 s, layer miss, final miss;
- exact `auto`: 56.832 s, layer hit, final hit;
- same 52,638,669-byte Guest artifact and artifact SHA-256;
- 6.03× build-time ratio (83.4% elapsed reduction).

The final evaluated source `955b780` then reused that identity-bound layer but missed the
new exact-source final key in 152.224 s. Its next exact final hit took 44.254 s (3.44×),
with identical artifact bytes and
`sha256:80d69d364d0c5d58b5fd4972d04b2b8c9d6f395a3543fd80d9dd074923384c51`.

The private cache currently occupies 1.8 GiB and contains the configured maximum of two
layer keys and two final keys. This is build acceleration, not execution-result reuse.
Final artifact verification and real Guest gates still ran.

## Semantic decisions

- Executable AST-region reuse: `no_go` — 19 programs, 69 candidates, zero statically
  materializable regions and zero materializable cross-program overlaps.
- Semantic placement replacement: `no_go` — existing router 16 WASM / 3 native;
  semantic overlay 19 unknown; zero gains and 19 replacement regressions.
- Existing exact module-entry whole-Run qualification remains default-off. The placement
  and region consumers remain unadmitted.

## Overhead, resource and failure coverage

- The real Guest semantic-reuse gate observed 3 logical invocations, one physical compute,
  one in-flight waiter and one retained hit. Analysis took 3,616,543 µs; the concurrent
  batch took 3,556,484 µs; the retained lookup took 312 µs. These are one-run diagnostics,
  not population estimates.
- The sealed paired evidence uses 191,081 bytes. The Lab ships the exact same file rather
  than a second mutable projection.
- CPU time and peak RSS were **not captured** by the v0 paired evidence contract. No CPU or
  memory-efficiency claim is made. Guest artifact size, evidence bytes and bounded private
  cache storage are the reported resource facts.
- Focused tests cover cancellation isolation, leader failure/panic cleanup, corruption,
  expiry/eviction, exact privacy/authority/freshness partitioning, rejected writes,
  ambiguous completion and no post-effect fallback/replay.
- The all-off treatment executes every logical read physically. Each optimization remains
  independently representable and near-match candidates stay ordinary while recording a
  canonical rejection.

## Final claim boundary

Pysolate may claim that, for this frozen prepared workload and exact qualified contracts,
Host-owned explicit workflow/tool boundaries can make preissue, declared-independent
overlap, in-flight coalescing and freshness-safe retained reuse observable and can reduce
exact physical reads without terminal-output/effect divergence.

Pysolate must not claim:

- arbitrary Python semantic or executable AST-region reuse;
- semantic similarity or containment matching;
- universal or inferred sibling parallelism;
- hidden model thoughts or chain-of-thought observability;
- heap recovery, started-work migration, post-effect retry/replay or implicit task spawn;
- semantic placement improvement on the measured corpus;
- representative latency, CPU, memory or production throughput from this fixture.
