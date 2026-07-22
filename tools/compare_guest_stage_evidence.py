#!/usr/bin/env python3
"""Validate and compare retained guest build-stage evidence."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any


RETAINED_ROLES = {
    "raw_wasm",
    "final_wasm",
    "patched_wasi_vfs_archive",
    "linked_storage_object",
    "source_lock",
    "vfs_manifest",
}
PACK_INPUT_ROLES = {"wasi_vfs_cli", "vfs_manifest", "source_lock"}


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_and_validate(directory: pathlib.Path, label: str) -> tuple[dict[str, Any], list[str]]:
    errors = []
    report_path = directory / "stage-evidence.json"
    try:
        report = json.loads(report_path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        return {}, [f"{label} stage report is unreadable: {error}"]
    if report.get("schema_version") != 1 or report.get("evidence_type") != "guest-reproducibility-stage-identities":
        errors.append(f"{label} stage report schema/type is invalid")
    files = report.get("files")
    if not isinstance(files, dict):
        return report, errors + [f"{label} stage report files must be an object"]
    required = RETAINED_ROLES | {"wasi_vfs_cli", "source_lock"}
    missing = sorted(required - set(files))
    if missing:
        errors.append(f"{label} stage report missing file roles: {missing}")
    for role in sorted(RETAINED_ROLES & set(files)):
        record = files.get(role)
        if not isinstance(record, dict):
            errors.append(f"{label} {role} identity must be an object")
            continue
        relative = record.get("path")
        if not isinstance(relative, str) or pathlib.PurePosixPath(relative).name != relative:
            errors.append(f"{label} {role} retained path is invalid")
            continue
        path = directory / relative
        if not path.is_file() or path.is_symlink():
            errors.append(f"{label} {role} retained file is missing")
            continue
        expected_size = record.get("size_bytes")
        expected_digest = record.get("sha256")
        if record.get("retained") is not True:
            errors.append(f"{label} {role} must be marked retained")
        if expected_size != path.stat().st_size or expected_digest != sha256(path):
            errors.append(f"{label} {role} retained identity mismatch")
    identity = report.get("build_identity")
    if not isinstance(identity, dict):
        errors.append(f"{label} build identity is missing")
    else:
        commit = identity.get("repository_commit")
        epoch = identity.get("source_date_epoch")
        if not isinstance(commit, str) or re.fullmatch(r"[0-9a-f]{40}", commit) is None:
            errors.append(f"{label} repository commit is invalid")
        if not isinstance(epoch, str) or not epoch.isdigit() or int(epoch) <= 0:
            errors.append(f"{label} SOURCE_DATE_EPOCH is invalid")
    return report, errors


def _digest(report: dict[str, Any], role: str) -> Any:
    files = report.get("files", {})
    record = files.get(role, {}) if isinstance(files, dict) else {}
    return record.get("sha256") if isinstance(record, dict) else None


def compare_stage_evidence(left_dir: pathlib.Path, right_dir: pathlib.Path) -> dict[str, Any]:
    left_dir = pathlib.Path(left_dir)
    right_dir = pathlib.Path(right_dir)
    left, errors = read_and_validate(left_dir, "left")
    right, right_errors = read_and_validate(right_dir, "right")
    errors.extend(right_errors)

    left_identity = left.get("build_identity", {}) if isinstance(left, dict) else {}
    right_identity = right.get("build_identity", {}) if isinstance(right, dict) else {}
    for field in ("repository_commit", "source_date_epoch"):
        if left_identity.get(field) != right_identity.get(field):
            errors.append(f"build identity mismatch: {field}")

    raw_match = _digest(left, "raw_wasm") is not None and _digest(left, "raw_wasm") == _digest(right, "raw_wasm")
    final_match = _digest(left, "final_wasm") is not None and _digest(left, "final_wasm") == _digest(right, "final_wasm")
    pack_input_matches = {
        role: _digest(left, role) is not None and _digest(left, role) == _digest(right, role)
        for role in sorted(PACK_INPUT_ROLES)
    }

    if errors:
        outcome = "inconclusive due to missing or invalid evidence"
    elif not raw_match:
        outcome = "raw-stage drift established"
    elif final_match:
        outcome = "no raw/final Wasm drift observed"
    elif not all(pack_input_matches.values()):
        outcome = "inconclusive due to mismatched pack inputs"
    else:
        outcome = "pack-stage drift established"

    return {
        "schema_version": 1,
        "comparison_type": "guest-reproducibility-stage-comparison",
        "outcome": outcome,
        "raw_wasm_match": raw_match,
        "final_wasm_match": final_match,
        "pack_input_matches": pack_input_matches,
        "left_build_identity": left_identity,
        "right_build_identity": right_identity,
        "stage_hashes": {
            role: {"left": _digest(left, role), "right": _digest(right, role)}
            for role in sorted((RETAINED_ROLES | PACK_INPUT_ROLES) - {"vfs_manifest"})
        }
        | {
            "vfs_manifest": {
                "left": _digest(left, "vfs_manifest"),
                "right": _digest(right, "vfs_manifest"),
            }
        },
        "validation_errors": errors,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("left", type=pathlib.Path)
    parser.add_argument("right", type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    report = compare_stage_evidence(args.left, args.right)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    print(json.dumps({
        "outcome": report["outcome"],
        "raw_wasm_match": report["raw_wasm_match"],
        "final_wasm_match": report["final_wasm_match"],
        "validation_errors": report["validation_errors"],
    }, sort_keys=True))
    return 1 if report["validation_errors"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
