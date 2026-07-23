#!/usr/bin/env python3
"""Verify the provenance binding and reviewed WASM ABI of a guest artifact."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
from typing import Any

REQUIRED_EXPORTS = {
    "_initialize",
    "memory",
    "runtime_init",
    "runtime_prepare",
    "alloc",
    "dealloc",
    "execute",
}
ALLOWED_SUPPORT_EXPORTS = {
    "__data_end",
    "__heap_base",
    "__wasi_vfs_rt_init",
    "wasi_vfs_pack_fs",
}
ALLOWED_IMPORT_MODULES = {"wasi_snapshot_preview1", "agent_runtime_v1"}
REQUIRED_CUSTOM_IMPORTS = {("agent_runtime_v1", "host_call")}
ARTIFACT_FILENAMES = {
    "base": "agent-python-runtime.wasm",
    "numpy-core": "agent-python-runtime-numpy-core.wasm",
}
NUMPY_CORE_MODULES = (
    "numpy._core._multiarray_umath",
    "numpy.linalg._umath_linalg",
)


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify(
    artifact: pathlib.Path,
    manifest: dict[str, Any],
    extension_selection: pathlib.Path | None = None,
) -> None:
    if artifact.read_bytes()[:4] != b"\x00asm":
        raise ValueError("artifact does not have the WASM magic")
    if manifest.get("schema_version") != 2 or manifest.get("abi_version") != "v1":
        raise ValueError("unsupported manifest or ABI version")
    profile = manifest.get("artifact_profile")
    if profile not in ARTIFACT_FILENAMES:
        raise ValueError(f"unsupported artifact profile: {profile!r}")
    expected_filename = ARTIFACT_FILENAMES[profile]
    if artifact.name != expected_filename:
        raise ValueError("artifact filename does not match artifact profile")
    if manifest.get("target") != "wasm32-wasip1":
        raise ValueError("unexpected artifact target")

    artifact_record = manifest.get("artifact", {})
    if artifact_record.get("filename") != expected_filename:
        raise ValueError("manifest artifact filename does not match artifact profile")
    if artifact_record.get("size") != artifact.stat().st_size:
        raise ValueError("artifact size does not match manifest")
    actual_sha256 = sha256(artifact)
    if artifact_record.get("sha256") != actual_sha256:
        raise ValueError("artifact sha256 does not match manifest")

    extension_profile = manifest.get("extension_profile")
    if profile == "base":
        if extension_profile is not None or extension_selection is not None:
            raise ValueError("base artifact profile forbids extension profile")
    else:
        if not isinstance(extension_profile, dict):
            raise ValueError("numpy-core requires extension profile")
        if extension_selection is None or not extension_selection.is_file():
            raise ValueError("numpy-core requires extension profile selection file")
        if extension_profile.get("filename") != extension_selection.name:
            raise ValueError("extension profile filename does not match selection file")
        if extension_profile.get("manifest_sha256") != sha256(extension_selection):
            raise ValueError("extension profile digest does not match selection file")
        selection = json.loads(extension_selection.read_text())
        selected_modules = [
            row.get("module") for row in selection.get("modules", [])
        ]
        manifest_modules = extension_profile.get("modules", [])
        if (
            extension_profile.get("profile") != "core"
            or selection.get("schema_version") != 1
            or selection.get("package") != "numpy"
            or selection.get("profile") != "core"
            or selected_modules != list(NUMPY_CORE_MODULES)
            or manifest_modules != list(NUMPY_CORE_MODULES)
            or extension_profile.get("link_input_count") != len(selection.get("link_inputs", []))
        ):
            raise ValueError("numpy-core extension profile does not match exact core closure")

    wasm = manifest.get("wasm", {})
    exports = set(wasm.get("exports", []))
    if "_start" in exports:
        raise ValueError("command export _start is forbidden for the reactor")
    missing = sorted(REQUIRED_EXPORTS - exports)
    if missing:
        raise ValueError(f"missing exports: {', '.join(missing)}")
    unexpected = sorted(exports - REQUIRED_EXPORTS - ALLOWED_SUPPORT_EXPORTS)
    if unexpected:
        raise ValueError(f"unexpected exports: {', '.join(unexpected)}")

    imports = {
        (item.get("module"), item.get("name")) for item in wasm.get("imports", [])
    }
    import_modules = {module for module, _ in imports}
    forbidden_modules = sorted(import_modules - ALLOWED_IMPORT_MODULES)
    if forbidden_modules:
        raise ValueError(f"forbidden import module: {', '.join(forbidden_modules)}")
    custom_imports = {item for item in imports if item[0] != "wasi_snapshot_preview1"}
    forbidden_imports = sorted(custom_imports - REQUIRED_CUSTOM_IMPORTS)
    if forbidden_imports:
        raise ValueError(f"forbidden import: {forbidden_imports}")
    missing_imports = sorted(REQUIRED_CUSTOM_IMPORTS - custom_imports)
    if missing_imports:
        raise ValueError(f"missing required import: {missing_imports}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("artifact", type=pathlib.Path)
    parser.add_argument("manifest", type=pathlib.Path)
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_text())
    extension_profile = manifest.get("extension_profile")
    selection = None
    if isinstance(extension_profile, dict):
        filename = extension_profile.get("filename")
        if not isinstance(filename, str) or pathlib.Path(filename).name != filename:
            raise ValueError("invalid extension profile filename")
        selection = args.manifest.parent / filename
    verify(args.artifact, manifest, selection)
    print(json.dumps({"artifact": str(args.artifact), "sha256": sha256(args.artifact)}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
