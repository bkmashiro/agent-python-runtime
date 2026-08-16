#!/usr/bin/env python3
"""Freeze one exact Open-SWE trajectory as a natural placement control."""

import argparse
import hashlib
import json
import pathlib
import stat
from collections import Counter
from typing import Any

SCHEMA = "pysolate.natural-placement-request.v1"
SOURCE_SCHEMA = "pysolate.natural-corpus-download.v1"
DATASET = "nvidia/Open-SWE-Traces"
REVISION = "ad4805a5aa7de70d99cab0bb8f99b15304c76de0"
SOURCE_SHA = "sha256:4b91b39f54849bac8323a468b67fbed6065535dba004731c99ea21cb6345de1e"
RECORD_SHA = "sha256:607d9fb4f9ebfe90ba654f85a5d012ba1ef3f845cf2d12ed53c022686d795a4d"
EXPECTED_TOOLS = {"execute_bash": 31, "finish": 1, "str_replace_editor": 15}
REQUIREMENTS = ["shell", "subprocess"]


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def build(source_root: pathlib.Path) -> dict[str, Any]:
    if not source_root.is_dir() or stat.S_IMODE(source_root.stat().st_mode) & 0o077:
        raise ValueError("private source root must be mode 0700")
    manifest = json.loads((source_root / "download-manifest.json").read_text())
    if manifest.get("schema_version") != SOURCE_SCHEMA or not isinstance(manifest.get("sources"), list):
        raise ValueError("invalid source manifest")
    descriptors = [item for item in manifest["sources"] if item.get("dataset") == DATASET]
    if len(descriptors) != 1:
        raise ValueError("exact Open-SWE source missing")
    descriptor = descriptors[0]
    expected_descriptor = {
        "dataset": DATASET, "config": "openhands", "split": "minimax_m25", "offset": 0, "length": 10,
        "bytes": 2542745, "sha256": SOURCE_SHA, "dataset_revision_observed": REVISION, "license_id": "cc-by-4.0",
        "file": "open-swe-10.json",
    }
    for key, value in expected_descriptor.items():
        if descriptor.get(key) != value:
            raise ValueError(f"source descriptor mismatch: {key}")
    raw = (source_root / descriptor["file"]).read_bytes()
    if len(raw) != descriptor["bytes"] or digest(raw) != SOURCE_SHA:
        raise ValueError("source body identity mismatch")
    rows = json.loads(raw).get("rows")
    if not isinstance(rows, list) or len(rows) != 10:
        raise ValueError("source row count mismatch")
    matches = []
    for outer in rows:
        row = outer.get("row", {})
        record_sha = digest(str(row.get("instance_id", "")).encode())
        if record_sha == RECORD_SHA:
            matches.append((outer, row))
    if len(matches) != 1:
        raise ValueError("exact placement record missing")
    outer, row = matches[0]
    trajectory = row.get("trajectory")
    if outer.get("truncated_cells") or row.get("language") != "python" or row.get("resolved") != 1 or not isinstance(trajectory, list):
        raise ValueError("placement record contract mismatch")
    counts: Counter[str] = Counter()
    for message in trajectory:
        if not isinstance(message, dict):
            raise ValueError("invalid trajectory message")
        for tool_call in message.get("tool_calls") or []:
            function = tool_call.get("function") or {}
            name = function.get("name") or tool_call.get("name") or tool_call.get("type")
            if not isinstance(name, str):
                raise ValueError("invalid tool name")
            counts[name] += 1
    if dict(sorted(counts.items())) != EXPECTED_TOOLS:
        raise ValueError("placement tool surface mismatch")
    return {
        "schema_version": SCHEMA,
        "source": {"dataset": DATASET, "revision": REVISION, "license_id": "cc-by-4.0", "source_sha256": SOURCE_SHA},
        "task": {
            "record_sha256": RECORD_SHA, "record_body_sha256": digest(canonical(row)),
            "trajectory_sha256": digest(canonical(trajectory)), "language": "python", "resolved": 1,
            "trajectory_messages": len(trajectory), "tool_name_counts": EXPECTED_TOOLS,
        },
        "placement_contract": {
            "required_features": REQUIREMENTS, "mutable_workspace_observed": True,
            "expected_backend": "native_sandbox", "expected_reason": "required_native_feature",
            "pysolate_guest_calls": 0,
        },
        "private_bodies_included": False,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    body = canonical(build(args.source_root)) + b"\n"
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(body)
    args.output.chmod(0o600)
    print(hashlib.sha256(body).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
