#!/usr/bin/env python3
"""Resolve closed repository-declared Guest package profiles."""

from __future__ import annotations

import argparse
import importlib.util
import json
import pathlib
import re
from typing import Any

ROOT = pathlib.Path(__file__).resolve().parent
REGISTRY_PATH = ROOT / "profiles" / "registry.v1.json"
PROFILE_FIELDS = {"id", "artifact_filename", "kind", "lock", "recipe", "required_import_roots", "private_inputs"}
REGISTRY_FIELDS = {"schema_version", "target", "profiles"}
PROFILE_ID = re.compile(r"^[a-z][a-z0-9-]{0,63}$")
FILENAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
KINDS = {"base", "pure-python-package", "static-native-package"}
PRIVATE_INPUTS = {"extension_patch"}


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


def _safe_filename(value: Any, *, nullable: bool = False) -> str | None:
    if value is None and nullable:
        return None
    if not isinstance(value, str) or FILENAME.fullmatch(value) is None or pathlib.PurePosixPath(value).name != value:
        raise ValueError("invalid profile path")
    return value


def _sorted_unique_strings(value: Any, label: str) -> list[str]:
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value) or value != sorted(set(value)):
        raise ValueError(f"{label} must be sorted unique strings")
    return value


def validate_registry(value: Any) -> dict[str, Any]:
    registry = _exact_fields(value, REGISTRY_FIELDS, "package profile registry")
    if registry["schema_version"] != 1 or registry["target"] != "wasm32-wasip1" or not isinstance(registry["profiles"], list) or not registry["profiles"]:
        raise ValueError("invalid package profile registry identity")
    ids: list[str] = []
    for index, raw in enumerate(registry["profiles"]):
        profile = _exact_fields(raw, PROFILE_FIELDS, "package profile")
        profile_id = profile["id"]
        if not isinstance(profile_id, str) or PROFILE_ID.fullmatch(profile_id) is None:
            raise ValueError("invalid package profile ID")
        ids.append(profile_id)
        _safe_filename(profile["artifact_filename"])
        kind = profile["kind"]
        if kind not in KINDS:
            raise ValueError("invalid package profile kind")
        lock = _safe_filename(profile["lock"], nullable=True)
        recipe = profile["recipe"]
        if recipe is not None and (not isinstance(recipe, str) or PROFILE_ID.fullmatch(recipe) is None):
            raise ValueError("invalid package profile recipe")
        roots = _sorted_unique_strings(profile["required_import_roots"], "required import roots")
        if any(not root.isidentifier() or root.startswith("_") for root in roots):
            raise ValueError("invalid required import root")
        private_inputs = _sorted_unique_strings(profile["private_inputs"], "private inputs")
        if not set(private_inputs).issubset(PRIVATE_INPUTS):
            raise ValueError("unknown private profile input")
        if index == 0:
            if profile_id != "base" or kind != "base" or lock is not None or recipe is not None or private_inputs:
                raise ValueError("base package profile is invalid")
        elif kind == "base" or lock is None or recipe is None:
            raise ValueError("package-bearing profile is incomplete")
    if ids[0] != "base" or ids[1:] != sorted(set(ids[1:])) or len(set(ids)) != len(ids):
        raise ValueError("package profile IDs must be base-first sorted unique")
    return registry


def load_registry(path: pathlib.Path = REGISTRY_PATH) -> dict[str, Any]:
    return validate_registry(strict_json_loads(path.read_text()))


def profile_ids(registry: dict[str, Any]) -> tuple[str, ...]:
    validate_registry(registry)
    return tuple(profile["id"] for profile in registry["profiles"])


def resolve_profile(registry: dict[str, Any], profile_id: str) -> dict[str, Any]:
    validate_registry(registry)
    matches = [profile for profile in registry["profiles"] if profile["id"] == profile_id]
    if len(matches) != 1:
        raise ValueError(f"unsupported package profile: {profile_id}")
    return dict(matches[0])


def _load_extension_profile_module():
    path = ROOT / "extension_profile.py"
    spec = importlib.util.spec_from_file_location("legacy_attrs_extension_profile", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load legacy attrs profile validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _load_native_package_profile_module():
    path = ROOT / "native_package_profile.py"
    spec = importlib.util.spec_from_file_location("static_native_package_profile", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load native package profile validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_package_contract(profile: dict[str, Any]) -> dict[str, Any] | None:
    registry = load_registry()
    profile_id = profile.get("id") if isinstance(profile, dict) else None
    if not isinstance(profile_id, str):
        raise ValueError("package profile does not match registry")
    resolved = resolve_profile(registry, profile_id)
    if resolved != profile:
        raise ValueError("package profile does not match registry")
    if resolved["kind"] == "base":
        return None
    lock_path = ROOT / "profiles" / resolved["lock"]
    if resolved["recipe"] == "attrs-770-v1":
        return _load_extension_profile_module().load_lock(lock_path)
    if resolved["recipe"] == "numpy-static-v1":
        return _load_native_package_profile_module().load_lock(lock_path)
    raise ValueError("package profile recipe is unsupported")


def source_lock_projection(profile: dict[str, Any], contract: dict[str, Any] | None) -> dict[str, Any]:
    if profile["kind"] == "base":
        if contract is not None:
            raise ValueError("base profile has a package contract")
        return {"schema_version": 1, "target": "wasm32-wasip1", "sources": []}
    if profile["recipe"] == "attrs-770-v1" and contract is not None:
        return _load_extension_profile_module().source_lock_projection(contract)
    if profile["recipe"] == "numpy-static-v1" and contract is not None:
        return _load_native_package_profile_module().source_lock_projection(contract)
    raise ValueError("package source projection is unsupported")


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    field_parser = subparsers.add_parser("field")
    field_parser.add_argument("--profile", required=True)
    field_parser.add_argument("--name", choices=["artifact_filename", "kind", "lock", "recipe"], required=True)
    list_parser = subparsers.add_parser("list")
    list_parser.add_argument("--json", action="store_true")
    validate_parser = subparsers.add_parser("validate")
    validate_parser.add_argument("--registry", type=pathlib.Path, default=REGISTRY_PATH)
    args = parser.parse_args()
    registry = load_registry(args.registry) if args.command == "validate" else load_registry()
    if args.command == "validate":
        return 0
    if args.command == "list":
        ids = profile_ids(registry)
        print(json.dumps(ids) if args.json else "\n".join(ids))
        return 0
    profile = resolve_profile(registry, args.profile)
    value = profile[args.name]
    print("" if value is None else value)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
