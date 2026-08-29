#!/usr/bin/env python3
"""Validate and aggregate prepared-family COW fan-out economics cells."""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import sys
from pathlib import Path
from typing import Any, Iterable, Sequence


SCHEMA = "pysolate.cow-fanout-economics.v1"
PREPARED_FAMILY_SCHEMA = "pysolate.prepared-family-economics.v1"
EXPECTED_INPUT_BYTES = 8 << 20
EXPECTED_INPUT_ELEMENT_VALUE = 1
EXPECTED_RESULT = 1_048_577
EXPECTED_PROCESS_MEMORY_SOURCE = "/proc/self/smaps_rollup"
EXPECTED_ISOLATION = "one fresh test subprocess per treatment and repetition; treatment order alternates"
MODES = ("private_copy", "private_cow")
TIMING_ARRAY_FIELDS = ("runner_create_nanos", "run_nanos", "runner_close_nanos")
TIMING_SCALAR_FIELDS = ("family_prepare_nanos", "family_close_nanos")
RESOURCE_FIELDS = ("pss_bytes", "private_dirty_bytes")
RESOURCE_SNAPSHOTS = ("baseline_resources", "after_create_resources", "after_run_resources")
RESOURCE_DELTAS = (
    "pss_create_delta_bytes",
    "pss_run_delta_bytes",
    "private_dirty_create_delta_bytes",
    "private_dirty_run_delta_bytes",
)
TREATMENT_MEDIANS = (
    "family_prepare_median_nanos",
    "runner_create_median_nanos",
    "run_median_nanos",
    "pss_create_delta_median_bytes",
    "pss_run_delta_median_bytes",
    "private_dirty_create_delta_median_bytes",
    "private_dirty_run_delta_median_bytes",
)
IDENTITY_FIELDS = (
    "source_commit",
    "source_tree",
    "artifact_sha256",
    "input_sha256",
    "input_bytes",
    "input_element_value",
    "expected_result",
    "process_memory_source",
)


