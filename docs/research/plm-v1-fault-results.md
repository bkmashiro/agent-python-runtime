# PLM V1 temporal and fault results

Status: Gate 7 complete.

Evidence: [`plm-v1-fault-matrix.json`](../evidence/plm-v1-fault-matrix.json)

The checked-in aggregate is attributed to target `cfa8f4a0fe494a318619c7d05bd1030f92416873`
and exact Guest artifact
`sha256:f89f10170a6a2d4c412ed29db320b3b019b9efde09c135429e81845a3e9bda43`.

The gate passed 16 deterministic two-call seeds, 10 repeated race runs, 10 setup-failure teardown runs and six exact-Guest tests. The rows cover immutable, snapshot, versioned, leased, current and wall-clock modes; authority and quota changes; branch, loop, exception and failure paths; cancellation, late completion, foreign identity and disabled-pass fallback.

Visible results, logical-call counts and receipt order refine the sequential path. Provider starts, restart decisions and validator events remain separate economic evidence. Candidate and job dispositions remain Host-private and bounded. No response or provider body is stored in the aggregate.

The fault gate found and closed an uncertainty replay defect. A restart decision now cancels and settles the exact candidate before any canonical start. If the provider reports `PLMProviderOutcomeUncertain`, PLM materialises one logical `provider_outcome_uncertain` error even when temporal or provider proof fails; it does not invoke the provider again.

The controlled latency fixture remains negative; this result does not generalise beyond that fixture.
