# ADR 0003: Artifact provenance

- Status: Accepted for V1 implementation
- Date: 2026-07-22

## Context

A large Python WASI artifact combines a compiler toolchain, CPython, packed standard library, optional NumPy code/static libraries, build helpers, and the project guest bootstrap. A successful CI upload or recognizable filename does not identify those inputs.

## Decision

- Build the guest in this repository's GitHub Actions from `guest/build/sources.lock.json`.
- Prefer official source distributions and independently written build scripts.
- Require immutable version identity, HTTPS URL, SHA-256, license, and role for each downloaded input.
- Reject mutable `latest` URLs.
- Pin GitHub Actions by full commit SHA with tag comments.
- Emit artifact, manifest, SHA256SUMS, notices, SBOM, and raw test report together.
- Pass the exact artifact to downstream Linux Host E2E jobs through Actions artifact transfer.
- Do not commit the canonical large `.wasm` to Git.
- Do not create a GitHub Release automatically.

## Existing references

`webassembly-language-runtimes` is an Apache-2.0 artifact-build reference, but its current reactor guest contains Shimmy/Lambda Feedback semantics and is not copied.

`bkmashiro/wasi-wheels` currently provides useful build evidence and assets, but its repository has no root LICENSE/NOTICE despite a README MIT statement. Its scripts are not copied unless licensing/provenance is resolved. Mutable `latest` assets are never trusted without a pinned digest and are not the preferred clean-build source.

## Manifest minima

The manifest records repository/run identity, ABI version, source/toolchain locks, compile/link flags, exact imports/exports, bundled packages, limitations, byte size, artifact SHA-256, and smoke outcomes.

## Reproducibility

A manual workflow performs two cold isolated builds. Binary identity is the target. If nondeterminism remains, the workflow must locate and document the exact fields before any normalization; unexplained differences fail the release-strength gate.

## Consequences

Builds may be slower and NumPy may be delayed behind a provenance blocker. That is preferred to shipping a binary whose source and redistribution obligations cannot be reconstructed.
