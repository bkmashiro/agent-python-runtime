#!/usr/bin/env python3
"""Independent validator for frozen Phase 4 semantic-speculation contracts."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

PHASE3_ID = "sha256:f69c31c874d56b7563942bf889a798ed16b38a657fef18be90d4251f49fbee3f"
MATRIX_ID = "sha256:4cec92655c0f73578f96dc352be13e17aff3376645830ff89f0292e01d15af39"
PREREG_ID = "sha256:d17a78fa49fd8699f2d7ae3ec4f183e6e05e50a18d868f8fe54b26b87899676e"


def reject_duplicates(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def load_canonical(path: Path) -> tuple[dict[str, object], bytes]:
    raw = path.read_bytes()
    value = json.loads(raw, object_pairs_hook=reject_duplicates)
    canonical = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
    if raw != canonical:
        raise ValueError(f"non-canonical JSON: {path}")
    return value, raw


def verify_identity(value: dict[str, object], expected: str, label: str, domain: bytes = b"") -> None:
    if value.get("identity") != expected:
        raise ValueError(f"{label} identity mismatch")
    unsigned = dict(value)
    unsigned["identity"] = ""
    digest = "sha256:" + hashlib.sha256(
        domain + json.dumps(unsigned, ensure_ascii=False, separators=(",", ":")).encode()
    ).hexdigest()
    if digest != expected:
        raise ValueError(f"{label} content digest mismatch: {digest}")


def walk_keys(value: object) -> set[str]:
    if isinstance(value, dict):
        keys = set(value)
        for child in value.values():
            keys.update(walk_keys(child))
        return keys
    if isinstance(value, list):
        keys: set[str] = set()
        for child in value:
            keys.update(walk_keys(child))
        return keys
    return set()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    evidence = args.root / "docs" / "evidence"
    phase3, _ = load_canonical(evidence / "semantic-speculation-synthetic-case-matrix-v1.json")
    matrix, matrix_raw = load_canonical(evidence / "semantic-speculation-phase4-extension-matrix-v1.json")
    prereg, prereg_raw = load_canonical(evidence / "semantic-speculation-phase4-preregistration-v1.json")

    verify_identity(phase3, PHASE3_ID, "phase 3 matrix", b"pysolate.semantic-speculation-synthetic-case-matrix.v1\x00")
    verify_identity(matrix, MATRIX_ID, "phase 4 matrix")
    verify_identity(prereg, PREREG_ID, "phase 4 preregistration")
    if matrix.get("parent_phase3_matrix_identity") != PHASE3_ID or prereg.get("parent_phase3_matrix_identity") != PHASE3_ID:
        raise ValueError("phase 3 parent binding mismatch")
    if prereg.get("extension_matrix_identity") != MATRIX_ID:
        raise ValueError("extension matrix binding mismatch")
    coordinates = matrix.get("coordinates")
    if not isinstance(coordinates, list) or len(coordinates) != 12:
        raise ValueError("unexpected coordinate count")
    if matrix.get("profiles") != ["cold_end_to_end", "preprovisioned_equivalent_capacity"]:
        raise ValueError("profile set mismatch")
    forbidden = {"source", "source_body", "request", "response", "result"}
    leaked = forbidden.intersection(walk_keys(matrix) | walk_keys(prereg))
    if leaked:
        raise ValueError(f"body-bearing keys present: {sorted(leaked)}")

    print(json.dumps({
        "status": "passed",
        "phase3_identity": PHASE3_ID,
        "extension_matrix_identity": MATRIX_ID,
        "preregistration_identity": PREREG_ID,
        "coordinates": len(coordinates),
        "matrix_sha256": hashlib.sha256(matrix_raw).hexdigest(),
        "preregistration_sha256": hashlib.sha256(prereg_raw).hexdigest(),
    }, sort_keys=True))


if __name__ == "__main__":
    main()
