# Semantic pre-execution placement census v0

Status: **Pre-registered comparison; Runtime consumer remains absent**
Date: 2026-08-15

## Question

Can the verified Guest semantic overlay choose a more precise pre-execution backend than the current Host router without disagreeing with or regressing an already decisive Host choice?

This is a placement-only question. The overlay does not execute code, rewrite Python, create tasks, migrate started work, or grant capability authority.

## Inputs and identity

`pysolate.semantic-placement-census.v0` consumes only `semantic.VerifiedAnalysis` handles from the same target-Guest run as the effectgraph census. The sealed report binds:

- exact artifact source commit and artifact SHA-256;
- analyzer, execution-profile, import-closure and capability-Plan SHA-256;
- exact corpus SHA-256;
- each prepared program ID and the current Host router's baseline placement.

No Python source or argument/result body is emitted.

## Pre-registered decision rule

For each program, compare the baseline Host placement with `semantic.RequiredBackend`:

- **safe precision gain**: baseline is unknown and semantic placement is decisive;
- **agreement**: both are decisive and equal;
- **disagreement**: both are decisive and different;
- **replacement regression**: baseline is decisive and semantic placement is unknown.

Minimal integration is admitted only when there is at least one safe precision gain, zero disagreements, and zero replacement regressions. Otherwise the decision is `no_go`, `current_router_retained=true`, and `consumer_admitted=false`.

## Current contract expectation

The existing semantic overlay deliberately has no canonical backend requirement. `semantic.RequiredBackend` therefore returns `unknown` with `backend_contract_missing`. A representative corpus where the current router is decisive should produce zero safe gains and replacement regressions, preserving the current router.

This is a useful negative result: semantic occurrence and legality evidence can support explanation and rejection without becoming a second placement authority. A future placement proposal requires a new versioned Host-owned backend contract, a new pre-registration, and fresh target-Guest evidence.

## Evidence generation

`effectgraph-census` now writes the placement report in the same atomic generation as the corpus, effect report and region report. `pysolate.effectgraph-census-bundle.v1` binds all four file SHA-256 values and is replaced last. The release check verifies the whole generation without rerunning a Guest.
