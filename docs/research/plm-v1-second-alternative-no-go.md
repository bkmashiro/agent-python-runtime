# PLM V1 second-alternative no-go

Status: Gate 6 decision record.

## Result

Inline final-Guest lowering preserves the PLM mechanism but remains slower on the controlled V1 fixture:

| profile | sequential median | PLM median | delta |
|---|---:|---:|---:|
| cold end to end | 4.018 s | 5.305 s | +32.01% |
| Engine precompiled | 2.690 s | 3.989 s | +48.27% |

The result is limited to five samples per arm, the exact artifact and source recorded in `docs/evidence/plm-v1-economics.json`.

The PLM medians attribute about 0.51 s to exact-Guest AST lowering and 0.79 s to validating and installing the derived tree. The provider itself is about 0.077 s. Median allocation increases by about 0.64 MiB in each profile. Every PLM sample records one physical start, one logical linearization, one materialization and no canonical restart.

## Bounded second-alternative assessment

### Prepared external analyzer

A prepared analyzer can reduce analyzer initialization, but it cannot remove the measured AST lowering or final-Guest derived-tree installation. It also creates one of two disallowed lifecycles:

1. a fresh or private-copy analyzer Guest per Run, which restores a second physical Guest and its transport; or
2. a retained analyzer interpreter shared across Runs, which violates the no-retained-interpreter and no-cross-Run-mutable-analyzer mechanism gate.

The earlier predecessor measurement already showed that a cold external analyzer was substantially worse on its fixture. Reintroducing it cannot address the current 1.30 s lowering-plus-selection cost.

### Host-side rewrite

Moving Python AST rewriting to Go would remove exact target-Guest AST authority and create a second compiler implementation. This is outside the accepted architecture.

### Cross-Run patch cache

An immutable cache could help repeated identical source and projection manifests, but it does not improve first-source cold latency. It also changes the question from PLM execution economics to cache-hit economics. It is not accepted as evidence for this gate.

### Source-specific prepared authority Guest

Preinstalling a source-specific tree into a Guest carrying Broker or workspace authority would make physical state survive before Run ownership. Pysolate intentionally rejects public pre-provisioning of such Guests.

## Decision

No second production runtime is implemented. The remaining safe ideas either do not address the measured cold cost or violate the one-Run ownership and one-Agent-execution gates.

PLM V1 remains default-off research infrastructure. Its correctness result is retained independently from the negative economics result. Future work may reduce the pure AST lowering and derived-tree installation cost inside the same final Guest, but it must clear the same controlled protocol before changing the default.
