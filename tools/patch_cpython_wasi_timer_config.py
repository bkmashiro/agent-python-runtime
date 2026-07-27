#!/usr/bin/env python3
"""Patch CPython's WASI config.site for wazero-compatible relative sleeps."""

from __future__ import annotations

import argparse
from pathlib import Path

SETTINGS = (
    "ac_cv_func_clock_nanosleep=no",
    "ac_cv_lib_rt_clock_nanosleep=no",
)
MARKER = "# agent-python-runtime: wazero poll_oneoff supports relative clock sleeps"


def patch_config_site(source: str) -> str:
    conflicts = [setting for setting in SETTINGS if setting in source]
    if conflicts:
        raise ValueError(f"clock_nanosleep setting already present: {conflicts[0]}")
    suffix = "" if source.endswith("\n") else "\n"
    settings = "\n".join(SETTINGS)
    return f"{source}{suffix}\n{MARKER}\n{settings}\n"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=Path)
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    args.destination.write_text(patch_config_site(args.source.read_text()))


if __name__ == "__main__":
    main()
