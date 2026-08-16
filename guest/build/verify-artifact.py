#!/usr/bin/env python3
"""Verify the provenance binding and reviewed WASM ABI of a guest artifact."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import pathlib
from typing import Any

REQUIRED_EXPORTS = {
    "_initialize",
    "memory",
    "runtime_init",
    "runtime_validate_source",
    "runtime_analyze_source",
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
    "attrs-770": "agent-python-runtime-attrs-770.wasm",
}
MAX_JSON_BYTES = 1 << 20
MANIFEST_FIELDS = {
    "schema_version", "abi_version", "artifact_profile", "target", "artifact", "build", "sources", "wasm",
    "packages", "extension_profile", "python_import_inventory", "python_import_qualification", "limitations",
}
INVENTORY_FIELDS = {"schema_version", "artifact_profile", "probe", "implementation", "python_version", "discoverable_roots", "failures"}
QUALIFICATION_FIELDS = {"schema_version", "artifact_profile", "probe", "implementation", "python_version", "qualified_roots", "results"}
ARTIFACT_FIELDS = {"filename", "size", "sha256"}
BUILD_FIELDS = {"repository_commit", "source_date_epoch", "compiler_target", "execution_model"}
PACKAGE_FIELDS = {"name", "version", "status"}
EMBEDDED_INVENTORY_FIELDS = (INVENTORY_FIELDS - {"artifact_profile"}) | {"filename", "sha256"}
INVENTORY_FAILURE_FIELDS = {"name", "error"}
EMBEDDED_QUALIFICATION_FIELDS = (QUALIFICATION_FIELDS - {"artifact_profile"}) | {"filename", "sha256"}
QUALIFICATION_RESULT_FIELDS = {"name", "operation", "status", "error"}


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def _reject_json_constant(value: str):
    raise ValueError(f"non-standard JSON constant: {value}")


def require_exact_object(value: Any, fields: set[str], label: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != fields:
        raise ValueError(f"{label} fields are invalid")
    return value


def require_exact_rows(value: Any, fields: set[str], label: str) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        raise ValueError(f"{label} must be a list")
    return [require_exact_object(row, fields, label) for row in value]


def load_json_strict(path: pathlib.Path, allowed_fields: set[str] | None = None) -> dict[str, Any]:
    encoded = path.read_bytes()
    if not encoded or len(encoded) > MAX_JSON_BYTES:
        raise ValueError("JSON evidence size is invalid")
    value = json.loads(encoded, object_pairs_hook=_unique_object, parse_constant=_reject_json_constant)
    if not isinstance(value, dict):
        raise ValueError("JSON evidence must be an object")
    if allowed_fields is not None and not set(value).issubset(allowed_fields):
        raise ValueError("JSON evidence contains unknown fields")
    return value


def load_import_inventory_module():
    path = pathlib.Path(__file__).with_name("import_inventory.py")
    spec = importlib.util.spec_from_file_location("artifact_import_inventory", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load import inventory validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_import_qualification_module():
    path = pathlib.Path(__file__).with_name("import_qualification.py")
    spec = importlib.util.spec_from_file_location("artifact_import_qualification", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load import qualification validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_extension_profile_module():
    path = pathlib.Path(__file__).with_name("extension_profile.py")
    spec = importlib.util.spec_from_file_location("artifact_extension_profile", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load extension profile validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


IMPORT_INVENTORY = load_import_inventory_module()
IMPORT_QUALIFICATION = load_import_qualification_module()
EXTENSION_PROFILE = load_extension_profile_module()
ATTRS_LOCK = EXTENSION_PROFILE.load_lock(EXTENSION_PROFILE.PROFILE_LOCK)
BASE_SOURCE_LOCK = EXTENSION_PROFILE.strict_json_loads((pathlib.Path(__file__).with_name("sources.lock.json")).read_text())
ATTRS_SOURCES = EXTENSION_PROFILE.merge_source_lock(BASE_SOURCE_LOCK, ATTRS_LOCK)["sources"]


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
    import_inventory: pathlib.Path | None = None,
    import_qualification: pathlib.Path | None = None,
) -> None:
    if not isinstance(manifest, dict) or not set(manifest).issubset(MANIFEST_FIELDS):
        raise ValueError("manifest fields are invalid")
    if artifact.read_bytes()[:4] != b"\x00asm":
        raise ValueError("artifact does not have the WASM magic")
    schema_version = manifest.get("schema_version")
    if schema_version not in {2, 3, 4} or manifest.get("abi_version") != "v1":
        raise ValueError("unsupported manifest or ABI version")
    profile = manifest.get("artifact_profile")
    if profile not in ARTIFACT_FILENAMES:
        raise ValueError(f"unsupported artifact profile: {profile!r}")
    expected_filename = ARTIFACT_FILENAMES[profile]
    if artifact.name != expected_filename:
        raise ValueError("artifact filename does not match artifact profile")
    if manifest.get("target") != "wasm32-wasip1":
        raise ValueError("unexpected artifact target")

    artifact_record = require_exact_object(manifest.get("artifact"), ARTIFACT_FIELDS, "manifest artifact")
    if artifact_record.get("filename") != expected_filename:
        raise ValueError("manifest artifact filename does not match artifact profile")
    if artifact_record.get("size") != artifact.stat().st_size:
        raise ValueError("artifact size does not match manifest")
    actual_sha256 = sha256(artifact)
    if artifact_record.get("sha256") != actual_sha256:
        raise ValueError("artifact sha256 does not match manifest")
    build_record = require_exact_object(manifest.get("build"), BUILD_FIELDS, "manifest build")
    if (
        not isinstance(build_record["repository_commit"], str)
        or len(build_record["repository_commit"]) != 40
        or not build_record["source_date_epoch"]
        or build_record["compiler_target"] != "wasm32-wasip1"
        or build_record["execution_model"] != "reactor"
    ):
        raise ValueError("manifest build identity is invalid")

    inventory_record = manifest.get("python_import_inventory")
    qualification_record = manifest.get("python_import_qualification")
    if schema_version == 2:
        if inventory_record is not None or import_inventory is not None or qualification_record is not None or import_qualification is not None:
            raise ValueError("legacy manifest must not contain Python import evidence")
    else:
        if not isinstance(inventory_record, dict) or import_inventory is None or not import_inventory.is_file():
            raise ValueError("schema-v3 manifest requires import inventory")
        require_exact_object(inventory_record, EMBEDDED_INVENTORY_FIELDS, "manifest import inventory")
        require_exact_rows(inventory_record.get("failures"), INVENTORY_FAILURE_FIELDS, "manifest import inventory failure")
        if inventory_record.get("filename") != import_inventory.name or inventory_record.get("sha256") != sha256(import_inventory):
            raise ValueError("import inventory sidecar identity mismatch")
        strict_inventory = load_json_strict(import_inventory, INVENTORY_FIELDS)
        inventory = IMPORT_INVENTORY.load_inventory(import_inventory, profile)
        if inventory != strict_inventory:
            raise ValueError("import inventory parser mismatch")
        embedded = {key: value for key, value in inventory.items() if key != "artifact_profile"}
        embedded["filename"] = import_inventory.name
        embedded["sha256"] = sha256(import_inventory)
        if inventory_record != embedded:
            raise ValueError("manifest import inventory does not match sidecar")
        if schema_version == 3:
            if qualification_record is not None or import_qualification is not None:
                raise ValueError("schema-v3 manifest must not contain import qualification")
        else:
            if not isinstance(qualification_record, dict) or import_qualification is None or not import_qualification.is_file():
                raise ValueError("schema-v4 manifest requires import qualification")
            require_exact_object(qualification_record, EMBEDDED_QUALIFICATION_FIELDS, "manifest import qualification")
            require_exact_rows(qualification_record.get("results"), QUALIFICATION_RESULT_FIELDS, "manifest import qualification result")
            if qualification_record.get("filename") != import_qualification.name or qualification_record.get("sha256") != sha256(import_qualification):
                raise ValueError("import qualification sidecar identity mismatch")
            strict_qualification = load_json_strict(import_qualification, QUALIFICATION_FIELDS)
            qualification = IMPORT_QUALIFICATION.load_qualification(import_qualification, profile)
            if qualification != strict_qualification:
                raise ValueError("import qualification parser mismatch")
            if (
                qualification["implementation"] != inventory["implementation"]
                or qualification["python_version"] != inventory["python_version"]
                or not set(qualification["qualified_roots"]).issubset(inventory["discoverable_roots"])
            ):
                raise ValueError("import qualification does not match inventory")
            embedded_qualification = {key: value for key, value in qualification.items() if key != "artifact_profile"}
            embedded_qualification["filename"] = import_qualification.name
            embedded_qualification["sha256"] = sha256(import_qualification)
            if qualification_record != embedded_qualification:
                raise ValueError("manifest import qualification does not match sidecar")

    extension_profile = manifest.get("extension_profile")
    if profile == "base":
        if extension_profile is not None or extension_selection is not None:
            raise ValueError("base artifact profile forbids extension profile")
    elif profile == "attrs-770":
        if schema_version != 4:
            raise ValueError("attrs-770 artifact profile requires schema v4")
        extension_profile = EXTENSION_PROFILE.validate_selection(extension_profile)
        if extension_selection is not None:
            sidecar = EXTENSION_PROFILE.load_selection(extension_selection)
            if sidecar != extension_profile:
                raise ValueError("extension selection does not match manifest")
        package = extension_profile["package"]
        sources = manifest.get("sources")
        if not isinstance(sources, list) or sources != ATTRS_SOURCES:
            raise ValueError("attrs artifact source set is invalid")
        attrs_sources = [row for row in sources if isinstance(row, dict) and row.get("id") == "attrs-source"]
        if len(attrs_sources) != 1:
            raise ValueError("attrs extension source is missing")
        source = attrs_sources[0]
        if source.get("version") != package["version"] or source.get("sha256") != package["source_archive_sha256"] or source.get("license") != package["repository_license_id"] or source.get("role") != "python-package" or source.get("artifact_relation") != "packaged":
            raise ValueError("attrs extension source does not match package profile")
    else:
        raise ValueError("unsupported artifact profile")

    if profile == "attrs-770":
        assert isinstance(extension_profile, dict)
        packages = manifest.get("packages")
        package = extension_profile["package"]
        if (
            not isinstance(packages, list)
            or len(packages) != 2
            or not isinstance(packages[0], dict)
            or packages[0].get("name") != "cpython"
            or packages[0].get("status") != "core"
            or not isinstance(packages[0].get("version"), str)
            or not packages[0]["version"]
            or packages[1] != {"name": "attrs", "version": package["version"], "status": "selected-pure-python"}
        ):
            raise ValueError("attrs artifact package set is invalid")
    else:
        packages = manifest.get("packages")
        if not isinstance(packages, list) or not packages:
            raise ValueError("artifact package set is invalid")
    require_exact_rows(packages, PACKAGE_FIELDS, "manifest package")

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
    parser.add_argument("--extension-selection", type=pathlib.Path)
    args = parser.parse_args()
    manifest = load_json_strict(args.manifest, MANIFEST_FIELDS)

    inventory_record = manifest.get("python_import_inventory")
    inventory = None
    if isinstance(inventory_record, dict):
        filename = inventory_record.get("filename")
        if not isinstance(filename, str) or pathlib.Path(filename).name != filename:
            raise ValueError("invalid import inventory filename")
        inventory = args.manifest.parent / filename
    qualification_record = manifest.get("python_import_qualification")
    qualification = None
    if isinstance(qualification_record, dict):
        filename = qualification_record.get("filename")
        if not isinstance(filename, str) or pathlib.Path(filename).name != filename:
            raise ValueError("invalid import qualification filename")
        qualification = args.manifest.parent / filename
    verify(args.artifact, manifest, args.extension_selection, inventory, qualification)
    print(json.dumps({"artifact": str(args.artifact), "sha256": sha256(args.artifact)}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
