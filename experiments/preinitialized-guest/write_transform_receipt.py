#!/usr/bin/env python3
"""Bind two Wizer transforms to the exact input and record repeat determinism."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import tempfile

_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_REVISION = re.compile(r"^[0-9a-f]{40}$")


def artifact_record(path: pathlib.Path) -> dict[str, object]:
    data = path.read_bytes()
    if not data.startswith(b"\x00asm"):
        raise ValueError(f"{path} is not a WebAssembly module")
    return {
        "filename": path.name,
        "size_bytes": len(data),
        "sha256": hashlib.sha256(data).hexdigest(),
    }


def build_receipt(
    *,
    input_path: pathlib.Path,
    first_path: pathlib.Path,
    second_path: pathlib.Path,
    tool_version: str,
    host_revision: str,
) -> dict[str, object]:
    if not _REVISION.fullmatch(host_revision):
        raise ValueError("host revision must be a 40-character lowercase Git SHA")
    version = tool_version.strip()
    if "44.0.1" not in version or "wasmtime" not in version.lower():
        raise ValueError("spike requires locked Wasmtime 44.0.1")

    source = artifact_record(input_path)
    candidate = artifact_record(first_path)
    repeat = artifact_record(second_path)
    for row in (source, candidate, repeat):
        if not _SHA256.fullmatch(str(row["sha256"])):
            raise ValueError("invalid artifact digest")
    return {
        "schema_version": 1,
        "host_revision": host_revision,
        "tool": {"name": "wasmtime-wizer", "version": version},
        "input": source,
        "candidate": candidate,
        "repeat_candidate": repeat,
        "repeat_deterministic": candidate["sha256"] == repeat["sha256"],
        "transform": {
            "init_func": "runtime_preinitialize",
            "function_rename": "_initialize=runtime_preinitialized_initialize",
            "wasi_cli": True,
            "host_call_stub": "fail-closed",
            "reactor_initialization": "wizer-owned",
            "python_hash_seed": "fixed-experiment-only:0xa9e17f5d",
        },
    }


def write_json_atomic(path: pathlib.Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    encoded = json.dumps(value, indent=2, sort_keys=True) + "\n"
    with tempfile.NamedTemporaryFile("w", dir=path.parent, delete=False) as handle:
        handle.write(encoded)
        temporary = pathlib.Path(handle.name)
    temporary.replace(path)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True, type=pathlib.Path)
    parser.add_argument("--candidate", required=True, type=pathlib.Path)
    parser.add_argument("--repeat-candidate", required=True, type=pathlib.Path)
    parser.add_argument("--tool-version", required=True)
    parser.add_argument("--host-revision", required=True)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    receipt = build_receipt(
        input_path=args.input,
        first_path=args.candidate,
        second_path=args.repeat_candidate,
        tool_version=args.tool_version,
        host_revision=args.host_revision,
    )
    write_json_atomic(args.output, receipt)
    print(json.dumps({"output": str(args.output), "repeat_deterministic": receipt["repeat_deterministic"]}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
