# Guest supply-chain evidence

Each Core Guest build emits two deterministic supply-chain files beside the Wasm artifact:

- `sbom.spdx.json`: SPDX 2.3 JSON;
- `THIRD_PARTY_NOTICES.md`: a generated component/relation summary.

## Inputs and identity

[`guest/build/write-supply-chain.py`](../guest/build/write-supply-chain.py) binds both outputs to:

- the artifact SHA-256, size, filename, target, and repository commit from `manifest.json`;
- the canonical `SOURCE_DATE_EPOCH`;
- every immutable entry in `guest/build/sources.lock.json`;
- each source's explicit `artifact_relation`: `packaged`, `linked`, or `build-only`;
- a sorted SHA-256 inventory of actual files staged under `/usr/lib/python3.14`.

Build-generated `__pycache__`, `.pyc`, and `.pyo` files are excluded from the staged VFS. The SBOM therefore describes source files actually supplied to wasi-vfs, not an aspirational package list. NumPy is absent until an actual locked package is bundled. Host-side wazero is not represented as a Guest component.

The locked wasi-vfs static archive is accompanied by the exact upstream `linked_storage.c` at commit `0f4db4b…`. The build applies one fail-closed local patch that zero-initializes four allocated structs before Wizer snapshots linear memory, recompiles only that C object with the pinned wasi-sdk, and replaces the unique old archive member. Both the release archive and patched source appear as linked SBOM inputs.

## Validation

The producer performs three checks before writing `SHA256SUMS`:

1. rebuild the canonical SBOM/notices from the same artifact, manifest, lock, and VFS and require exact equality;
2. fetch the official SPDX 2.3 JSON schema from immutable commit `aadf3b0b8dbbabdb4d880b0fc714255fea436ff7` through the source lock and verify its SHA-256;
3. validate `sbom.spdx.json` with the repository's go.sum-locked JSON Schema engine.

The artifact consumer repeats bundle identity/source-lock/notice validation without trusting producer staging, refetches the hash-locked official schema, and repeats schema validation. `SHA256SUMS` covers the Wasm, manifest, SPDX document, and generated notices.

The generated notice separates packaged/linked components from build-only tools. It records immutable source URLs, versions, archive digests, and license identifiers. License identifiers are provenance metadata, not legal advice or a substitute for upstream license texts.
