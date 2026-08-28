#!/usr/bin/env python3
"""Validate Pysolate Linux lane outputs and write one suite manifest."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path


SCHEMAS = {
    "one": "pysolate.plm-economics.v1",
    "four": "pysolate.plm-multiread-economics.v1",
    "family": "pysolate.prepared-family-economics.v1",
    "producer": "pysolate.transparent-campaign-public-projection.v1",
}


def sha(path: Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def load(path: Path) -> dict:
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def profiles(document: dict) -> dict:
    return {
        row["name"]: {
            "baseline_median_nanos": row["baseline_median_nanos"],
            "plm_median_nanos": row["plm_median_nanos"],
            "delta_percent": row["delta_percent"],
        }
        for row in document["profiles"]
    }


def validate_copy_on_write(document: dict, runs: int, fanout: int) -> None:
    expected_result = 1_048_577
    if (
        document.get("runs_per_arm") != runs
        or document.get("fanout") != fanout
        or document.get("input_element_value") != 1
        or document.get("expected_result") != expected_result
        or document.get("process_memory_source") != "/proc/self/smaps_rollup"
    ):
        raise ValueError("copy-on-write evidence contract drift")
    treatments = document.get("treatments")
    if not isinstance(treatments, list) or {row.get("mode") for row in treatments} != {"private_copy", "private_cow"}:
        raise ValueError("copy-on-write evidence contract drift")
    for treatment in treatments:
        samples = treatment.get("samples")
        if not isinstance(samples, list) or len(samples) != runs:
            raise ValueError("copy-on-write evidence contract drift")
        for sample in samples:
            if (
                sample.get("fanout") != fanout
                or sample.get("result") != expected_result
                or any(len(sample.get(field, [])) != fanout for field in ("runner_create_nanos", "run_nanos", "runner_close_nanos"))
            ):
                raise ValueError("copy-on-write evidence contract drift")


def project(root: Path, source_commit: str, source_tree: str, source_epoch: int, runs: int, fanout: int) -> dict:
    one = load(root / "plm/one-read.json")
    four = load(root / "plm/four-read.json")
    family = load(root / "prepared-family/economics.json")
    producer = load(root / "producer/public.json")
    platform = load(root / "platform.json")
    actual = {
        "one": one.get("schema_version"),
        "four": four.get("schema_version"),
        "family": family.get("schema_version"),
        "producer": producer.get("schema_version"),
    }
    if actual != SCHEMAS:
        raise ValueError(f"evidence schema drift: {actual}")
    base_sha = sha(root / "artifacts/base.wasm")
    numpy_sha = sha(root / "artifacts/numpy-core.wasm")
    if one.get("target_commit") != source_commit or four.get("target_commit") != source_commit:
        raise ValueError("PLM source commit drift")
    if four.get("artifact_source_commit") != source_commit:
        raise ValueError("PLM artifact source commit drift")
    if one.get("artifact_sha256") != base_sha or four.get("artifact_sha256") != base_sha:
        raise ValueError("PLM artifact identity drift")
    if family.get("source_commit") != source_commit or family.get("source_tree") != source_tree or family.get("artifact_sha256") != numpy_sha:
        raise ValueError("prepared-family source or artifact drift")
    validate_copy_on_write(family, runs, fanout)
    producer_source = producer.get("source", {})
    if (
        producer_source.get("campaign_source_commit") != source_commit
        or producer_source.get("artifact_source_commit") != source_commit
        or producer_source.get("artifact_sha256") != base_sha
    ):
        raise ValueError("producer source or artifact drift")
    return {
        "schema_version": "pysolate.linux-evaluation-suite.v1",
        "source_commit": source_commit,
        "source_tree": source_tree,
        "source_epoch": source_epoch,
        "platform": platform,
        "parameters": {"runs_per_arm": runs, "prepared_family_fanout": fanout},
        "artifacts": {"base": {"sha256": base_sha}, "numpy_core": {"sha256": numpy_sha}},
        "evidence": {
            "plm_one_read": {"path": "plm/one-read.json", "sha256": sha(root / "plm/one-read.json")},
            "plm_four_read": {"path": "plm/four-read.json", "sha256": sha(root / "plm/four-read.json")},
            "prepared_family": {"path": "prepared-family/economics.json", "sha256": sha(root / "prepared-family/economics.json")},
            "producer_public": {"path": "producer/public.json", "sha256": sha(root / "producer/public.json")},
            "producer_private_summary": {"path": "producer/private/summary.json", "sha256": sha(root / "producer/private/summary.json")},
        },
        "metrics": {
            "plm": {"one_read": profiles(one), "four_read": profiles(four)},
            "prepared_family": family["treatments"],
            "producer_physical_executions": {
                "baseline": producer["baseline"]["physical_executions"]["median"],
                "qualified": producer["qualified"]["physical_executions"]["median"],
            },
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-tree", required=True)
    parser.add_argument("--source-epoch", type=int, required=True)
    parser.add_argument("--runs", type=int, required=True)
    parser.add_argument("--fanout", type=int, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    manifest = project(args.root, args.source_commit, args.source_tree, args.source_epoch, args.runs, args.fanout)
    args.output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
