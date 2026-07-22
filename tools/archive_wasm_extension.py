#!/usr/bin/env python3
"""Archive Wasm extension objects instead of producing dynamic side modules.

Meson still receives an output at the requested ``.so`` path, but that file is
a deterministic ar archive. A canonical ``.a`` sibling and a JSON link manifest
retain the direct objects, static inputs, and original link flags for the later
monolithic Guest link.
"""

import argparse
import json
import re
import shutil
import subprocess
import sys
from pathlib import Path
from typing import List, NamedTuple, Optional, Sequence, Tuple


class ArchivePlan(NamedTuple):
    output: Path
    archive: Path
    objects: Tuple[Path, ...]
    static_inputs: Tuple[Path, ...]
    link_args: Tuple[str, ...]


def _resolve(path: str, cwd: Path) -> Path:
    candidate = Path(path)
    if not candidate.is_absolute():
        candidate = cwd / candidate
    return candidate.resolve()


def plan_shared_archive(args: Sequence[str], cwd: Path) -> Optional[ArchivePlan]:
    output_arg = None
    output_indexes = set()
    for index, arg in enumerate(args):
        if arg == "-o":
            if index + 1 >= len(args):
                raise ValueError("-o requires an output path")
            if output_arg is not None:
                raise ValueError("multiple -o outputs are unsupported")
            output_arg = args[index + 1]
            output_indexes.update((index, index + 1))

    if output_arg is None or "-shared" not in args or not output_arg.endswith(".so"):
        return None

    output = _resolve(output_arg, cwd)
    archive_name = re.sub(r"\.cpython-[^.]+\.so$", ".a", output.name)
    if archive_name == output.name:
        archive_name = output.name[:-3] + ".a"
    archive = output.with_name(archive_name)

    objects: List[Path] = []
    static_inputs: List[Path] = []
    link_args: List[str] = []
    for index, arg in enumerate(args):
        if index in output_indexes:
            continue
        if arg.endswith(".o"):
            objects.append(_resolve(arg, cwd))
        elif arg.endswith(".a"):
            static_inputs.append(_resolve(arg, cwd))
        else:
            link_args.append(arg)

    if not objects:
        raise ValueError("shared extension link has no direct object inputs")

    return ArchivePlan(
        output=output,
        archive=archive,
        objects=tuple(objects),
        static_inputs=tuple(static_inputs),
        link_args=tuple(link_args),
    )


def manifest_path_for(plan: ArchivePlan, manifest_dir: Path, build_root: Path) -> Path:
    output = plan.output.resolve()
    root = build_root.resolve()
    try:
        relative = output.relative_to(root)
    except ValueError as exc:
        raise ValueError("extension output is outside build root") from exc

    stem = re.sub(r"\.cpython-[^.]+\.so$", "", relative.name)
    if stem == relative.name:
        stem = relative.name[:-3]
    return manifest_dir.resolve() / relative.parent / (stem + ".json")


def _display_path(path: Path, build_root: Path) -> str:
    try:
        return str(path.resolve().relative_to(build_root.resolve()))
    except ValueError:
        return str(path.resolve())


def execute_archive(
    plan: ArchivePlan,
    archiver: Path,
    manifest_dir: Path,
    build_root: Path,
    compiler: Path,
) -> Path:
    missing = [str(path) for path in plan.objects if not path.is_file()]
    if missing:
        raise ValueError("missing extension objects: " + ", ".join(missing))

    plan.archive.parent.mkdir(parents=True, exist_ok=True)
    if plan.archive.exists():
        plan.archive.unlink()
    subprocess.run(
        [str(archiver), "rcsD", str(plan.archive)] + [str(path) for path in plan.objects],
        check=True,
    )
    shutil.copyfile(plan.archive, plan.output)

    manifest = manifest_path_for(plan, manifest_dir, build_root)
    manifest.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "schema_version": 1,
        "kind": "wasm-static-python-extension",
        "compiler": str(compiler.resolve()),
        "output": _display_path(plan.output, build_root),
        "archive": _display_path(plan.archive, build_root),
        "objects": [_display_path(path, build_root) for path in plan.objects],
        "static_inputs": [_display_path(path, build_root) for path in plan.static_inputs],
        "link_args": list(plan.link_args),
    }
    manifest.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
    return manifest


def _parse_args(argv: Sequence[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--real-compiler", type=Path, required=True)
    parser.add_argument("--archiver", type=Path, required=True)
    parser.add_argument("--manifest-dir", type=Path, required=True)
    parser.add_argument("--build-root", type=Path, required=True)
    parser.add_argument("compiler_args", nargs=argparse.REMAINDER)
    parsed = parser.parse_args(argv)
    if parsed.compiler_args[:1] == ["--"]:
        parsed.compiler_args = parsed.compiler_args[1:]
    if not parsed.compiler_args:
        parser.error("compiler arguments are required after --")
    return parsed


def main(argv: Sequence[str]) -> int:
    parsed = _parse_args(argv)
    cwd = Path.cwd()
    try:
        plan = plan_shared_archive(parsed.compiler_args, cwd)
        if plan is None:
            return subprocess.run(
                [str(parsed.real_compiler)] + list(parsed.compiler_args)
            ).returncode
        manifest = execute_archive(
            plan,
            parsed.archiver,
            parsed.manifest_dir,
            parsed.build_root,
            parsed.real_compiler,
        )
    except (OSError, subprocess.CalledProcessError, ValueError) as exc:
        print("archive_wasm_extension: {}".format(exc), file=sys.stderr)
        return 2

    print(
        "archive_wasm_extension: {} objects -> {} ({})".format(
            len(plan.objects), plan.archive, manifest
        ),
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
