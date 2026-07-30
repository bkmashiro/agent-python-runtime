# Production policy compiler

`runtime/scheduler.CompileProductionPolicy` reduces the public scheduler policy surface to three bounded knobs:

```go
policy := scheduler.ProductionPolicy{
    MaxMemoryBytes: 16 << 30,
    MaxCPU:         12,
    Greed:          50,
}
effective, err := scheduler.CompileProductionPolicy(policy)
```

- `MaxMemoryBytes` is the deployment hard-memory limit, from 64 MiB through 1 EiB.
- `MaxCPU` is an integer CPU-core limit, from 1 through 1024.
- `Greed` is a dimensionless integer from 0 (conservative) through 100 (most aggressive).

The compiler does not inspect the machine, invent either hard limit, or enable production overcommit. The deployment layer must apply `CPUQuotaMicros`/`CPUPeriodMicros` and the memory cgroup limit. `MaxActive` is a Host concurrency bound, not CPU enforcement. Greed never raises the compiled hard-memory or CPU limit.

## Versioned derivation

`production-policy-v1` derives:

| Effective setting | Greed 0 | Greed 50 | Greed 100 |
|---|---:|---:|---:|
| target / high / critical memory | 80% / 88% / 95% | 85% / 91% / 96.5% | 90% / 94% / 98% |
| maximum active multiplier over `MaxCPU` | 1× | 2.5× | 4× |
| initial reservation quantile | p100 | p95 | p90 |
| target eviction budget | 0 PPM | 25,000 PPM | 50,000 PPM |
| control interval | 200 ms | 125 ms | 50 ms |
| default retry delay | 250 ms | 150 ms | 50 ms |
| speculative eviction bound | 0 | 1 | 2 |

All intermediate values are monotonic integer fixed-point derivations. Maximum active attempts remain capped at 4096. Scheduler task, attempt, profile, and sample records retain the existing hard library bounds.

An unknown workload reserves approximately `target memory / max active`, with a 1 MiB floor. The per-attempt and retry margin is 0.1% of hard memory, clamped to 1–64 MiB. Profile adaptation remains bounded to p90–p100 and becomes more conservative immediately on OOM, hard/critical/high pressure, PSI excess, or eviction-budget excess.

## Telemetry

`effective.Telemetry()` returns deterministic JSON-safe `EffectivePolicyTelemetry`. It includes the three source knobs, byte watermarks, CPU quota values, maximum active attempts, reservation policy, eviction budget, retry/poll timing, PSI limits, and record bounds. It excludes the scheduler clock function and live controller state.

The effective telemetry must be recorded alongside deployment and experiment evidence. It explains a compiled policy; it is not proof that the deployment layer successfully applied its cgroup limits.

## Product boundary

This compiler is a library configuration surface. It does not add an `apyrun` production-overcommit CLI flag, wire the executor to a public service, or claim production readiness. Those steps still require an explicit multi-request product contract, cgroup application/read-back, effect-safe eviction wiring, and fail-closed startup verification.
