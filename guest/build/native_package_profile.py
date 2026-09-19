#!/usr/bin/env python3
"""Validate and materialize repository-declared static native package profiles."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import stat
from typing import Any

LOCK_FIELDS = {
    "schema_version", "kind", "artifact_profile", "artifact_filename", "target",
    "sources", "package", "build", "native_modules", "support_libraries", "qualification", "policy",
}
SOURCE_FIELDS = {"id", "version", "url", "sha256", "license", "role", "artifact_relation"}
PACKAGE_FIELDS = {
    "name", "version", "source_commit", "source_archive_sha256",
    "repository_license_id", "import_root", "status", "max_packaged_tree_bytes",
}
BUILD_FIELDS = {
    "recipe", "reference_repository", "reference_commit", "reference_build_sha256",
    "reference_static_build_sha256", "cpython_version", "wasi_sdk_version",
    "build_parallelism", "blas_order", "lapack_order", "disable_svml", "disable_optimization",
    "link_libraries",
}
POLICY_FIELDS = {
    "runtime_package_installation", "dynamic_native_loading",
    "external_prebuilt_artifact", "pysolate_runtime_integration",
}
MODULE_FIELDS = {"name", "archive", "init_symbol"}
SUPPORT_LIBRARY_FIELDS = {"name", "archive"}
QUALIFICATION_FIELDS = {"name", "operation"}
SELECTION_FIELDS = {
    "schema_version", "kind", "profile", "package", "build", "native_modules",
    "support_libraries", "link_input_count", "identity",
}
SELECTION_PACKAGE_FIELDS = {
    "name", "version", "status", "import_root", "install_path", "source_commit",
    "source_archive_sha256", "repository_license_id", "tree_sha256", "file_count", "total_bytes",
}
SELECTION_BUILD_FIELDS = {
    "recipe", "reference_repository", "reference_commit", "reference_build_sha256",
    "reference_static_sha256", "cpython_version", "wasi_sdk_version", "link_libraries",
}
SELECTION_MODULE_FIELDS = {"name", "archive", "init_symbol", "archive_sha256", "archive_size"}
SELECTION_SUPPORT_FIELDS = {"name", "archive", "archive_sha256", "archive_size"}
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
IDENTIFIER_RE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,127}$")
MODULE_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+$")
SYMBOL_RE = re.compile(r"^PyInit_[A-Za-z0-9_]+$")
MAX_JSON_BYTES = 1 << 20
MAX_FILES = 20000
MAX_MODULES = 64


def _unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def _reject_constant(value: str) -> None:
    raise ValueError(f"invalid JSON constant: {value}")


def strict_json_loads(encoded: str) -> dict[str, Any]:
    if not encoded or len(encoded.encode()) > MAX_JSON_BYTES:
        raise ValueError("profile JSON size is invalid")
    value = json.loads(encoded, object_pairs_hook=_unique_object, parse_constant=_reject_constant)
    if not isinstance(value, dict):
        raise ValueError("profile JSON must be an object")
    return value


def _exact(value: Any, fields: set[str], label: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != fields:
        raise ValueError(f"{label} fields are invalid")
    return value


def _digest(value: dict[str, Any]) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()
    return hashlib.sha256(encoded).hexdigest()


def validate_lock(value: dict[str, Any]) -> dict[str, Any]:
    _exact(value, LOCK_FIELDS, "native package lock")
    if value["schema_version"] != 1 or value["kind"] != "static-native-package":
        raise ValueError("unsupported native package lock")
    if value["artifact_profile"] != "numpy-core" or value["artifact_filename"] != "agent-python-runtime-numpy-core.wasm" or value["target"] != "wasm32-wasip1":
        raise ValueError("native package lock identity is invalid")
    sources = value["sources"]
    if not isinstance(sources, list) or not sources:
        raise ValueError("native package sources are invalid")
    source_ids = []
    for source in sources:
        _exact(source, SOURCE_FIELDS, "native package source")
        if not isinstance(source["id"], str) or not IDENTIFIER_RE.fullmatch(source["id"]):
            raise ValueError("native package source id is invalid")
        if not isinstance(source["sha256"], str) or not SHA256_RE.fullmatch(source["sha256"]):
            raise ValueError("native package source digest is invalid")
        if source["artifact_relation"] not in {"build-only", "linked", "packaged"}:
            raise ValueError("native package source relation is invalid")
        for key in ("version", "url", "license", "role"):
            if not isinstance(source[key], str) or not source[key]:
                raise ValueError("native package source metadata is invalid")
        source_ids.append(source["id"])
    if source_ids != sorted(set(source_ids)):
        raise ValueError("native package sources must be sorted unique")
    package = _exact(value["package"], PACKAGE_FIELDS, "native package")
    if package["name"] != "numpy" or package["import_root"] != "numpy" or package["status"] != "selected-static-native":
        raise ValueError("native package declaration is invalid")
    if package["source_commit"] != "7bc18034031f32e5d03bb646c472dabd1623e9d5" or not SHA256_RE.fullmatch(package["source_archive_sha256"]):
        raise ValueError("native package source identity is invalid")
    numpy_source = next((row for row in sources if row["id"] == "numpy-source"), None)
    if numpy_source is None or numpy_source["sha256"] != package["source_archive_sha256"] or numpy_source["license"] != package["repository_license_id"]:
        raise ValueError("native package source does not match package declaration")
    if not isinstance(package["max_packaged_tree_bytes"], int) or not 0 < package["max_packaged_tree_bytes"] <= 256 << 20:
        raise ValueError("native package tree bound is invalid")
    build = _exact(value["build"], BUILD_FIELDS, "native package build")
    if build["recipe"] != "numpy-static-v1" or build["reference_commit"] != "184cce0b537088be76e1e8a06d6fe742e2f29ff4" or build["cpython_version"] != "3.14.0" or build["wasi_sdk_version"] != "33":
        raise ValueError("native package build identity is invalid")
    for key in ("reference_build_sha256", "reference_static_build_sha256"):
        if not SHA256_RE.fullmatch(build[key]):
            raise ValueError("native package reference digest is invalid")
    if build["build_parallelism"] != 4 or build["blas_order"] != "" or build["lapack_order"] != "" or build["disable_svml"] is not True or build["disable_optimization"] is not True or build["link_libraries"] != ["c-printscan-long-double"]:
        raise ValueError("native package build policy is invalid")
    modules = value["native_modules"]
    if not isinstance(modules, list) or not 0 < len(modules) <= MAX_MODULES:
        raise ValueError("native module inventory is invalid")
    names = []
    archives = []
    symbols = []
    for module in modules:
        _exact(module, MODULE_FIELDS, "native module")
        name, archive, symbol = module["name"], module["archive"], module["init_symbol"]
        path = pathlib.PurePosixPath(archive)
        if not isinstance(name, str) or not MODULE_RE.fullmatch(name) or not name.startswith("numpy."):
            raise ValueError("native module name is invalid")
        if not isinstance(archive, str) or path.is_absolute() or ".." in path.parts or path.suffix != ".a" or path.parts[0] != "numpy":
            raise ValueError("native module archive is invalid")
        if not isinstance(symbol, str) or not SYMBOL_RE.fullmatch(symbol):
            raise ValueError("native module init symbol is invalid")
        names.append(name)
        archives.append(archive)
        symbols.append(symbol)
    if names != sorted(set(names)) or len(set(archives)) != len(archives) or len(set(symbols)) != len(symbols):
        raise ValueError("native modules must be sorted and unique")
    support_libraries = value["support_libraries"]
    if not isinstance(support_libraries, list) or len(support_libraries) != 2:
        raise ValueError("native support library set is invalid")
    support_rows = []
    for library in support_libraries:
        _exact(library, SUPPORT_LIBRARY_FIELDS, "native support library")
        name, archive = library["name"], library["archive"]
        path = pathlib.PurePosixPath(archive)
        if not isinstance(name, str) or not IDENTIFIER_RE.fullmatch(name) or not isinstance(archive, str) or path.is_absolute() or ".." in path.parts or path.suffix != ".a" or path.parts[:2] != ("numpy", "lib"):
            raise ValueError("native support library is invalid")
        support_rows.append((name, archive))
    if support_rows != [("npymath", "numpy/lib/libnpymath.a"), ("npyrandom", "numpy/lib/libnpyrandom.a")]:
        raise ValueError("native support library set is not the locked closure")
    if set(archives).intersection(archive for _, archive in support_rows):
        raise ValueError("native archive paths are not unique")
    policy = _exact(value["policy"], POLICY_FIELDS, "native package policy")
    if policy != {
        "runtime_package_installation": False,
        "dynamic_native_loading": False,
        "external_prebuilt_artifact": False,
        "pysolate_runtime_integration": True,
    }:
        raise ValueError("native package policy is invalid")
    qualification = value["qualification"]
    if not isinstance(qualification, list) or not qualification:
        raise ValueError("native package qualification is invalid")
    rows = []
    for row in qualification:
        _exact(row, QUALIFICATION_FIELDS, "native package qualification")
        if row["name"] != "numpy" or not isinstance(row["operation"], str) or not row["operation"]:
            raise ValueError("native package qualification row is invalid")
        rows.append((row["name"], row["operation"]))
    if rows != [("numpy", "numpy_core_oracle")]:
        raise ValueError("native package qualification must be the locked core oracle")
    return value


def load_lock(path: pathlib.Path) -> dict[str, Any]:
    return validate_lock(strict_json_loads(path.read_text()))


def source_lock(lock: dict[str, Any]) -> dict[str, Any]:
    validate_lock(lock)
    return {"schema_version": 1, "target": lock["target"], "sources": sorted(lock["sources"], key=lambda row: row["id"])}


def source_lock_projection(lock: dict[str, Any]) -> dict[str, Any]:
    return source_lock(lock)


def merge_source_lock(base: dict[str, Any], lock: dict[str, Any]) -> dict[str, Any]:
    validate_lock(lock)
    if not isinstance(base, dict) or set(base) != {"schema_version", "target", "sources"} or base["schema_version"] != 1 or base["target"] != lock["target"] or not isinstance(base["sources"], list):
        raise ValueError("base source lock is invalid")
    merged = list(base["sources"]) + list(lock["sources"])
    identifiers = [row.get("id") for row in merged if isinstance(row, dict)]
    if len(identifiers) != len(merged) or len(set(identifiers)) != len(identifiers):
        raise ValueError("merged source ids are not unique")
    return {"schema_version": 1, "target": lock["target"], "sources": sorted(merged, key=lambda row: row["id"])}


def package_tree_identity(root: pathlib.Path) -> dict[str, Any]:
    if not root.is_dir() or root.is_symlink():
        raise ValueError("package root must be a real directory")
    digest = hashlib.sha256()
    file_count = 0
    total_bytes = 0
    for path in sorted(root.rglob("*"), key=lambda item: item.relative_to(root).as_posix()):
        relative = path.relative_to(root).as_posix()
        if path.is_symlink():
            raise ValueError("package tree symlinks are forbidden")
        if path.is_dir():
            continue
        if not path.is_file() or file_count >= MAX_FILES:
            raise ValueError("package tree is invalid or too large")
        mode = stat.S_IMODE(path.stat().st_mode)
        if mode & 0o111:
            raise ValueError("package tree executable files are forbidden")
        payload = path.read_bytes()
        file_count += 1
        total_bytes += len(payload)
        digest.update(relative.encode())
        digest.update(b"\0")
        digest.update(str(len(payload)).encode())
        digest.update(b"\0")
        digest.update(payload)
    if file_count == 0:
        raise ValueError("package tree is empty")
    return {"tree_sha256": digest.hexdigest(), "file_count": file_count, "total_bytes": total_bytes}


def registration_header(lock: dict[str, Any]) -> str:
    validate_lock(lock)
    lines = ["/* Generated from numpy-core.lock.json; do not edit. */"]
    for module in lock["native_modules"]:
        lines.append(f"extern PyObject *{module['init_symbol']}(void);")
    lines.extend(["", "static int register_selected_builtins(void) {"])
    for module in lock["native_modules"]:
        lines.append(f'    if (PyImport_AppendInittab("{module["name"]}", {module["init_symbol"]}) != 0) return -1;')
    lines.extend(["    return 0;", "}", ""])
    return "\n".join(lines)


def build_selection(lock: dict[str, Any], package_root: pathlib.Path, archive_root: pathlib.Path) -> dict[str, Any]:
    validate_lock(lock)
    tree = package_tree_identity(package_root)
    if tree["total_bytes"] > lock["package"]["max_packaged_tree_bytes"]:
        raise ValueError("package tree exceeds locked byte bound")
    modules = []
    for declaration in lock["native_modules"]:
        archive = archive_root / pathlib.PurePosixPath(declaration["archive"])
        if not archive.is_file() or archive.is_symlink():
            raise ValueError(f"missing native archive: {declaration['archive']}")
        payload = archive.read_bytes()
        if not payload:
            raise ValueError("native archive is empty")
        modules.append({
            **declaration,
            "archive_sha256": hashlib.sha256(payload).hexdigest(),
            "archive_size": len(payload),
        })
    support_libraries = []
    for declaration in lock["support_libraries"]:
        archive = archive_root / pathlib.PurePosixPath(declaration["archive"])
        if not archive.is_file() or archive.is_symlink():
            raise ValueError(f"missing support archive: {declaration['archive']}")
        payload = archive.read_bytes()
        if not payload:
            raise ValueError("native support archive is empty")
        support_libraries.append({
            **declaration,
            "archive_sha256": hashlib.sha256(payload).hexdigest(),
            "archive_size": len(payload),
        })
    package = lock["package"]
    build = lock["build"]
    selection: dict[str, Any] = {
        "schema_version": 1,
        "kind": "static-native-package",
        "profile": lock["artifact_profile"],
        "package": {
            "name": package["name"], "version": package["version"], "status": package["status"],
            "import_root": package["import_root"], "install_path": "site-packages/numpy",
            "source_commit": package["source_commit"], "source_archive_sha256": package["source_archive_sha256"],
            "repository_license_id": package["repository_license_id"], **tree,
        },
        "build": {
            "recipe": build["recipe"], "reference_repository": build["reference_repository"],
            "reference_commit": build["reference_commit"], "reference_build_sha256": build["reference_build_sha256"],
            "reference_static_sha256": build["reference_static_build_sha256"],
            "cpython_version": build["cpython_version"], "wasi_sdk_version": build["wasi_sdk_version"],
            "link_libraries": build["link_libraries"],
        },
        "native_modules": modules,
        "support_libraries": support_libraries,
        "link_input_count": len(modules) + len(support_libraries),
        "identity": "",
    }
    selection["identity"] = "sha256:" + _digest({**selection, "identity": ""})
    return validate_selection(selection, lock)


def validate_selection(value: dict[str, Any], lock: dict[str, Any]) -> dict[str, Any]:
    validate_lock(lock)
    _exact(value, SELECTION_FIELDS, "native package selection")
    if value["schema_version"] != 1 or value["kind"] != "static-native-package" or value["profile"] != lock["artifact_profile"]:
        raise ValueError("native package selection identity is invalid")
    package = _exact(value["package"], SELECTION_PACKAGE_FIELDS, "native selection package")
    declared = lock["package"]
    for key in ("name", "version", "status", "source_commit", "source_archive_sha256", "repository_license_id", "import_root"):
        if package[key] != declared[key]:
            raise ValueError("native selection package drift")
    if package["install_path"] != "site-packages/numpy":
        raise ValueError("native selection install path drift")
    if not SHA256_RE.fullmatch(package["tree_sha256"]) or not isinstance(package["file_count"], int) or not 0 < package["file_count"] <= MAX_FILES or not isinstance(package["total_bytes"], int) or not 0 < package["total_bytes"] <= declared["max_packaged_tree_bytes"]:
        raise ValueError("native selection package tree is invalid")
    build = _exact(value["build"], SELECTION_BUILD_FIELDS, "native selection build")
    locked_build = lock["build"]
    expected_build = {
        "recipe": locked_build["recipe"],
        "reference_repository": locked_build["reference_repository"],
        "reference_commit": locked_build["reference_commit"],
        "reference_build_sha256": locked_build["reference_build_sha256"],
        "reference_static_sha256": locked_build["reference_static_build_sha256"],
        "cpython_version": locked_build["cpython_version"],
        "wasi_sdk_version": locked_build["wasi_sdk_version"],
        "link_libraries": locked_build["link_libraries"],
    }
    if build != expected_build:
        raise ValueError("native selection build drift")
    modules = value["native_modules"]
    if not isinstance(modules, list) or len(modules) != len(lock["native_modules"]):
        raise ValueError("native selection module count drift")
    for actual, expected in zip(modules, lock["native_modules"]):
        _exact(actual, SELECTION_MODULE_FIELDS, "native selection module")
        for key in MODULE_FIELDS:
            if actual[key] != expected[key]:
                raise ValueError("native selection module drift")
        if not SHA256_RE.fullmatch(actual["archive_sha256"]) or not isinstance(actual["archive_size"], int) or actual["archive_size"] <= 0:
            raise ValueError("native selection archive identity is invalid")
    support_libraries = value["support_libraries"]
    if not isinstance(support_libraries, list) or len(support_libraries) != len(lock["support_libraries"]):
        raise ValueError("native selection support library count drift")
    for actual, expected in zip(support_libraries, lock["support_libraries"]):
        _exact(actual, SELECTION_SUPPORT_FIELDS, "native selection support library")
        for key in SUPPORT_LIBRARY_FIELDS:
            if actual[key] != expected[key]:
                raise ValueError("native selection support library drift")
        if not SHA256_RE.fullmatch(actual["archive_sha256"]) or not isinstance(actual["archive_size"], int) or actual["archive_size"] <= 0:
            raise ValueError("native selection support archive identity is invalid")
    if value["link_input_count"] != len(modules) + len(support_libraries):
        raise ValueError("native selection link count drift")
    identity = value["identity"]
    expected_identity = "sha256:" + _digest({**value, "identity": ""})
    if identity != expected_identity:
        raise ValueError("native package selection identity mismatch")
    return value


def _write_json(path: pathlib.Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    validate = sub.add_parser("validate-lock")
    validate.add_argument("--lock", type=pathlib.Path, required=True)
    project = sub.add_parser("source-lock")
    project.add_argument("--lock", type=pathlib.Path, required=True)
    project.add_argument("--output", type=pathlib.Path, required=True)
    merge = sub.add_parser("effective-source-lock")
    merge.add_argument("--lock", type=pathlib.Path, required=True)
    merge.add_argument("--source-lock", type=pathlib.Path, required=True)
    merge.add_argument("--output", type=pathlib.Path, required=True)
    header = sub.add_parser("registration-header")
    header.add_argument("--lock", type=pathlib.Path, required=True)
    header.add_argument("--output", type=pathlib.Path, required=True)
    prepare = sub.add_parser("prepare")
    prepare.add_argument("--lock", type=pathlib.Path, required=True)
    prepare.add_argument("--package-root", type=pathlib.Path, required=True)
    prepare.add_argument("--archive-root", type=pathlib.Path, required=True)
    prepare.add_argument("--selection-output", type=pathlib.Path, required=True)
    archives = sub.add_parser("archive-paths")
    archives.add_argument("--lock", type=pathlib.Path, required=True)
    archives.add_argument("--archive-root", type=pathlib.Path, required=True)
    sub.add_parser("init-symbols").add_argument("--lock", type=pathlib.Path, required=True)
    sub.add_parser("link-libraries").add_argument("--lock", type=pathlib.Path, required=True)
    args = parser.parse_args()
    lock = load_lock(args.lock)
    if args.command == "validate-lock":
        return 0
    if args.command == "source-lock":
        _write_json(args.output, source_lock(lock))
    elif args.command == "effective-source-lock":
        base = strict_json_loads(args.source_lock.read_text())
        _write_json(args.output, merge_source_lock(base, lock))
    elif args.command == "registration-header":
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(registration_header(lock))
    elif args.command == "prepare":
        _write_json(args.selection_output, build_selection(lock, args.package_root, args.archive_root))
    elif args.command == "archive-paths":
        root = args.archive_root.resolve()
        for declaration in [*lock["native_modules"], *lock["support_libraries"]]:
            archive = (root / pathlib.PurePosixPath(declaration["archive"])).resolve()
            if root not in archive.parents or not archive.is_file() or archive.is_symlink():
                raise ValueError(f"missing native archive: {declaration['archive']}")
            print(archive)
    elif args.command == "init-symbols":
        for module in lock["native_modules"]:
            print(module["init_symbol"])
    elif args.command == "link-libraries":
        flags = {"c-printscan-long-double": "-lc-printscan-long-double"}
        for library in lock["build"]["link_libraries"]:
            print(flags[library])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
