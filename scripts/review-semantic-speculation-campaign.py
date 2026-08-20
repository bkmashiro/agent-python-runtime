#!/usr/bin/env python3
"""Independently verify and aggregate a private Phase 3 campaign."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import statistics
import sys
from typing import Any

CASES = (
    "branch_not_taken",
    "earlier_exception",
    "external_read_valid_suffix",
    "later_runtime_error",
    "later_syntax_error",
    "pure_local",
    "unknown_wrapper",
)
TREATMENTS = ("serial_whole_file", "eager_style_gate", "semantic_pre_dispatch")
SEED = 20260820
BINDING_FIELDS = (
    "artifact_sha256",
    "manifest_sha256",
    "import_inventory_sha256",
    "execution_profile_sha256",
    "capability_plan_sha256",
    "privacy_sha256",
)


def compact(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()


def digest(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def identity(value: dict[str, Any]) -> str:
    clone = dict(value)
    clone["identity"] = ""
    return digest(compact(clone))


def ranked_coordinates() -> list[tuple[str, int]]:
    values = [(case_id, trial) for case_id in CASES for trial in range(1, 6)]
    return sorted(values, key=lambda item: hashlib.sha256(f"{SEED}\0{item[0]}\0{item[1]}\0coordinate".encode()).digest())


def treatment_order(case_id: str, trial: int) -> list[str]:
    return sorted(TREATMENTS, key=lambda treatment: hashlib.sha256(f"{SEED}\0{case_id}\0{trial}\0{treatment}".encode()).digest())


def positive_delta(left: int, right: int) -> int:
    return left - right if left > right else 0


def elapsed(record: dict[str, Any]) -> int:
    result = record["ended_nanos"] - record["started_nanos"]
    require(result > 0, "non-positive elapsed time")
    return result


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def verify(root: pathlib.Path) -> dict[str, Any]:
    manifest_path = root / "campaign-manifest.json"
    require(root.is_dir() and not root.is_symlink(), "evidence root is not a directory")
    require(os.stat(root).st_mode & 0o077 == 0, "evidence root is not private")
    manifest_raw = manifest_path.read_bytes()
    manifest = json.loads(manifest_raw)
    require(compact(manifest) == manifest_raw, "manifest is not canonical JSON")
    require(identity(manifest) == manifest["identity"], "manifest identity mismatch")
    require(manifest["shuffle_seed"] == SEED and manifest["trials_per_treatment"] == 5, "campaign schedule mismatch")
    require(manifest["claim_scope"] == "synthetic_matched_mechanism_only", "claim scope mismatch")
    require(manifest["production_generalization"] is False and manifest["oracle_analysis_only"] is True, "claim guard mismatch")
    require((os.stat(manifest_path).st_mode & 0o777) == 0o600, "manifest mode mismatch")

    expected = ranked_coordinates()
    refs = manifest["files"]
    require(len(refs) == len(expected) == 35, "incomplete campaign grid")
    aggregates: list[dict[str, Any]] = []
    for ref, coordinate in zip(refs, expected):
        case_id, trial = coordinate
        require((ref["case_id"], ref["trial_index"]) == coordinate, "coordinate order mismatch")
        require(ref["file_name"] == f"{case_id}-trial-{trial:02d}.json", "evidence filename mismatch")
        path = root / ref["file_name"]
        raw = path.read_bytes()
        require((os.stat(path).st_mode & 0o777) == 0o600, "evidence mode mismatch")
        require(len(raw) == ref["size_bytes"] and digest(raw) == ref["sha256"], "evidence file binding mismatch")
        evidence = json.loads(raw)
        require(compact(evidence) == raw, "evidence is not canonical JSON")
        require(identity(evidence) == evidence["identity"] == ref["identity"], "evidence identity mismatch")
        require(evidence["execution_order"] == treatment_order(case_id, trial), "treatment order mismatch")
        require(evidence["claim_scope"] == "synthetic_matched_mechanism_only", "evidence claim scope mismatch")
        require(evidence["production_generalization"] is False and evidence["oracle_analysis_only"] is True, "evidence claim guard mismatch")

        records = evidence["records"]
        require([record["treatment"] for record in records] == evidence["execution_order"], "record order mismatch")
        require(len(records) == 3, "achieved treatment count mismatch")
        lanes = {record["treatment"]: record for record in records}
        require(set(lanes) == set(TREATMENTS), "achieved treatment set mismatch")
        for record in records:
            require(record["case_id"] == case_id and record["trial_index"] == trial, "record coordinate mismatch")
            for field in BINDING_FIELDS:
                require(record[field] == manifest["bindings"][field], f"record {field} mismatch")
        comparable = ("final_program_outcome", "result_sha256", "error_class", "logical_calls", "authority_disposition")
        for field in comparable:
            require(len({record[field] for record in records}) == 1, f"semantic outcome mismatch: {field}")

        serial, eager, semantic = (lanes[name] for name in TREATMENTS)
        aggregate = evidence["aggregate"]
        oracle = evidence["oracle"]
        recomputed = {
            "case_id": case_id,
            "trial_index": trial,
            "serial_elapsed_nanos": elapsed(serial),
            "eager_elapsed_nanos": elapsed(eager),
            "semantic_elapsed_nanos": elapsed(semantic),
            "oracle_elapsed_nanos": oracle["elapsed_nanos"],
            "semantic_versus_serial_nanos": positive_delta(elapsed(serial), elapsed(semantic)),
            "false_conservative_nanos": positive_delta(elapsed(eager), elapsed(semantic)),
            "safe_overlap_ready_before_finalize": semantic["ready_before_finalize"],
            "orphaned_physical_attempts": semantic["physical_dispositions"]["orphaned"],
            "physical_result_bytes": semantic["physical_result_bytes"],
            "provider_cost_units": semantic["provider_cost_units"],
            "oracle_excluded_from_achieved_speedup": True,
        }
        require(aggregate == recomputed, "aggregate mismatch")
        aggregates.append(recomputed)

    per_case: dict[str, Any] = {}
    for case_id in CASES:
        rows = [row for row in aggregates if row["case_id"] == case_id]
        per_case[case_id] = {
            "coordinates": len(rows),
            "median_serial_nanos": int(statistics.median(row["serial_elapsed_nanos"] for row in rows)),
            "median_eager_nanos": int(statistics.median(row["eager_elapsed_nanos"] for row in rows)),
            "median_semantic_nanos": int(statistics.median(row["semantic_elapsed_nanos"] for row in rows)),
            "median_oracle_nanos": int(statistics.median(row["oracle_elapsed_nanos"] for row in rows)),
            "median_serial_minus_semantic_nanos": int(statistics.median(row["serial_elapsed_nanos"] - row["semantic_elapsed_nanos"] for row in rows)),
            "median_eager_minus_semantic_nanos": int(statistics.median(row["eager_elapsed_nanos"] - row["semantic_elapsed_nanos"] for row in rows)),
            "ready_before_finalize_coordinates": sum(row["safe_overlap_ready_before_finalize"] > 0 for row in rows),
        }

    positive = sum(row["semantic_versus_serial_nanos"] > 0 for row in aggregates)
    ready = sum(row["safe_overlap_ready_before_finalize"] > 0 for row in aggregates)
    report = {
        "schema_version": "pysolate.semantic-speculation-independent-review.v1",
        "manifest_identity": manifest["identity"],
        "manifest_sha256": digest(manifest_raw),
        "source_commit": manifest["source_commit"],
        "claim_scope": manifest["claim_scope"],
        "integrity": {
            "canonical_manifest": True,
            "canonical_evidence_files": 35,
            "complete_seeded_grid": True,
            "matched_semantic_outcomes": 35,
            "recomputed_aggregates": 35,
            "shared_bindings_verified": True,
        },
        "campaign": {
            "coordinates": 35,
            "achieved_trials": 105,
            "positive_semantic_versus_serial_coordinates": positive,
            "ready_before_finalize_coordinates": ready,
            "orphaned_physical_attempts": sum(row["orphaned_physical_attempts"] for row in aggregates),
            "physical_result_bytes": sum(row["physical_result_bytes"] for row in aggregates),
            "provider_cost_units": sum(row["provider_cost_units"] for row in aggregates),
        },
        "per_case": per_case,
        "gate_p3": {
            "passed": positive > 0 and ready > 0,
            "required_positive_net_overlap": True,
            "observed_positive_net_overlap": positive > 0,
            "observed_pre_finalization_readiness": ready > 0,
        },
        "identity": "",
    }
    report["identity"] = identity(report)
    return report


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("evidence_root", type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    try:
        report = verify(args.evidence_root)
        encoded = compact(report)
        if args.output:
            fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "wb") as handle:
                handle.write(encoded)
                handle.flush()
                os.fsync(handle.fileno())
        sys.stdout.buffer.write(encoded + b"\n")
        return 0
    except (OSError, ValueError, KeyError, TypeError, json.JSONDecodeError) as error:
        print(f"independent campaign review failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
