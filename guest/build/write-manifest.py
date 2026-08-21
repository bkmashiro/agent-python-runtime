#!/usr/bin/env python3
"""Write a deterministic provenance manifest for a built guest artifact."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import subprocess
from typing import Any

IMPORT_RE = re.compile(r'\(import\s+"([^"]+)"\s+"([^"]+)"')
EXPORT_RE = re.compile(r'\(export\s+"([^"]+)"')
MEMORY_RE = re.compile(r'^\s*\(memory(?:\s+\(;\d+;\))?\s+(\d+)\s+(\d+)\s*\)', re.MULTILINE)

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


def load_package_profile_module():
    path = pathlib.Path(__file__).with_name("package_profile.py")
    spec = importlib.util.spec_from_file_location("artifact_package_profile", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load package profile registry")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


IMPORT_INVENTORY = load_import_inventory_module()
IMPORT_QUALIFICATION = load_import_qualification_module()
EXTENSION_PROFILE = load_extension_profile_module()
PACKAGE_PROFILE = load_package_profile_module()
PROFILE_REGISTRY = PACKAGE_PROFILE.load_registry()
ARTIFACT_FILENAMES = {
    profile_id: PACKAGE_PROFILE.resolve_profile(PROFILE_REGISTRY, profile_id)["artifact_filename"]
    for profile_id in PACKAGE_PROFILE.profile_ids(PROFILE_REGISTRY)
}


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def parse_memory_bounds(wat_text: str) -> tuple[int, int] | None:
    matches = MEMORY_RE.findall(wat_text)
    if not matches:
        return None
    if len(matches) != 1:
        raise ValueError("artifact WAT must contain exactly one bounded memory declaration")
    initial, maximum = (int(value) for value in matches[0])
    if initial <= 0 or maximum < initial:
        raise ValueError("artifact WAT memory bounds are invalid")
    return initial, maximum


def locked_source_version(lock: dict[str, Any], source_id: str) -> str:
    sources = lock.get("sources")
    rows = (
        [row for row in sources if isinstance(row, dict) and row.get("id") == source_id]
        if isinstance(sources, list)
        else []
    )
    version = rows[0].get("version") if len(rows) == 1 else None
    if (
        not isinstance(version, str)
        or not version
        or version.strip() != version
    ):
        raise ValueError(f"source lock requires exactly one versioned {source_id}")
    return version


def build_manifest(
    *,
    artifact: pathlib.Path,
    wat: pathlib.Path,
    source_lock: pathlib.Path,
    commit: str,
    source_date_epoch: str,
    artifact_profile: str,
    extension_selection: pathlib.Path | None,
    import_inventory: pathlib.Path,
    import_qualification: pathlib.Path,
    memory_initial_pages: int | None = None,
    memory_maximum_pages: int | None = None,
) -> dict[str, Any]:
    if artifact_profile not in ARTIFACT_FILENAMES:
        raise ValueError(f"unsupported artifact profile: {artifact_profile}")
    expected_filename = ARTIFACT_FILENAMES[artifact_profile]
    if artifact.name != expected_filename:
        raise ValueError(
            f"artifact filename {artifact.name!r} does not match profile "
            f"{artifact_profile!r}: expected {expected_filename!r}"
        )

    if import_inventory.name != "import-inventory.json":
        raise ValueError("import inventory must use canonical filename")
    inventory = IMPORT_INVENTORY.load_inventory(import_inventory, artifact_profile)
    inventory_record = {
        key: value for key, value in inventory.items() if key != "artifact_profile"
    }
    inventory_record["filename"] = import_inventory.name
    inventory_record["sha256"] = sha256(import_inventory)

    if import_qualification.name != "import-qualification.json":
        raise ValueError("import qualification must use canonical filename")
    qualification = IMPORT_QUALIFICATION.validate_qualification(
        IMPORT_QUALIFICATION.strict_json_loads(import_qualification.read_text()),
        artifact_profile,
    )
    if (
        qualification["implementation"] != inventory["implementation"]
        or qualification["python_version"] != inventory["python_version"]
        or not set(qualification["qualified_roots"]).issubset(inventory["discoverable_roots"])
    ):
        raise ValueError("import qualification does not match import inventory")
    qualification_record = {
        key: value for key, value in qualification.items() if key != "artifact_profile"
    }
    qualification_record["filename"] = import_qualification.name
    qualification_record["sha256"] = sha256(import_qualification)

    extension_profile = None
    if artifact_profile == "base":
        if extension_selection is not None:
            raise ValueError("base artifact profile forbids extension selection")
    elif artifact_profile == "attrs-770":
        if extension_selection is None:
            raise ValueError("attrs-770 artifact profile requires extension selection")
        extension_profile = EXTENSION_PROFILE.load_selection(extension_selection)
    else:
        raise ValueError("unsupported artifact profile")

    lock = json.loads(source_lock.read_text())
    wat_text = wat.read_text()
    imports = [
        {"module": module, "name": name}
        for module, name in IMPORT_RE.findall(wat_text)
    ]
    exports = EXPORT_RE.findall(wat_text)
    packages = [
        {
            "name": "cpython",
            "version": locked_source_version(lock, "cpython-source"),
            "status": "core",
        },
    ]
    if artifact_profile == "attrs-770":
        assert extension_profile is not None
        attrs_version = locked_source_version(lock, "attrs-source")
        if extension_profile["package"]["version"] != attrs_version:
            raise ValueError("extension package version does not match source lock")
        packages.append({
            "name": "attrs",
            "version": attrs_version,
            "status": "selected-pure-python",
        })
    limitations = [
        "import qualification covers only the named guest-import-exec-v1 operations and is not transitive closure or arbitrary behavior proof",
        "Host tools are explicitly registered and call-bounded",
        "the proof of concept does not provide package installation or native extensions",
    ]
    if artifact_profile == "attrs-770":
        limitations[-1] = "the profile provides one pinned pure-Python package and no runtime package installation or native extensions"

    detected_memory = parse_memory_bounds(wat_text)
    if (memory_initial_pages is None) != (memory_maximum_pages is None):
        raise ValueError("memory initial and maximum pages must be provided together")
    if memory_initial_pages is None:
        if detected_memory is None:
            raise ValueError("memory page bounds are required when WAT has no bounded memory declaration")
        memory_initial_pages, memory_maximum_pages = detected_memory
    elif detected_memory is not None and detected_memory != (memory_initial_pages, memory_maximum_pages):
        raise ValueError("declared memory page bounds do not match artifact WAT")
    assert memory_initial_pages is not None and memory_maximum_pages is not None
    if memory_initial_pages <= 0 or memory_maximum_pages < memory_initial_pages:
        raise ValueError("memory page bounds are invalid")
    wasm = {
        "imports": imports,
        "exports": exports,
    }
    if memory_initial_pages is not None:
        wasm["memory"] = {
            "initial_pages": memory_initial_pages,
            "maximum_pages": memory_maximum_pages,
            "fixed": memory_initial_pages == memory_maximum_pages,
        }

    return {
        "schema_version": 4,
        "abi_version": "v1",
        "artifact_profile": artifact_profile,
        "target": lock["target"],
        "artifact": {
            "filename": artifact.name,
            "size": artifact.stat().st_size,
            "sha256": sha256(artifact),
        },
        "build": {
            "repository_commit": commit,
            "source_date_epoch": source_date_epoch,
            "compiler_target": "wasm32-wasip1",
            "execution_model": "reactor",
        },
        "sources": lock["sources"],
        "wasm": wasm,
        "packages": packages,
        "extension_profile": extension_profile,
        "python_import_inventory": inventory_record,
        "python_import_qualification": qualification_record,
        "limitations": limitations,
    }


def git_commit() -> str:
    return subprocess.check_output(
        ["git", "rev-parse", "HEAD"], text=True
    ).strip()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifact", required=True, type=pathlib.Path)
    parser.add_argument("--wat", required=True, type=pathlib.Path)
    parser.add_argument("--source-lock", required=True, type=pathlib.Path)
    parser.add_argument(
        "--artifact-profile", choices=sorted(ARTIFACT_FILENAMES), default="base"
    )
    parser.add_argument("--extension-selection", type=pathlib.Path)
    parser.add_argument("--import-inventory", required=True, type=pathlib.Path)
    parser.add_argument("--import-qualification", required=True, type=pathlib.Path)
    parser.add_argument("--memory-initial-pages", type=int)
    parser.add_argument("--memory-maximum-pages", type=int)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()

    manifest = build_manifest(
        artifact=args.artifact,
        wat=args.wat,
        source_lock=args.source_lock,
        commit=os.environ.get("GITHUB_SHA") or git_commit(),
        source_date_epoch=os.environ.get("SOURCE_DATE_EPOCH", "unknown"),
        artifact_profile=args.artifact_profile,
        extension_selection=args.extension_selection,
        import_inventory=args.import_inventory,
        import_qualification=args.import_qualification,
        memory_initial_pages=args.memory_initial_pages,
        memory_maximum_pages=args.memory_maximum_pages,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
