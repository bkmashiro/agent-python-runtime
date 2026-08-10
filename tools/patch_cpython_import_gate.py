#!/usr/bin/env python3
"""Insert the Pysolate import audit boundary into the pinned CPython source."""

from __future__ import annotations

import argparse
from pathlib import Path

MARKER = '"agent_runtime.import"'
OLD = """    mod = import_get_module(tstate, abs_name);
    if (mod == NULL && _PyErr_Occurred(tstate)) {
"""
NEW = """    /* Pysolate emits this before the module-cache lookup so a native
       audit hook can enforce the sealed per-Run import set even for cached
       modules. This is an artifact-local CPython policy boundary. */
    if (_PySys_Audit(tstate, "agent_runtime.import", "O", abs_name) < 0) {
        goto error;
    }

    mod = import_get_module(tstate, abs_name);
    if (mod == NULL && _PyErr_Occurred(tstate)) {
"""


def patch(path: Path) -> None:
    source = path.read_text(encoding="utf-8")
    if MARKER in source:
        raise ValueError("CPython import gate is already present")
    if source.count(OLD) != 1:
        raise ValueError("pinned CPython import boundary drifted")
    path.write_text(source.replace(OLD, NEW), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("path", type=Path)
    args = parser.parse_args()
    patch(args.path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
