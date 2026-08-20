#!/usr/bin/env python3
"""Independent, observation-free review of the frozen Phase 5 inputs."""

import argparse
import ast
import hashlib
import json
from pathlib import Path

MATRIX_FILE_SHA256 = "6d9a65844ab909f2e581e7ff42bf87490d20044c4271b1c1ef5ccedf7e6ae559"
PREREG_FILE_SHA256 = "d4ae7b8c8db0c09bc3501472d4a54f60768922c2e62bc6030fa77ce17ef30c43"
MATRIX_IDENTITY = "sha256:e4025295cc47cdc62925f4a4e0b0d3f072726de9aff983c75a0b9187fd355cee"
PREREG_IDENTITY = "sha256:9db34a4fa8091bd9875132457dfcf9515fbf78802a5f0453029a4f52e1f776c6"
PHASE3_IDENTITY = "sha256:f69c31c874d56b7563942bf889a798ed16b38a657fef18be90d4251f49fbee3f"
PHASE4_MATRIX_IDENTITY = "sha256:4cec92655c0f73578f96dc352be13e17aff3376645830ff89f0292e01d15af39"
PHASE4_PREREG_IDENTITY = "sha256:d17a78fa49fd8699f2d7ae3ec4f183e6e05e50a18d868f8fe54b26b87899676e"
EXPECTED_CASE_IDS = [
    "scalar_add_2_pilot",
    "scalar_add_16_gap0",
    "scalar_add_64_gap250",
    "scalar_multiply_256_gap1000",
    "scalar_add_512_gap6000",
    "scalar_int64_overflow",
    "scalar_unsafe_call",
    "derived_suffix_drift",
    "exception_before_region",
    "exception_after_region",
    "pre_cancelled_final_execution",
]
FORBIDDEN_FIELDS = {"observed_result", "trial_records", "gate_passed", "median_nanos"}


