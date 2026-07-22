#!/usr/bin/env python3
"""Validate immutable, licensed build-source declarations."""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import sys
from typing import Any

SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
ALLOWED_ROLES = {"runtime-source", "toolchain", "build-tool", "link-library"}
REQUIRED_SOURCE_FIELDS = {"id", "version", "url", "sha256", "license", "role"}


def validate_lock(document: Any) -> list[str]:
    errors: list[str] = []
    if not isinstance(document, dict):
        return ["lock must be a JSON object"]

    if document.get("schema_version") != 1:
        errors.append("schema_version must equal 1")
    if document.get("target") != "wasm32-wasip1":
        errors.append("target must equal wasm32-wasip1")

    sources = document.get("sources")
    if not isinstance(sources, list) or not sources:
        errors.append("sources must be a non-empty array")
        return errors

    seen: set[str] = set()
    for index, source in enumerate(sources):
        prefix = f"sources[{index}]"
        if not isinstance(source, dict):
            errors.append(f"{prefix} must be an object")
            continue

        missing = sorted(REQUIRED_SOURCE_FIELDS - source.keys())
        if missing:
            errors.append(f"{prefix} missing required fields: {', '.join(missing)}")

        source_id = source.get("id")
        if not isinstance(source_id, str) or not source_id:
            errors.append(f"{prefix}.id must be a non-empty string")
        elif source_id in seen:
            errors.append(f"duplicate source id: {source_id}")
        else:
            seen.add(source_id)

        url = source.get("url")
        if not isinstance(url, str) or not url.startswith("https://"):
            errors.append(f"{prefix}.url must use https")
        elif re.search(r"(?:/|[?=&])latest(?:/|$|[?=&])", url, re.IGNORECASE):
            errors.append(f"{prefix}.url is mutable: latest is forbidden")

        digest = source.get("sha256")
        if not isinstance(digest, str) or not SHA256_RE.fullmatch(digest):
            errors.append(f"{prefix}.sha256 must be 64 lowercase hex characters")

        license_id = source.get("license")
        if not isinstance(license_id, str) or not license_id.strip():
            errors.append(f"{prefix}.license must be explicit")

        role = source.get("role")
        if role not in ALLOWED_ROLES:
            errors.append(f"{prefix}.role must be one of {sorted(ALLOWED_ROLES)}")

    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "path",
        nargs="?",
        default="guest/build/sources.lock.json",
        type=pathlib.Path,
    )
    args = parser.parse_args()
    try:
        document = json.loads(args.path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        print(f"source lock read failed: {error}", file=sys.stderr)
        return 2

    errors = validate_lock(document)
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    print(json.dumps({"sources": len(document["sources"]), "target": document["target"]}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
