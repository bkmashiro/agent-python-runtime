#!/usr/bin/env python3
"""Generate and validate target-Guest import inventory evidence."""

from __future__ import annotations

import argparse
import importlib.util
import json
import pathlib
from typing import Any

PROBE_ID = "guest-importlib-find-spec-v1"
MAX_ROOTS = 1024


def _load_package_profile_module():
    path = pathlib.Path(__file__).with_name("package_profile.py")
    spec = importlib.util.spec_from_file_location("package_profile_inventory", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load package profile registry")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


PACKAGE_PROFILE = _load_package_profile_module()
PROFILE_REGISTRY = PACKAGE_PROFILE.load_registry()
PROFILES = set(PACKAGE_PROFILE.profile_ids(PROFILE_REGISTRY))

PROBE_CODE = r'''import importlib.util
import pkgutil
import sys

probe_id = "guest-importlib-find-spec-v1"
candidates = set(sys.builtin_module_names)
candidates.update(sys.stdlib_module_names)
candidates.update(item.name.partition(".")[0] for item in pkgutil.iter_modules())
roots = []
failures = []
for name in sorted(candidates):
    if not name.isidentifier():
        continue
    try:
        spec = importlib.util.find_spec(name)
    except Exception as exc:
        failures.append({"name": name, "error": type(exc).__name__})
        continue
    if spec is None:
        failures.append({"name": name, "error": "not_found"})
    else:
        roots.append(name)
result = {
    "schema_version": 1,
    "artifact_profile": inputs["artifact_profile"],
    "probe": probe_id,
    "implementation": sys.implementation.name,
    "python_version": ".".join(str(part) for part in sys.version_info[:3]),
    "discoverable_roots": roots,
    "failures": failures,
}
'''


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def strict_json_loads(value: str) -> Any:
    try:
        return json.loads(value, object_pairs_hook=_strict_object)
    except (json.JSONDecodeError, TypeError) as exc:
        raise ValueError("invalid JSON") from exc


def build_request(profile: str) -> dict[str, Any]:
    if profile not in PROFILES:
        raise ValueError("unsupported artifact profile")
    return {
        "run_id": "artifact-import-inventory",
        "code": PROBE_CODE,
        "inputs": {"artifact_profile": profile},
    }


def _valid_root(value: Any) -> bool:
    return isinstance(value, str) and 0 < len(value) <= 128 and value.isidentifier()


def validate_inventory(value: Any, profile: str) -> dict[str, Any]:
    if profile not in PROFILES or not isinstance(value, dict):
        raise ValueError("invalid import inventory")
    expected = {
        "schema_version",
        "artifact_profile",
        "probe",
        "implementation",
        "python_version",
        "discoverable_roots",
        "failures",
    }
    if set(value) != expected:
        raise ValueError("invalid import inventory fields")
    if (
        value["schema_version"] != 1
        or value["artifact_profile"] != profile
        or value["probe"] != PROBE_ID
        or value["implementation"] != "cpython"
        or not isinstance(value["python_version"], str)
        or not 0 < len(value["python_version"]) <= 256
    ):
        raise ValueError("invalid import inventory identity")
    roots = value["discoverable_roots"]
    if (
        not isinstance(roots, list)
        or not 1 <= len(roots) <= MAX_ROOTS
        or any(not _valid_root(root) for root in roots)
        or roots != sorted(set(roots))
    ):
        raise ValueError("discoverable roots must be sorted unique import names")
    required = set(PACKAGE_PROFILE.resolve_profile(PROFILE_REGISTRY, profile)["required_import_roots"])
    if not required.issubset(roots):
        raise ValueError("required profile import root is not discoverable")

    failures = value["failures"]
    if not isinstance(failures, list) or len(failures) > MAX_ROOTS:
        raise ValueError("invalid import inventory failures")
    failure_names: list[str] = []
    for failure in failures:
        if not isinstance(failure, dict) or set(failure) != {"name", "error"}:
            raise ValueError("invalid import inventory failure")
        name, error = failure["name"], failure["error"]
        if not _valid_root(name) or not isinstance(error, str) or not 0 < len(error) <= 128:
            raise ValueError("invalid import inventory failure")
        failure_names.append(name)
    if failure_names != sorted(set(failure_names)) or set(failure_names) & set(roots):
        raise ValueError("inventory failures must be sorted unique and disjoint")
    return value


def extract_inventory(response: Any, profile: str) -> dict[str, Any]:
    if not isinstance(response, dict) or response.get("status") != "ok" or "result" not in response:
        raise ValueError("Guest import inventory probe did not complete")
    return validate_inventory(response["result"], profile)


def load_inventory(path: pathlib.Path, profile: str) -> dict[str, Any]:
    return validate_inventory(strict_json_loads(path.read_text()), profile)


def write_inventory(path: pathlib.Path, inventory: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(inventory, indent=2, sort_keys=True) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    request_parser = subparsers.add_parser("request")
    request_parser.add_argument("--profile", choices=sorted(PROFILES), required=True)
    request_parser.add_argument("--output", type=pathlib.Path, required=True)
    extract_parser = subparsers.add_parser("extract")
    extract_parser.add_argument("--profile", choices=sorted(PROFILES), required=True)
    extract_parser.add_argument("--response", type=pathlib.Path, required=True)
    extract_parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()

    if args.command == "request":
        write_inventory(args.output, build_request(args.profile))
        return 0
    response = strict_json_loads(args.response.read_text())
    write_inventory(args.output, extract_inventory(response, args.profile))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
