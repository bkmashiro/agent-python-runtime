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
ARTIFACT_FILENAMES = {
    "base": "agent-python-runtime.wasm",
    "numpy-core": "agent-python-runtime-numpy-core.wasm",
}
NUMPY_CORE_MODULES = (
    "numpy._core._multiarray_umath",
    "numpy.linalg._umath_linalg",
)


def load_import_inventory_module():
    path = pathlib.Path(__file__).with_name("import_inventory.py")
    spec = importlib.util.spec_from_file_location("artifact_import_inventory", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load import inventory validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


IMPORT_INVENTORY = load_import_inventory_module()


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


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

    extension_profile = None
    if artifact_profile == "base":
        if extension_selection is not None:
            raise ValueError("base artifact profile forbids extension selection")
    else:
        if extension_selection is None:
            raise ValueError("numpy-core artifact profile requires extension selection")
        selection = json.loads(extension_selection.read_text())
        modules = [row.get("module") for row in selection.get("modules", [])]
        if (
            selection.get("schema_version") != 1
            or selection.get("package") != "numpy"
            or selection.get("profile") != "core"
            or modules != list(NUMPY_CORE_MODULES)
        ):
            raise ValueError("numpy-core artifact profile requires exact core extension selection")
        extension_profile = {
            "filename": extension_selection.name,
            "manifest_sha256": sha256(extension_selection),
            "profile": "core",
            "modules": modules,
            "link_input_count": len(selection.get("link_inputs", [])),
        }

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
    limitations = [
        "fetch_many is the only built-in capability and requires explicit Host grants; frozen catalogs may project additional typed Host tools",
        "WASI execution evidence is recorded separately",
    ]
    if artifact_profile == "base":
        limitations.insert(0, "NumPy is not included in the core artifact")
    else:
        packages.append(
            {
                "name": "numpy",
                "version": locked_source_version(lock, "numpy-source"),
                "status": "selected-core",
            }
        )
        limitations.insert(0, "NumPy random and FFT are not included")

    if (memory_initial_pages is None) != (memory_maximum_pages is None):
        raise ValueError("memory initial and maximum pages must be provided together")
    if memory_initial_pages is not None and memory_maximum_pages is not None and (
        memory_initial_pages <= 0 or memory_maximum_pages < memory_initial_pages
    ):
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
        "schema_version": 3,
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
    parser.add_argument("--memory-initial-pages", required=True, type=int)
    parser.add_argument("--memory-maximum-pages", required=True, type=int)
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
        memory_initial_pages=args.memory_initial_pages,
        memory_maximum_pages=args.memory_maximum_pages,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
