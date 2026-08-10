# Guest supply-chain evidence

Each Core Guest build emits two deterministic supply-chain files beside the Wasm artifact:

- `sbom.spdx.json`: SPDX 2.3 JSON;
- `THIRD_PARTY_NOTICES.md`: a generated component/relation summary.

## Inputs and identity

[`guest/build/write-supply-chain.py`](../guest/build/write-supply-chain.py) binds both outputs to:

- the artifact SHA-256, size, filename, target, and repository commit from `manifest.json`;
- the canonical `SOURCE_DATE_EPOCH`;
- every immutable entry in the selected profile lock: `guest/build/sources.lock.json` for `base` or `guest/build/sources.numpy-core.lock.json` for `numpy-core`;
- each source's explicit `artifact_relation`: `packaged`, `linked`, or `build-only`;
- a sorted SHA-256 inventory of actual files staged under `/usr/lib/python3.14`.

Build-generated `__pycache__`, `.pyc`, and `.pyo` files are excluded from the staged VFS. The SBOM therefore describes source files actually supplied to wasi-vfs, not an aspirational package list. NumPy/Cython are absent from `base`; the explicit `numpy-core` lock and artifact record NumPy as packaged and Cython as build-only. Host-side wazero is not represented as a Guest component. Manifest package versions are derived from exactly one corresponding selected-lock source row rather than duplicated constants.

The locked wasi-vfs prebuilt static archive is accompanied by the exact upstream `linked_storage.c` at commit `0f4db4b…`. The build applies one fail-closed local patch that zero-initializes four allocated structs before Wizer snapshots linear memory, recompiles only that C object with the pinned wasi-sdk, and replaces the unique old archive member. Both the prebuilt archive and patched source appear as linked SBOM inputs.

The locked wasi-vfs static archive is accompanied by the exact upstream `linked_storage.c` at commit `0f4db4b…`. The build applies one fail-closed local patch that zero-initializes four allocated structs before Wizer snapshots linear memory, recompiles only that C object with the pinned wasi-sdk, and replaces the unique old archive member. Both the release archive and patched source appear as linked SBOM inputs.

## Validation

The producer performs three checks before writing `SHA256SUMS`:

1. rebuild the canonical SBOM/notices from the same artifact, manifest, lock, and VFS and require exact equality;
2. fetch the official SPDX 2.3 JSON schema from immutable commit `aadf3b0b8dbbabdb4d880b0fc714255fea436ff7` through the source lock and verify its SHA-256;
3. validate `sbom.spdx.json` with the repository's go.sum-locked JSON Schema engine.

The artifact consumer repeats bundle identity/source-lock/notice validation without trusting producer staging, refetches the hash-locked official schema, and repeats schema validation. `SHA256SUMS` covers the Wasm, manifest, SPDX document, and generated notices; `numpy-core` additionally covers the exact extension-selection sidecar.

Runtime admission uses one canonical `runtime.VerifyDistributionArtifact` verifier shared by the local CLI and the pinned Hermes loader. It rejects duplicate/unknown identity fields and trailing manifest JSON, validates the exact profile/package-set contract (`base` versus `numpy-core`), canonical filename, size and artifact SHA-256, build/target identity, NumPy extension-profile identity, and computes the manifest SHA-256. An enabled `ExecutionProfile` retains both digests and exposes them through validated Runner properties before Guest work. This is runtime identity binding, not a replacement for downloaded-bundle SBOM/source-lock verification and not yet a per-import inventory.

The generated notice separates packaged/linked components from build-only tools. It records immutable source URLs, versions, archive digests, and license identifiers. License identifiers are provenance metadata, not legal advice or a substitute for upstream license texts.

## Consumer evidence index

After downloaded-bundle verification, exact-artifact E2E, and both canonical production-safe benchmark commands pass for `base`, the artifact workflow writes `evidence-index.json`. Its strict v1 schema binds the workflow run/commit to the artifact, manifest, SPDX document, notices, and completed test classes. Its limitations explicitly preserve the unresolved exact-reproducibility failure and synthetic-benchmark scope.

`numpy-core` deliberately does not write that production-safe index. It uploads separate fresh and prepared `profile-candidate` evidence after downloaded verification and real E2E; class/profile validation prevents either candidate document from being mislabeled as `production-safe`.

The base index is uploaded as a separate consumer-side artifact. It is intentionally not inserted into the Guest bundle: the workflow run ID is evidence about a particular CI execution and would itself make two otherwise identical producer bundles differ.
