# Observable workflow-boundary evaluation v0

Status: **Bounded research result; final claim frozen**
Date: 2026-08-15

## Frozen subjects

- Paired workload seed: `20260815`
- Harness source: `0f876d9ff9dac9ff3fa055c361b14b723e630b82`
- Guest source: `eb08ae94d576b1aaeceddb68c352e4dec78e6e84`
- Guest artifact: `sha256:4019fd9061c9bab7bab6ff7cb6ade5e15a457724d6e943e83423b07de8819c12`
- Workloads: 14 prepared explicit workflows; 4 positive classes, 8 one-dimension
  near-match negatives and 2 ordinary controls.
- Evidence: `docs/evidence/workflow-benchmark-evidence-v0.json` (191,081 bytes).

The baseline and optimized treatments used the same sealed manifest and shuffled order.
Model request/output intervals are deterministic local **replay fixtures**; Host tool and
Guest WASM intervals are measured. There was no paid or live model provider.

## Paired result

| Workload class | Tasks | Baseline physical | Optimized physical | Admitted | Rejected | Baseline elapsed | Optimized elapsed |
|---|---:|---:|---:|---:|---:|---:|---:|
| preissue | 1 | 1 | 1 | 1 | 0 | 2,197.63 ms | 2,186.88 ms |
| declared parallel | 1 | 2 | 2 | 1 | 0 | 2,200.18 ms | 2,198.52 ms |
| coalesced | 1 | 2 | 1 | 1 | 0 | 2,233.88 ms | 2,225.71 ms |
| retained reuse | 1 | 2 | 1 | 1 | 0 | 2,233.44 ms | 2,208.99 ms |
| near match | 8 | 16 | 16 | 0 | 8 | 17,687.79 ms | 17,678.41 ms |
| ordinary | 2 | 2 | 2 | 0 | 0 | 4,418.43 ms | 4,408.40 ms |
| **Total** | **14** | **25** | **23** | **4** | **8** | **30,971.35 ms** | **30,906.91 ms** |

The 14 sealed task reports contain 132 spans, 50 logical requests, 48 physical
executions and 12 optimization decisions. Every paired terminal output/effect oracle
matched; unclassifiable divergence was zero. The median task elapsed interval was
2,209.44 ms baseline and 2,204.63 ms optimized.

These elapsed values are actual measurements of this local prepared fixture, but Guest
startup dominates them and the sample is one fixed run per task. The 64.42 ms aggregate
difference is **not** a representative latency or throughput claim. The defensible
mechanism result is the exact 25→23 physical-read reduction plus observed overlap/preissue
relations under the qualified contracts.

## Build-cache result

On `gpu31`, exact source `eb08ae9` produced:

- cold `refresh`: 342.800 s, layer miss, final miss;
- exact `auto`: 56.832 s, layer hit, final hit;
- same 52,638,669-byte Guest artifact and artifact SHA-256;
- 6.03× build-time ratio (83.4% elapsed reduction).

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
