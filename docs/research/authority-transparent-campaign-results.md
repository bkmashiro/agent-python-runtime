# Authority-transparent campaign results

## Conclusion

Across 5 paired repetitions of the fixed 20-program campaign, qualified execution used a median of 17 physical executions versus 19 for baseline: a paired reduction of 2 executions (10.5%). Median wall time was 30.62 s versus 42.99 s; this descriptive small-sample result is not a production throughput claim.

![Paired physical execution counts](../figures/authority-transparent-campaign.svg)

| Treatment | Physical executions, median [min, max] | Wall time, median [min, max] | Process CPU, median [min, max] |
|---|---:|---:|---:|
| Baseline | 19 [19, 19] | 42.99 s [34.14, 47.31] | 112.14 s [91.91, 121.84] |
| Qualified | 17 [17, 17] | 30.62 s [29.14, 40.26] | 91.18 s [88.12, 114.89] |

## Provenance

- Campaign source: eb45d3af224be281ca105d200b5547c00d4cb4ab
- Guest artifact: sha256:923e58c534b0534ac1ff5246ee2771fd3490cd304bc39bed340dc3fe883377ea
- Guest artifact source: eb45d3af224be281ca105d200b5547c00d4cb4ab
- Manifest: sha256:01b1598760962311be630a2bbc5ac0e259ac211ed6bcd4b9e29933201a83cfc7
- Host: linux/amd64; go1.25.0; Linux 6.17.0-35-generic #35~24.04.1-Ubuntu SMP PREEMPT_DYNAMIC Tue May 26 19:30:42 UTC 2
- Evidence strength: paired repetitions, full min–max shown; no confidence interval inferred.

## Claim boundary

**Valid:** For this fixed 20-program campaign on one recorded host, exact qualified sharing reduced physical executions while preserving every registered oracle and authority rejection.

**Invalid inference:** Do not generalize these five paired repetitions to arbitrary workloads, hosts, schedulers, or steady-state production throughput.

## Qualified arrival-to-terminal flow

![Qualified repetition 0 arrival-to-terminal flow](../figures/authority-transparent-campaign-flow.svg)

## Fixed 20-case contract

| ID | Family | Release | Typed operation | Actual admission | Actual terminal |
|---|---|---:|---|---|---|
| P01 | authority bifurcation | +0 ms | execute python | admitted | complete |
| P02 | authority bifurcation | +4 ms | consume result | admitted | complete |
| P03 | authority bifurcation | +4 ms | consume result | admitted | complete |
| P04 | authority bifurcation | +6 ms | consume result | admitted | cancelled |
| P05 | exact sharing | +10 ms | exact request | admitted | complete |
| P06 | exact sharing | +10 ms | exact request | admitted | complete |
| P07 | exact sharing | +11 ms | exact request | admitted | complete |
| P08 | exact sharing | +12 ms | exact request | admitted | complete |
| P09 | exact sharing | +13 ms | exact request | admitted | complete |
| P10 | root verification | +18 ms | verify workspace | admitted | complete |
| P11 | root verification | +19 ms | verify workspace | admitted | complete |
| P12 | root verification | +20 ms | verify workspace | admitted | complete |
| P13 | authority resume | +25 ms | start workflow | admitted | complete |
| P14 | authority resume | +26 ms | resume workflow | admitted | complete |
| P15 | authority resume | +27 ms | resume workflow | admitted | complete |
| P16 | authority resume | +28 ms | resume workflow | authority_expired | rejected |
| P17 | delegation attenuation | +32 ms | delegate child | admitted | complete |
| P18 | delegation attenuation | +33 ms | delegate child | authority_widening | rejected |
| P19 | delegation attenuation | +34 ms | delegate child | delegation_budget | rejected |
| P20 | delegation attenuation | +35 ms | delegate child | parent_terminal | cancelled |

## Observed logical-to-physical identity

| Logical cases | Qualified physical identity | Observed boundary |
|---|---|---|
| P05 / P06 | campaign-exact-2 / campaign-exact-2 | exact request identity reused one physical Guest result |
| P10 / P11 | campaign-verifier-19 / campaign-verifier-19 | exact sealed-root verifier identity reused one verifier execution |
| P18 / P19 / P20 | none | rejected or cancelled before physical execution |
