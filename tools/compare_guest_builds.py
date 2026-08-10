#!/usr/bin/env python3
"""Require exact equality between two guest bundles and localize mismatches."""

import argparse
import hashlib
import json
import pathlib
from typing import Any, Dict, List, Optional, Tuple


REQUIRED_FILES = {
    "agent-python-runtime.wasm",
    "manifest.json",
    "import-inventory.json",
    "import-qualification.json",
    "SHA256SUMS",
    "sbom.spdx.json",
    "THIRD_PARTY_NOTICES.md",
}


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_uleb(data: bytes, offset: int) -> Tuple[int, int]:
    value = 0
    shift = 0
    for _ in range(10):
        if offset >= len(data):
            raise ValueError("truncated unsigned LEB128")
        byte = data[offset]
        offset += 1
        value |= (byte & 0x7F) << shift
        if byte & 0x80 == 0:
            return value, offset
        shift += 7
    raise ValueError("unsigned LEB128 is too long")


def wasm_sections(path: pathlib.Path) -> List[Dict[str, Any]]:
    data = path.read_bytes()
    if len(data) < 8 or data[:4] != b"\x00asm" or data[4:8] != b"\x01\x00\x00\x00":
        raise ValueError("invalid core Wasm header")
    rows = []
    offset = 8
    index = 0
    while offset < len(data):
        section_id = data[offset]
        offset += 1
        size, payload_offset = read_uleb(data, offset)
        payload_end = payload_offset + size
        if payload_end > len(data):
            raise ValueError("truncated Wasm section")
        payload = data[payload_offset:payload_end]
        name: Optional[str] = None
        if section_id == 0:
            name_size, name_offset = read_uleb(payload, 0)
            name_end = name_offset + name_size
            if name_end > len(payload):
                raise ValueError("truncated Wasm custom-section name")
            name = payload[name_offset:name_end].decode("utf-8", errors="replace")
        rows.append(
            {
                "index": index,
                "section_id": section_id,
                "custom_name": name,
                "size_bytes": size,
                "sha256": hashlib.sha256(payload).hexdigest(),
            }
        )
        index += 1
        offset = payload_end
    return rows


def manifest_differences(left: Any, right: Any, pointer: str = "") -> List[Dict[str, Any]]:
    if type(left) is not type(right):
        return [{"pointer": pointer or "/", "left": left, "right": right}]
    if isinstance(left, dict):
        rows = []
        for key in sorted(set(left) | set(right)):
            child = f"{pointer}/{str(key).replace('~', '~0').replace('/', '~1')}"
            if key not in left or key not in right:
                rows.append({"pointer": child, "left": left.get(key), "right": right.get(key)})
            else:
                rows.extend(manifest_differences(left[key], right[key], child))
        return rows
    if isinstance(left, list):
        rows = []
        for index in range(max(len(left), len(right))):
            child = f"{pointer}/{index}"
            if index >= len(left) or index >= len(right):
                rows.append(
                    {
                        "pointer": child,
                        "left": left[index] if index < len(left) else None,
                        "right": right[index] if index < len(right) else None,
                    }
                )
            else:
                rows.extend(manifest_differences(left[index], right[index], child))
        return rows
    if left != right:
        return [{"pointer": pointer or "/", "left": left, "right": right}]
    return []


def _file_inventory(directory: pathlib.Path) -> Dict[str, pathlib.Path]:
    return {
        path.relative_to(directory).as_posix(): path
        for path in sorted(directory.rglob("*"))
        if path.is_file()
    }


