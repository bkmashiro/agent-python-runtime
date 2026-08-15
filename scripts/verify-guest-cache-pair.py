#!/usr/bin/env python3
"""Verify a cold-refresh/warm-hit Guest cache pair."""

from __future__ import annotations

import argparse
import importlib.util
import json
import pathlib

VERIFIER_PATH = pathlib.Path(__file__).with_name("verify-workstation-build.py")
SPEC = importlib.util.spec_from_file_location("workstation_build_verifier", VERIFIER_PATH)
assert SPEC is not None and SPEC.loader is not None
verifier = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verifier)


def verify_pair_reports(cold: dict[str, object], warm: dict[str, object]) -> dict[str, object]:
    identity_fields = (
        "source_commit",
        "source_tree",
        "artifact_sha256",
        "cache_key",
        "cache_layer_sha256",
        "final_cache_key",
    )
    for field in identity_fields:
        if cold.get(field) != warm.get(field):
            raise ValueError(f"cold/warm identity mismatch: {field}")
    if cold.get("requested_cache_mode") != "refresh" or cold.get("cache_disposition") != "miss":
        raise ValueError("cold evidence is not a refresh miss")
    if warm.get("requested_cache_mode") != "auto" or warm.get("cache_disposition") != "hit":
        raise ValueError("warm evidence is not an auto hit")
    if cold.get("final_cache_disposition") != "miss" or warm.get("final_cache_disposition") != "hit":
        raise ValueError("final artifact cache did not prove miss then hit")
    cold_millis = cold.get("build_millis")
    warm_millis = warm.get("build_millis")
    if not isinstance(cold_millis, int) or not isinstance(warm_millis, int) or warm_millis * 4 > cold_millis * 3:
        raise ValueError("warm build is not at least 25 percent faster")
    return {
        "schema_version": "pysolate.guest-build-cache-pair.v0",
        "source_commit": cold["source_commit"],
        "source_tree": cold["source_tree"],
        "artifact_sha256": cold["artifact_sha256"],
        "cache_key": cold["cache_key"],
        "cache_layer_sha256": cold["cache_layer_sha256"],
        "cold_build_millis": cold_millis,
        "warm_build_millis": warm_millis,
        "speedup": round(cold_millis / warm_millis, 6),
    }


def verify_pair(cold_root: pathlib.Path, warm_root: pathlib.Path) -> dict[str, object]:
    cold = verifier.verify(cold_root)
    warm = verifier.verify(warm_root, str(cold["source_commit"]), str(cold["source_tree"]))
    return verify_pair_reports(cold, warm)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("cold", type=pathlib.Path)
    parser.add_argument("warm", type=pathlib.Path)
    args = parser.parse_args()
    print(json.dumps(verify_pair(args.cold, args.warm), sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
