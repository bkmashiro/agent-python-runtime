# Benchmarks

Checked on 2026-10-13 against GitHub: main runtime [`6f66119d`](https://github.com/bkmashiro/agent-python-runtime/tree/6f66119d7b39ff3abf36ef90858b729bee32fe46), experiment repository [`92cb647c`](https://github.com/bkmashiro/pysolate-spine/tree/92cb647c774f04c75f3b5949ebbaab54bcb0e677). **Some completed experiments remain local.** Spine is a private repository; its links require access.

## Main-runtime measurements

These reports and their listed non-sensitive measurements are in this repository. Follow each report's command and artifact requirements.

| Question | Report / runner | Recorded data |
|---|---|---|
| Fresh, Prepared/COW, compilation cache, admission and HTTP service | [Performance results](performance-results.md), [`pysolate-bench`](../cmd/pysolate-bench/), [`pysolate-service-bench`](../cmd/pysolate-service-bench/) | [Performance data](performance-data/) |
| Warm-path CPU, allocation and startup costs | [Dissection](performance-dissection.md), [COW data image](cow-data-image.md), [import cache](scientific-bytecode-cache.md) | [Dissection](performance-data/dissection-292690/), [COW](performance-data/cow-data-image-292705/), [imports](performance-data/import-cache-292739/) |
| Scheduling, live I/O waits and CPU/I/O mixes | [Scheduling evaluation](scheduling-evaluation.md), [semantic phases](semantic-scheduling-study.md), [`pysolate-phase-bench`](../cmd/pysolate-phase-bench/) | [Matrix](performance-data/scheduling-matrix/), [phases](performance-data/semantic-phases/), [live waits](performance-data/live-wait/), [mixed load](performance-data/mixed-load-292806/) |
| Early-read benefit, contention and frontend overhead | [Contention](early-read-contention.md), [validation](early-read-validation.md), [frontend cost](early-read-frontend-cost.md) | [Contention](performance-data/early-read-contention/); further evidence linked in each report |
| Common Python/tool tasks and replay | [Workload pack](workload-pack.md), [offline replay](offline-replay.md); `make workloads`, `make replay-check` | [Common use cases](performance-data/common-usecases/), [seeded replay](performance-data/seeded-replay/) |
| Model/tool behavior and dependent calls | [Agent evaluation](agent-evaluation.md), [dependency evaluation](dependency-evaluation.md), [partial effects](partial-effects-pilot.md) | [Agent evaluation](performance-data/agent-evaluation-v1/), [dependencies](performance-data/dependency-evaluation-v2/), [partial effects](performance-data/partial-effects-v1/) |

Guest builds follow [artifact profiles](artifact-profiles.md). Compiled artifacts and full private model recordings are not all stored in Git. Checked-in summaries do not imply that private prompts or provider responses are downloadable.

## Experimental-runtime measurements

These suites use the archived runtime, patched wazero and Guest specified by each experiment. They do not measure the main runtime's default configuration.

| Suite | Code and reports on GitHub | Data and build inputs |
|---|---|---|
| Native prefork vs Pysolate: idle/JSON/stream memory slopes, throughput, CPU/request and open-loop tails | [Unified comparison][compare]; `make bench-compare` in the Spine checkout | [288-cell Linux campaign][comparison-data]: raw archive, plan, manifest, summaries and report |
| COW, engine metadata, INIT/EXEC allocation and pymalloc | [Memory package][memory]; `make bench-full` for its full memory campaign | Source/Guest packages, patches and archived measurements under that directory |
| Request residency, lifecycle/allocator and Host ABI | [Runtime-cost index][cost], [report][cost-report] | Probe source, `results/controller-linux.tar.gz`, summaries and lane-specific evidence |
| Native I/O and request scheduling | [I/O comparison][io], [native comparison][native] | Workload/campaign source and evidence referenced by those reports |
| Frozen model-generated program reuse | [Offline evaluation][reuse]; `go run . offline` in that directory | Four generated programs, fixtures, original evaluation rows and follow-up diagnostics; uses the Spine Guest prepared by `tools/setup-guest.py` |

`make bench-compare` covers the matched native/Pysolate comparison. It does not rerun every historical diagnostic or model evaluation. Offline analyzers can recompute archived summaries without launching a Linux measurement campaign; new COW/PSS measurements require Linux.

## Completed experiments still local

These were checked by commit availability and file content, not just branch names. They have no GitHub evidence links yet and are not included in the README performance table.

| Study | Repository / local ref | Commit |
|---|---|---|
| Checked/unchecked worker retirement | Spine: `exp/runtime-cost-external-reap` | `ab3a27687f5d7fe12f7f1ff44e5a256696527d27` |
| Cache-hit preparation and paired normal/fault recovery | Spine: `exp/runtime-cost-cache-recovery` | `7198665517549862a663ba3bffb12fcf0bec5b18` |
| Named Guest hotspot attribution | Spine: `exp/runtime-cost-guest-symbols` | `4861bbd24aa5a6ad483ef12244e87b1901a85fcd` |
| JIT machine-code analysis | Spine: `exp/runtime-cost-machinecode` | `41180210a868902e7defe0d6d7c4fa6934831ce4` |
| Frozen standard library / minimalfs | Main runtime: `exp/frozen-minimalfs` | `4402afbd03d8d61a40e4617cce47c31754751daa` |
| Rebuilt Guest COW growth and compile-seed warmup | Main runtime: `exp/guest-rebuild-attribution` | `258519b3a24aa1064400c2695d4163f411426090` |

The older ABI, lifecycle and residency branches also lack matching remote branches, but their probes have already been integrated into Spine main. They are listed in the published suites above.

## Comparison scope

Use matched rows within a report. Different campaigns use different Guest builds, timers, hosts and request lifecycles. Native prefork runs trusted fixtures without an equivalent adversarial sandbox. Reported PSS slopes exclude the fixed intercept; page-table costs are separate where stated. Failed and incomplete campaigns remain failures.

[compare]: https://github.com/bkmashiro/pysolate-spine/tree/92cb647c774f04c75f3b5949ebbaab54bcb0e677/experiments/runtime-cost/compare
[comparison-data]: https://github.com/bkmashiro/pysolate-spine/tree/1908514a55f524289146bad5c9b3e7a24ef5c688/experiments/runtime-cost/compare/evidence/linux-arm64-20261012
[memory]: https://github.com/bkmashiro/pysolate-spine/tree/92cb647c774f04c75f3b5949ebbaab54bcb0e677/experiments/memory
[cost]: https://github.com/bkmashiro/pysolate-spine/blob/92cb647c774f04c75f3b5949ebbaab54bcb0e677/experiments/runtime-cost/README.md
[cost-report]: https://github.com/bkmashiro/pysolate-spine/blob/92cb647c774f04c75f3b5949ebbaab54bcb0e677/experiments/runtime-cost/REPORT.md
[io]: https://github.com/bkmashiro/pysolate-spine/blob/92cb647c774f04c75f3b5949ebbaab54bcb0e677/experiments/runtime-cost/IO-COMPARISON.md
[native]: https://github.com/bkmashiro/pysolate-spine/blob/92cb647c774f04c75f3b5949ebbaab54bcb0e677/experiments/runtime-cost/NATIVE-COMPARISON.md
[reuse]: https://github.com/bkmashiro/pysolate-spine/tree/92cb647c774f04c75f3b5949ebbaab54bcb0e677/experiments/reusable-code