def _section_comparison(left_path: pathlib.Path, right_path: pathlib.Path) -> List[Dict[str, Any]]:
    left = wasm_sections(left_path)
    right = wasm_sections(right_path)
    rows = []
    for index in range(max(len(left), len(right))):
        left_row = left[index] if index < len(left) else None
        right_row = right[index] if index < len(right) else None
        identity = left_row or right_row
        rows.append(
            {
                "index": index,
                "section_id": identity["section_id"],
                "custom_name": identity["custom_name"],
                "left_size_bytes": left_row["size_bytes"] if left_row else None,
                "right_size_bytes": right_row["size_bytes"] if right_row else None,
                "left_sha256": left_row["sha256"] if left_row else None,
                "right_sha256": right_row["sha256"] if right_row else None,
                "match": left_row == right_row,
            }
        )
    return rows


def compare_directories(left_dir: pathlib.Path, right_dir: pathlib.Path) -> Dict[str, Any]:
    left_files = _file_inventory(left_dir)
    right_files = _file_inventory(right_dir)
    all_paths = sorted(set(left_files) | set(right_files))
    files = []
    for relative in all_paths:
        left_path = left_files.get(relative)
        right_path = right_files.get(relative)
        left_digest = sha256(left_path) if left_path else None
        right_digest = sha256(right_path) if right_path else None
        files.append(
            {
                "path": relative,
                "left_size_bytes": left_path.stat().st_size if left_path else None,
                "right_size_bytes": right_path.stat().st_size if right_path else None,
                "left_sha256": left_digest,
                "right_sha256": right_digest,
                "match": left_digest is not None and left_digest == right_digest,
            }
        )

    errors = []
    missing_left = sorted(REQUIRED_FILES - set(left_files))
    missing_right = sorted(REQUIRED_FILES - set(right_files))
    if missing_left:
        errors.append(f"left bundle missing required files: {missing_left}")
    if missing_right:
        errors.append(f"right bundle missing required files: {missing_right}")

    left_manifest = json.loads((left_dir / "manifest.json").read_text()) if not missing_left else {}
    right_manifest = json.loads((right_dir / "manifest.json").read_text()) if not missing_right else {}
    differences = manifest_differences(left_manifest, right_manifest)
    source_epoch = None
    if left_manifest and right_manifest:
        for label, directory, manifest in (
            ("left", left_dir, left_manifest),
            ("right", right_dir, right_manifest),
        ):
            artifact = manifest.get("artifact", {})
            artifact_path = directory / str(artifact.get("filename", ""))
            if not artifact_path.is_file() or artifact.get("sha256") != sha256(artifact_path):
                errors.append(f"{label} manifest artifact digest is invalid")
            if artifact_path.is_file() and artifact.get("size") != artifact_path.stat().st_size:
                errors.append(f"{label} manifest artifact size is invalid")
        left_build = left_manifest.get("build", {})
        right_build = right_manifest.get("build", {})
        candidate = left_build.get("source_date_epoch")
        if (
            candidate not in (None, "", "unknown")
            and candidate == right_build.get("source_date_epoch")
            and left_build.get("repository_commit") == right_build.get("repository_commit")
        ):
            source_epoch = candidate
        else:
            errors.append("build commit or SOURCE_DATE_EPOCH mismatch")

    wasm_rows = []
    left_wasm = left_dir / "agent-python-runtime.wasm"
    right_wasm = right_dir / "agent-python-runtime.wasm"
    if left_wasm.is_file() and right_wasm.is_file():
        try:
            wasm_rows = _section_comparison(left_wasm, right_wasm)
        except ValueError as error:
            errors.append(f"Wasm section parse failed: {error}")

    exact = (
        not errors
        and not differences
        and set(left_files) == set(right_files)
        and all(row["match"] for row in files)
    )
    return {
        "schema_version": 1,
        "comparison_type": "exact-guest-bundle-reproducibility",
        "exact_match": exact,
        "source_date_epoch": source_epoch,
        "validation_errors": errors,
        "files": files,
        "manifest_differences": differences,
        "wasm_sections": wasm_rows,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("left", type=pathlib.Path)
    parser.add_argument("right", type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    report = compare_directories(args.left, args.right)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return 0 if report["exact_match"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