def load(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot load evidence {path}: {error}") from error
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def _require_integer(value: Any, field: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{field} must be an integer")
    return value


def _validate_fanouts(fanouts: Sequence[int]) -> list[int]:
    expected = [_require_integer(value, "fanout") for value in fanouts]
    if not expected:
        raise ValueError("fanouts must not be empty")
    if len(set(expected)) != len(expected):
        raise ValueError("duplicate fanout in requested sweep")
    if expected != sorted(expected):
        raise ValueError("ordered fanout sweep requested in ascending order")
    if any(value < 1 or value > 8 for value in expected):
        raise ValueError("fanout must be in [1,8]")
    return expected


def _validate_raw_sample(sample: dict[str, Any], mode: str, iteration: int, fanout: int) -> None:
    if sample.get("mode") != mode or sample.get("iteration") != iteration or sample.get("fanout") != fanout:
        raise ValueError("sample coordinate drift")
    if _require_integer(sample.get("result"), "sample result") != EXPECTED_RESULT:
        raise ValueError("oracle mismatch: sample result")
    for field in TIMING_SCALAR_FIELDS:
        _require_integer(sample.get(field), f"sample {field}")
    for field in TIMING_ARRAY_FIELDS:
        values = sample.get(field)
        if not isinstance(values, list) or len(values) != fanout:
            raise ValueError(f"array length drift: {field}")
        for value in values:
            _require_integer(value, f"sample {field} value")
    try:
        for snapshot_name in RESOURCE_SNAPSHOTS:
            snapshot = sample[snapshot_name]
            if not isinstance(snapshot, dict):
                raise ValueError
            for field in RESOURCE_FIELDS:
                _require_integer(snapshot.get(field), f"sample {snapshot_name}.{field}")
        for field in RESOURCE_DELTAS:
            _require_integer(sample.get(field), f"sample {field}")
    except (KeyError, ValueError) as error:
        raise ValueError("process memory evidence drift") from error


def _validate_document(
    document: dict[str, Any],
    *,
    fanout: int,
    runs: int,
    source_commit: str,
    source_tree: str,
    artifact_sha256: str,
    reference_identity: dict[str, Any] | None,
) -> dict[str, Any]:
    if document.get("schema_version") != PREPARED_FAMILY_SCHEMA:
        raise ValueError("schema mismatch: prepared-family evidence")

    expected_identity = {
        "source_commit": source_commit,
        "source_tree": source_tree,
        "artifact_sha256": artifact_sha256,
        "input_bytes": EXPECTED_INPUT_BYTES,
        "input_element_value": EXPECTED_INPUT_ELEMENT_VALUE,
        "expected_result": EXPECTED_RESULT,
        "process_memory_source": EXPECTED_PROCESS_MEMORY_SOURCE,
    }
    for field, expected in expected_identity.items():
        if document.get(field) != expected:
            if field in {"input_bytes", "input_element_value", "expected_result", "process_memory_source"}:
                raise ValueError(f"oracle mismatch: {field}")
            raise ValueError(f"identity mismatch: {field}")
    input_sha256 = document.get("input_sha256")
    if not isinstance(input_sha256, str) or not input_sha256:
        raise ValueError("identity mismatch: input_sha256")

    identity = {field: document.get(field) for field in IDENTITY_FIELDS}
    if reference_identity is not None and identity != reference_identity:
        changed = next(field for field in IDENTITY_FIELDS if identity[field] != reference_identity[field])
        raise ValueError(f"identity mismatch: {changed}")

    if _require_integer(document.get("runs_per_arm"), "runs_per_arm") != runs:
        raise ValueError("sample count drift: runs_per_arm")
    if document.get("isolation") != EXPECTED_ISOLATION:
        raise ValueError("oracle mismatch: isolation")

    treatments = document.get("treatments")
    if not isinstance(treatments, list) or [treatment.get("mode") for treatment in treatments if isinstance(treatment, dict)] != list(MODES):
        raise ValueError("treatment contract drift")
    if len(treatments) != len(MODES):
        raise ValueError("treatment contract drift")

    for treatment, mode in zip(treatments, MODES):
        if not isinstance(treatment, dict):
            raise ValueError("treatment contract drift")
        for field in TREATMENT_MEDIANS:
            _require_integer(treatment.get(field), f"treatment {field}")
        samples = treatment.get("samples")
        if not isinstance(samples, list) or len(samples) != runs:
            raise ValueError(f"sample count drift: {mode}")
        for iteration, sample in enumerate(samples):
            if not isinstance(sample, dict):
                raise ValueError("sample coordinate drift")
            _validate_raw_sample(sample, mode, iteration, fanout)

    return identity


def _full_lifecycle_total_nanos(sample: dict[str, Any]) -> int:
    return sum(_require_integer(sample[field], f"sample {field}") for field in TIMING_SCALAR_FIELDS) + sum(
        sum(_require_integer(value, f"sample {field} value") for value in sample[field]) for field in TIMING_ARRAY_FIELDS
    )


def _paired_deltas(treatments: list[dict[str, Any]], runs: int) -> list[dict[str, int]]:
    by_mode = {treatment["mode"]: treatment["samples"] for treatment in treatments}
    paired: list[dict[str, int]] = []
    for iteration in range(runs):
        copy_total = _full_lifecycle_total_nanos(by_mode["private_copy"][iteration])
        cow_total = _full_lifecycle_total_nanos(by_mode["private_cow"][iteration])
        paired.append(
            {
                "iteration": iteration,
                "private_copy_full_lifecycle_total_nanos": copy_total,
                "private_cow_full_lifecycle_total_nanos": cow_total,
                "full_lifecycle_total_delta_nanos": cow_total - copy_total,
            }
        )
    return paired


def project(
    paths: Iterable[Path],
    *,
    fanouts: Sequence[int],
    runs: int,
    host_id: str,
    source_commit: str,
    source_tree: str,
    artifact_sha256: str,
    order_offset: int = 0,
) -> dict[str, Any]:
    expected_fanouts = _validate_fanouts(fanouts)
    if isinstance(runs, bool) or not isinstance(runs, int) or runs < 3 or runs > 20:
        raise ValueError("runs must be in [3,20]")
    if not isinstance(host_id, str) or not host_id:
        raise ValueError("host id is required")
    if not isinstance(source_commit, str) or not isinstance(source_tree, str) or not isinstance(artifact_sha256, str):
        raise ValueError("identity arguments are required")
    if isinstance(order_offset, bool) or order_offset not in (0, 1):
        raise ValueError("order offset must be 0 or 1")

    input_paths = [Path(path) for path in paths]
    if not input_paths:
        raise ValueError("fanout evidence is required")
    documents = [load(path) for path in input_paths]
    actual_fanouts: list[int] = []
    for document in documents:
        value = document.get("fanout")
        if isinstance(value, bool) or not isinstance(value, int):
            raise ValueError("fanout evidence is missing or invalid")
        actual_fanouts.append(value)
    if len(set(actual_fanouts)) != len(actual_fanouts):
        raise ValueError("duplicate fanout evidence")
    if actual_fanouts != sorted(actual_fanouts):
        raise ValueError("evidence cells must use ordered fanout")
    missing = [fanout for fanout in expected_fanouts if fanout not in actual_fanouts]
    if missing:
        raise ValueError(f"missing fanout evidence: {missing}")
    unexpected = [fanout for fanout in actual_fanouts if fanout not in expected_fanouts]
    if unexpected:
        raise ValueError(f"unexpected fanout evidence: {unexpected}")
    if actual_fanouts != expected_fanouts:
        raise ValueError("evidence cells must use ordered fanout")

    identity: dict[str, Any] | None = None
    cells: list[dict[str, Any]] = []
    for fanout, document in zip(actual_fanouts, documents):
        identity = _validate_document(
            document,
            fanout=fanout,
            runs=runs,
            source_commit=source_commit,
            source_tree=source_tree,
            artifact_sha256=artifact_sha256,
            reference_identity=identity,
        )
        treatments = copy.deepcopy(document["treatments"])
        cells.append(
            {
                "fanout": fanout,
                "treatments": treatments,
                "paired_full_lifecycle_total_deltas": _paired_deltas(treatments, runs),
            }
        )

    assert identity is not None
    return {
        "schema_version": SCHEMA,
        "host_id": host_id,
        "source_commit": source_commit,
        "source_tree": source_tree,
        "artifact_sha256": artifact_sha256,
        "input_sha256": identity["input_sha256"],
        "input_bytes": EXPECTED_INPUT_BYTES,
        "input_element_value": EXPECTED_INPUT_ELEMENT_VALUE,
        "expected_result": EXPECTED_RESULT,
        "runs": runs,
        "order_offset": order_offset,
        "cells": cells,
    }


def _parse_fanouts(values: Sequence[str]) -> list[int]:
    parsed: list[int] = []
    for value in values:
        for item in value.split(","):
            item = item.strip()
            if not item:
                raise ValueError("fanouts must contain only integers")
            try:
                parsed.append(int(item))
            except ValueError as error:
                raise ValueError(f"invalid fanout: {item}") from error
    return parsed


def _artifact_sha256(path: Path) -> str:
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    return "sha256:" + digest


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", dest="input_paths", action="append", default=[])
    parser.add_argument("--inputs", dest="input_groups", nargs="+", action="append", default=[])
    parser.add_argument("--fanouts", nargs="+", required=True)
    parser.add_argument("--runs", type=int, required=True)
    parser.add_argument("--host-id", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-tree", required=True)
    parser.add_argument("--order-offset", type=int, choices=(0, 1), required=True)
    artifact_group = parser.add_mutually_exclusive_group(required=True)
    artifact_group.add_argument("--artifact", type=Path)
    artifact_group.add_argument("--artifact-sha256")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)

    input_paths = list(args.input_paths)
    for group in args.input_groups:
        input_paths.extend(group)
    try:
        artifact_sha256 = args.artifact_sha256 or _artifact_sha256(args.artifact)
        result = project(
            input_paths,
            fanouts=_parse_fanouts(args.fanouts),
            runs=args.runs,
            host_id=args.host_id,
            source_commit=args.source_commit,
            source_tree=args.source_tree,
            artifact_sha256=artifact_sha256,
            order_offset=args.order_offset,
        )
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    except (OSError, ValueError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
