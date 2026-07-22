#!/usr/bin/env python3
"""Resolve the canonical SOURCE_DATE_EPOCH for one repository commit."""

import argparse
import pathlib
import subprocess


def resolve(repository: pathlib.Path, commit: str) -> str:
    value = subprocess.check_output(
        ["git", "-C", str(repository), "show", "-s", "--format=%ct", commit],
        text=True,
    ).strip()
    if not value.isdigit() or int(value) <= 0:
        raise ValueError("commit did not resolve to a valid SOURCE_DATE_EPOCH")
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("commit", nargs="?", default="HEAD")
    parser.add_argument("--repository", type=pathlib.Path, default=pathlib.Path.cwd())
    args = parser.parse_args()
    print(resolve(args.repository.resolve(), args.commit))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
