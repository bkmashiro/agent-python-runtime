#!/usr/bin/env python3
"""Validate one pinned pure-Python extension profile and its staged package tree."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any


PROFILE = "attrs-770"
PROFILE_LOCK = pathlib.Path(__file__).with_name("profiles") / "attrs-770.lock.json"
ARTIFACT_FILENAME = "agent-python-runtime-attrs-770.wasm"
SCHEMA_VERSION = 1
TREE_DOMAIN = b"pysolate-package-tree-v1\0"
HEX = re.compile(r"^[0-9a-f]{64}$")
COMMIT = re.compile(r"^[0-9a-f]{40}$")
MAX_FILES = 4096
MAX_TOTAL_BYTES = 32 << 20
MAX_FILE_BYTES = 4 << 20


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            raise ValueError(f"duplicate JSON key: {key}")
        value[key] = item
    return value


def strict_json_loads(encoded: str) -> Any:
    try:
        return json.loads(encoded, object_pairs_hook=_strict_object)
    except (json.JSONDecodeError, TypeError) as exc:
        raise ValueError("invalid JSON") from exc


def _exact_fields(value: Any, fields: set[str], label: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != fields:
        raise ValueError(f"invalid {label} fields")
    return value


def validate_lock(value: Any) -> dict[str, Any]:
    lock = _exact_fields(
        value,
        {"schema_version", "artifact_profile", "artifact_filename", "source", "package", "qualification"},
        "extension profile lock",
    )
    if lock["schema_version"] != SCHEMA_VERSION or lock["artifact_profile"] != PROFILE or lock["artifact_filename"] != ARTIFACT_FILENAME:
        raise ValueError("invalid extension profile identity")
    source = _exact_fields(
        lock["source"],
        {
            "id", "version", "url", "sha256", "license", "role", "artifact_relation",
            "source_commit", "source_subdirectory", "patch_sha256",
        },
        "extension source",
    )
    if (
        source["id"] != "attrs-source"
        or not isinstance(source["version"], str) or not source["version"] or len(source["version"]) > 64
        or not isinstance(source["url"], str) or not source["url"].startswith("https://") or len(source["url"]) > 512
        or not isinstance(source["sha256"], str) or HEX.fullmatch(source["sha256"]) is None
        or source["license"] != "MIT"
        or source["role"] != "python-package"
        or source["artifact_relation"] != "packaged"
        or not isinstance(source["source_commit"], str) or COMMIT.fullmatch(source["source_commit"]) is None
        or source["source_subdirectory"] != "src/attr"
        or not isinstance(source["patch_sha256"], str) or HEX.fullmatch(source["patch_sha256"]) is None
    ):
        raise ValueError("invalid extension source identity")
    package = _exact_fields(
        lock["package"],
        {"name", "status", "import_root", "install_path", "tree_sha256", "file_count", "total_bytes"},
        "extension package",
    )
    if (
        package["name"] != "attrs"
        or package["status"] != "selected-pure-python"
        or package["import_root"] != "attr"
        or package["install_path"] != "site-packages/attr"
        or not isinstance(package["tree_sha256"], str) or HEX.fullmatch(package["tree_sha256"]) is None
        or not isinstance(package["file_count"], int) or not 1 <= package["file_count"] <= MAX_FILES
        or not isinstance(package["total_bytes"], int) or not 1 <= package["total_bytes"] <= MAX_TOTAL_BYTES
    ):
        raise ValueError("invalid extension package identity")
    expected_qualification = [
        {"name": "attr", "operation": "generic_dynamic_class"},
        {"name": "types", "operation": "new_class"},
        {"name": "typing", "operation": "generic_alias"},
    ]
    if lock["qualification"] != expected_qualification:
        raise ValueError("invalid extension qualification contract")
    return lock


def load_lock(path: pathlib.Path) -> dict[str, Any]:
    return validate_lock(strict_json_loads(path.read_text()))


def package_tree_identity(root: pathlib.Path) -> dict[str, Any]:
    if not root.is_dir() or root.is_symlink():
        raise ValueError("package tree root must be a real directory")
    digest = hashlib.sha256()
    digest.update(TREE_DOMAIN)
    count = 0
    total = 0
    entries = sorted(root.rglob("*"), key=lambda path: path.relative_to(root).as_posix())
    for path in entries:
        if path.is_symlink():
            raise ValueError("package tree must contain only regular files and directories")
        if path.is_dir():
            continue
        if not path.is_file():
            raise ValueError("package tree must contain only regular files and directories")
        relative = path.relative_to(root).as_posix()
        if relative.startswith("/") or relative in {"", "."} or ".." in pathlib.PurePosixPath(relative).parts:
            raise ValueError("package tree path is invalid")
        body = path.read_bytes()
        if len(body) > MAX_FILE_BYTES:
            raise ValueError("package tree file exceeds bound")
        count += 1
        total += len(body)
        if count > MAX_FILES or total > MAX_TOTAL_BYTES:
            raise ValueError("package tree exceeds bound")
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        digest.update(str(len(body)).encode("ascii"))
        digest.update(b"\0")
        digest.update(body)
    if count == 0:
        raise ValueError("package tree is empty")
    return {"tree_sha256": digest.hexdigest(), "file_count": count, "total_bytes": total}


def verify_patch(lock: dict[str, Any], patch: pathlib.Path) -> None:
    validate_lock(lock)
    if not patch.is_file() or patch.is_symlink():
        raise ValueError("extension patch must be a regular file")
    actual = hashlib.sha256(patch.read_bytes()).hexdigest()
    if actual != lock["source"]["patch_sha256"]:
        raise ValueError("extension patch digest mismatch")


def build_selection(lock: dict[str, Any], package_root: pathlib.Path) -> dict[str, Any]:
    validate_lock(lock)
    identity = package_tree_identity(package_root)
    if identity != {key: lock["package"][key] for key in ("tree_sha256", "file_count", "total_bytes")}:
        raise ValueError("package tree identity mismatch")
    source = lock["source"]
    package = lock["package"]
    return {
        "schema_version": SCHEMA_VERSION,
        "kind": "pure-python-package",
        "profile": PROFILE,
        "package": {
            "name": package["name"],
            "version": source["version"],
            "status": package["status"],
            "import_root": package["import_root"],
            "install_path": package["install_path"],
            "repository_license_id": source["license"],
            "source_commit": source["source_commit"],
            "source_archive_sha256": source["sha256"],
            "patch_sha256": source["patch_sha256"],
            **identity,
        },
    }


def _expected_selection_package(lock: dict[str, Any]) -> dict[str, Any]:
    source = lock["source"]
    package = lock["package"]
    return {
        "name": package["name"], "version": source["version"], "status": package["status"],
        "import_root": package["import_root"], "install_path": package["install_path"],
        "repository_license_id": source["license"], "source_commit": source["source_commit"],
        "source_archive_sha256": source["sha256"], "patch_sha256": source["patch_sha256"],
        "tree_sha256": package["tree_sha256"], "file_count": package["file_count"], "total_bytes": package["total_bytes"],
    }


def validate_selection(value: Any, expected_lock: dict[str, Any] | None = None) -> dict[str, Any]:
    selection = _exact_fields(value, {"schema_version", "kind", "profile", "package"}, "extension selection")
    if selection["schema_version"] != SCHEMA_VERSION or selection["kind"] != "pure-python-package" or selection["profile"] != PROFILE:
        raise ValueError("invalid extension selection identity")
    package = _exact_fields(
        selection["package"],
        {
            "name", "version", "status", "import_root", "install_path", "repository_license_id",
            "source_commit", "source_archive_sha256", "patch_sha256", "tree_sha256", "file_count", "total_bytes",
        },
        "extension selection package",
    )
    lock = expected_lock if expected_lock is not None else load_lock(PROFILE_LOCK)
    validate_lock(lock)
    if package != _expected_selection_package(lock):
        raise ValueError("invalid extension selection package identity")
    return selection


def load_selection(path: pathlib.Path) -> dict[str, Any]:
    return validate_selection(strict_json_loads(path.read_text()))


def source_lock_projection(extension: dict[str, Any]) -> dict[str, Any]:
    validate_lock(extension)
    source = extension["source"]
    public_source = {
        key: source[key]
        for key in ("id", "version", "url", "sha256", "license", "role", "artifact_relation")
    }
    return {"schema_version": 1, "target": "wasm32-wasip1", "sources": [public_source]}


def merge_source_lock(base: Any, extension: dict[str, Any]) -> dict[str, Any]:
    validate_lock(extension)
    base_lock = _exact_fields(base, {"schema_version", "target", "sources"}, "base source lock")
    if base_lock["schema_version"] != 1 or base_lock["target"] != "wasm32-wasip1" or not isinstance(base_lock["sources"], list):
        raise ValueError("invalid base source lock")
    source_ids = [row.get("id") for row in base_lock["sources"] if isinstance(row, dict)]
    if len(source_ids) != len(base_lock["sources"]) or len(set(source_ids)) != len(source_ids):
        raise ValueError("base source lock IDs are invalid")
    projection = source_lock_projection(extension)
    source = projection["sources"][0]
    if source["id"] in source_ids:
        raise ValueError("extension source already exists in base source lock")
    return {"schema_version": 1, "target": "wasm32-wasip1", "sources": [*base_lock["sources"], source]}


def write_json(path: pathlib.Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    patch_parser = subparsers.add_parser("verify-patch")
    patch_parser.add_argument("--lock", type=pathlib.Path, required=True)
    patch_parser.add_argument("--patch", type=pathlib.Path, required=True)
    tree_parser = subparsers.add_parser("verify-tree")
    tree_parser.add_argument("--lock", type=pathlib.Path, required=True)
    tree_parser.add_argument("--package-root", type=pathlib.Path, required=True)
    source_lock_parser = subparsers.add_parser("source-lock")
    source_lock_parser.add_argument("--lock", type=pathlib.Path, required=True)
    source_lock_parser.add_argument("--output", type=pathlib.Path, required=True)
    effective_parser = subparsers.add_parser("effective-source-lock")
    effective_parser.add_argument("--lock", type=pathlib.Path, required=True)
    effective_parser.add_argument("--source-lock", type=pathlib.Path, required=True)
    effective_parser.add_argument("--output", type=pathlib.Path, required=True)
    prepare_parser = subparsers.add_parser("prepare")
    prepare_parser.add_argument("--lock", type=pathlib.Path, required=True)
    prepare_parser.add_argument("--package-root", type=pathlib.Path, required=True)
    prepare_parser.add_argument("--source-lock", type=pathlib.Path, required=True)
    prepare_parser.add_argument("--selection-output", type=pathlib.Path, required=True)
    prepare_parser.add_argument("--effective-source-lock-output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    lock = load_lock(args.lock)
    if args.command == "verify-patch":
        verify_patch(lock, args.patch)
        return 0
    if args.command == "verify-tree":
        build_selection(lock, args.package_root)
        return 0
    if args.command == "source-lock":
        write_json(args.output, source_lock_projection(lock))
        return 0
    if args.command == "effective-source-lock":
        base = strict_json_loads(args.source_lock.read_text())
        write_json(args.output, merge_source_lock(base, lock))
        return 0
    selection = build_selection(lock, args.package_root)
    base = strict_json_loads(args.source_lock.read_text())
    effective = merge_source_lock(base, lock)
    write_json(args.selection_output, selection)
    write_json(args.effective_source_lock_output, effective)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