def decode_strict(path: Path, expected_file_sha: str):
    raw = path.read_bytes()
    if hashlib.sha256(raw).hexdigest() != expected_file_sha:
        raise ValueError(f"file identity drift: {path}")

    def reject_duplicates(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate key: {key}")
            result[key] = value
        return result

    value = json.loads(raw, object_pairs_hook=reject_duplicates)
    canonical = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
    if canonical != raw:
        raise ValueError(f"noncanonical JSON: {path}")
    return value


def contract_identity(value):
    body = dict(value)
    body["identity"] = ""
    raw = json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode()
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def reject_observations(value):
    if isinstance(value, dict):
        for key, child in value.items():
            if key.startswith("observed_") or key in FORBIDDEN_FIELDS:
                raise ValueError(f"observation field in frozen input: {key}")
            reject_observations(child)
    elif isinstance(value, list):
        for child in value:
            reject_observations(child)


def review(matrix_path: Path, prereg_path: Path):
    matrix = decode_strict(matrix_path, MATRIX_FILE_SHA256)
    prereg = decode_strict(prereg_path, PREREG_FILE_SHA256)
    reject_observations(matrix)
    reject_observations(prereg)

    if matrix["identity"] != MATRIX_IDENTITY or contract_identity(matrix) != MATRIX_IDENTITY:
        raise ValueError("matrix contract identity drift")
    if prereg["identity"] != PREREG_IDENTITY or contract_identity(prereg) != PREREG_IDENTITY:
        raise ValueError("preregistration contract identity drift")
    if matrix["parent_phase3_matrix_identity"] != PHASE3_IDENTITY:
        raise ValueError("Phase 3 lineage drift")
    if matrix["parent_phase4_matrix_identity"] != PHASE4_MATRIX_IDENTITY:
        raise ValueError("Phase 4 matrix lineage drift")
    if matrix["parent_phase4_preregistration_identity"] != PHASE4_PREREG_IDENTITY:
        raise ValueError("Phase 4 preregistration lineage drift")
    if prereg["case_matrix_identity"] != matrix["identity"]:
        raise ValueError("preregistration does not bind matrix")

    cases = matrix["cases"]
    if [case["id"] for case in cases] != EXPECTED_CASE_IDS:
        raise ValueError("case order or membership drift")
    eligible = [case for case in cases if case["economics_eligible"]]
    if len(eligible) != 4 or any(case["class"] != "positive" for case in eligible):
        raise ValueError("economics eligibility drift")
    if sum(case["class"] == "pilot_only" for case in cases) != 1:
        raise ValueError("pilot exclusion drift")

    for case in cases:
        tree = ast.parse(case["source"], filename=case["id"], mode="exec")
        source_digest = "sha256:" + hashlib.sha256(case["source"].encode()).hexdigest()
        if source_digest != case["source_sha256"]:
            raise ValueError(f"source identity drift: {case['id']}")
        focus_index = case["focus_region_index"]
        if focus_index >= len(tree.body) or not isinstance(tree.body[focus_index], ast.Assign):
            raise ValueError(f"focus region is not an assignment: {case['id']}")
        focus = tree.body[focus_index]
        if len(focus.targets) != 1 or not isinstance(focus.targets[0], ast.Name) or focus.targets[0].id != case["output_name"]:
            raise ValueError(f"focus output drift: {case['id']}")
        lines = case["source"].splitlines(keepends=True)
        region_source = "".join(lines[focus.lineno - 1:focus.end_lineno])
        region_digest = "sha256:" + hashlib.sha256(region_source.encode()).hexdigest()
        if region_digest != case["region_source_sha256"]:
            raise ValueError(f"region source identity drift: {case['id']}")
        binops = [node for node in ast.walk(focus.value) if isinstance(node, ast.BinOp)]
        if len(binops) != case["operator_count"]:
            raise ValueError(f"operator count drift: {case['id']}")
        expected_operator = {"integer_add": ast.Add, "integer_multiply": ast.Mult}.get(case["operation"])
        if expected_operator is not None and any(not isinstance(node.op, expected_operator) for node in binops):
            raise ValueError(f"operator shape drift: {case['id']}")

    profiles = matrix["profiles"]
    treatments = matrix["treatments"]
    trials = matrix["trials_per_treatment"]
    coordinate_count = len(eligible) * len(profiles) * len(treatments) * trials
    if profiles != ["cold_end_to_end", "preprovisioned_equivalent_capacity"]:
        raise ValueError("profile drift")
    if treatments != ["original_unchanged", "prepared_region_derived"]:
        raise ValueError("treatment drift")
    if coordinate_count != 80 or matrix["shuffle_seed"] != 20260822 or trials != 5:
        raise ValueError("campaign schedule drift")

    economics = prereg["economics_gate"]
    if economics != {
        "eligible_class": "positive",
        "minimum_positive_trials_per_cell": 4,
        "minimum_median_net_saving_nanos": 1,
        "required_passing_coordinates_per_profile": 2,
        "require_both_profiles_pass": True,
        "comparison": "original_unchanged_total_critical_path_nanos_minus_prepared_region_derived_total_critical_path_nanos",
    }:
        raise ValueError("economics gate drift")
    if prereg["failure_action"] != "record_no_go_retain_original_execution_and_do_not_expand_transport_or_authority":
        raise ValueError("failure action drift")

    return {
        "case_count": len(cases),
        "economics_case_count": len(eligible),
        "campaign_coordinate_count": coordinate_count,
        "matrix_identity": matrix["identity"],
        "preregistration_identity": prereg["identity"],
        "status": "pass",
        "timing_samples_observed": 0,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--matrix", type=Path, default=Path("docs/evidence/semantic-speculation-phase5-case-matrix-v1.json"))
    parser.add_argument("--preregistration", type=Path, default=Path("docs/evidence/semantic-speculation-phase5-preregistration-v1.json"))
    args = parser.parse_args()
    print(json.dumps(review(args.matrix, args.preregistration), sort_keys=True, separators=(",", ":")))


if __name__ == "__main__":
    main()
