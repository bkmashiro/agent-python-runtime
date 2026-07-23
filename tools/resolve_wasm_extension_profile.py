#!/usr/bin/env python3
"""Resolve deterministic static CPython extension feature profiles."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence


MODULE_RE = re.compile(r"^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)+$")
INIT_RE = re.compile(r"^PyInit_[A-Za-z0-9_]+$")
PROFILE_RE = re.compile(r"^[a-z][a-z0-9_-]*$")


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _safe_relative(value: object, label: str) -> Path:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{label} must be a non-empty relative path")
    path = Path(value)
    if path.is_absolute() or ".." in path.parts:
        raise ValueError(f"{label} path must remain relative: {value}")
    return path


def _under(root: Path, relative: Path, label: str) -> Path:
    root = root.resolve()
    path = (root / relative).resolve()
    try:
        path.relative_to(root)
    except ValueError as exc:
        raise ValueError(f"{label} path escapes root: {relative}") from exc
    return path


def _expand_profile(profiles: Dict[str, object], name: str) -> List[Dict[str, str]]:
    ordered: List[Dict[str, str]] = []
    visited = set()
    active: List[str] = []

    def visit(profile_name: str) -> None:
        if profile_name in active:
            chain = " -> ".join(active + [profile_name])
            raise ValueError(f"profile inheritance cycle: {chain}")
        if profile_name in visited:
            return
        profile = profiles.get(profile_name)
        if not isinstance(profile, dict):
            raise ValueError(f"unknown profile: {profile_name}")
        active.append(profile_name)
        parents = profile.get("extends", [])
        if not isinstance(parents, list) or not all(isinstance(item, str) for item in parents):
            raise ValueError(f"profile {profile_name} extends must be a string list")
        for parent in parents:
            visit(parent)
        modules = profile.get("modules", [])
        if not isinstance(modules, list):
            raise ValueError(f"profile {profile_name} modules must be a list")
        for item in modules:
            if not isinstance(item, dict):
                raise ValueError(f"profile {profile_name} module entry must be an object")
            ordered.append(item)
        active.pop()
        visited.add(profile_name)

    visit(name)
    return ordered


def resolve_profile(
    config_path: Path,
    profile_name: str,
    manifest_dir: Path,
    build_root: Path,
) -> Dict[str, Any]:
    if not PROFILE_RE.fullmatch(profile_name):
        raise ValueError(f"invalid profile name: {profile_name}")
    config = json.loads(config_path.read_text())
    if config.get("schema_version") != 1:
        raise ValueError("unsupported feature profile schema")
    package = config.get("package")
    if not isinstance(package, str) or not package:
        raise ValueError("feature profile package must be non-empty")
    profiles = config.get("profiles")
    if not isinstance(profiles, dict):
        raise ValueError("feature profiles must be an object")

    raw_modules = _expand_profile(profiles, profile_name)
    modules: List[Dict[str, object]] = []
    module_names = set()
    extension_archives: List[Path] = []
    static_inputs: List[Path] = []
    seen_extensions = set()
    seen_static_inputs = set()
    manifest_root = manifest_dir.resolve()
    build_root = build_root.resolve()

    for raw in raw_modules:
        module_name = raw.get("module")
        init_symbol = raw.get("init_symbol")
        if not isinstance(module_name, str) or not MODULE_RE.fullmatch(module_name):
            raise ValueError(f"invalid extension module name: {module_name}")
        if module_name in module_names:
            raise ValueError(f"duplicate extension module: {module_name}")
        module_names.add(module_name)
        if not isinstance(init_symbol, str) or not INIT_RE.fullmatch(init_symbol):
            raise ValueError(f"invalid initializer symbol for {module_name}: {init_symbol}")

        manifest_relative = _safe_relative(raw.get("manifest"), "manifest")
        manifest_path = _under(manifest_root, manifest_relative, "manifest")
        if not manifest_path.is_file():
            raise ValueError(f"missing extension manifest: {manifest_relative}")
        manifest = json.loads(manifest_path.read_text())
        if manifest.get("schema_version") != 1 or manifest.get("kind") != "wasm-static-python-extension":
            raise ValueError(f"invalid extension manifest: {manifest_relative}")

        archive_relative = _safe_relative(manifest.get("archive"), "archive")
        expected_archive = manifest_relative.with_suffix(".a")
        if archive_relative != expected_archive:
            raise ValueError(
                f"archive does not match manifest: {archive_relative} != {expected_archive}"
            )
        archive_path = _under(build_root, archive_relative, "archive")
        if not archive_path.is_file():
            raise ValueError(f"missing extension archive: {archive_relative}")

        static_values = manifest.get("static_inputs")
        if not isinstance(static_values, list):
            raise ValueError(f"manifest static_inputs must be a list: {manifest_relative}")
        static_paths: List[Path] = []
        for value in static_values:
            relative = _safe_relative(value, "static input")
            path = _under(build_root, relative, "static input")
            if not path.is_file():
                raise ValueError(f"missing static input: {relative}")
            static_paths.append(path)

        if archive_path not in seen_extensions:
            seen_extensions.add(archive_path)
            extension_archives.append(archive_path)
        for path in static_paths:
            if path not in seen_static_inputs:
                seen_static_inputs.add(path)
                static_inputs.append(path)
        modules.append(
            {
                "module": module_name,
                "init_symbol": init_symbol,
                "manifest": manifest_relative.as_posix(),
                "manifest_path": manifest_path,
                "archive": archive_relative.as_posix(),
                "archive_path": archive_path,
                "static_inputs": [path.relative_to(build_root).as_posix() for path in static_paths],
            }
        )

    if not modules:
        raise ValueError(f"profile {profile_name} selects no modules")
    static_inputs = [path for path in static_inputs if path not in seen_extensions]
    link_inputs = [*extension_archives, *static_inputs]
    return {
        "schema_version": 1,
        "package": package,
        "profile": profile_name,
        "modules": modules,
        "extension_archives": extension_archives,
        "static_inputs": static_inputs,
        "link_inputs": link_inputs,
        "build_root": build_root,
    }


def render_registry_header(result: Dict[str, Any]) -> str:
    modules = result["modules"]
    lines = [
        "#ifndef WASM_EXTENSION_PROFILE_REGISTRY_H",
        "#define WASM_EXTENSION_PROFILE_REGISTRY_H",
        "",
    ]
    for item in modules:
        lines.append(f'extern PyObject *{item["init_symbol"]}(void);')
    lines.extend(
        [
            "",
            "static int register_selected_builtins(void) {",
        ]
    )
    for index, item in enumerate(modules, start=1):
        lines.extend(
            [
                f'    if (PyImport_AppendInittab("{item["module"]}", {item["init_symbol"]}) != 0) {{',
                f"        return {index};",
                "    }",
            ]
        )
    lines.extend(["    return 0;", "}", "", "#endif", ""])
    return "\n".join(lines)


def selection_report(result: Dict[str, Any]) -> Dict[str, Any]:
    build_root = result["build_root"]
    modules = []
    for item in result["modules"]:
        manifest_path = item["manifest_path"]
        archive_path = item["archive_path"]
        modules.append(
            {
                "module": item["module"],
                "init_symbol": item["init_symbol"],
                "manifest": item["manifest"],
                "manifest_sha256": _sha256(manifest_path),
                "archive": item["archive"],
                "archive_sha256": _sha256(archive_path),
                "archive_size": archive_path.stat().st_size,
                "static_inputs": item["static_inputs"],
            }
        )
    link_inputs = []
    extension_set = set(result["extension_archives"])
    for path in result["link_inputs"]:
        link_inputs.append(
            {
                "path": path.relative_to(build_root).as_posix(),
                "role": "extension" if path in extension_set else "support",
                "sha256": _sha256(path),
                "size": path.stat().st_size,
            }
        )
    return {
        "schema_version": 1,
        "package": result["package"],
        "profile": result["profile"],
        "modules": modules,
        "link_inputs": link_inputs,
        "claim": "build-time static extension selection; runtime dynamic linking is not used",
    }


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--profile", required=True)
    parser.add_argument("--manifest-dir", type=Path, required=True)
    parser.add_argument("--build-root", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args(argv)
    result = resolve_profile(args.config, args.profile, args.manifest_dir, args.build_root)
    args.output_dir.mkdir(parents=True, exist_ok=True)
    (args.output_dir / "builtin-registry.h").write_text(render_registry_header(result))
    (args.output_dir / "extension-archives.txt").write_text(
        "".join(f"{path}\n" for path in result["extension_archives"])
    )
    (args.output_dir / "static-inputs.txt").write_text(
        "".join(f"{path}\n" for path in result["static_inputs"])
    )
    (args.output_dir / "selection-report.json").write_text(
        json.dumps(selection_report(result), indent=2, sort_keys=True) + "\n"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
