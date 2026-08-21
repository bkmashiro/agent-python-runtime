#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MATRIX = ROOT / "docs/evidence/numpy-result-reuse-case-matrix-v1.json"
PREREG = ROOT / "docs/evidence/numpy-result-reuse-preregistration-v1.json"
MATRIX_ID = "sha256:02172d79851a991b002bbfe3b5cda2c777eb92a0ecce877d6e360769be20cb5f"
PREREG_ID = "sha256:df6cb5376e25ace6084a40b3f323dc9843f18342bd56f5072f51b63c9b8f2c01"
PARENT = "sha256:e7065002d5519e99cbac4c182bcc79c8abadadf4aa2e3e753ece8bd160b9a12a"


def digest_without_identity(raw, identity):
    suffix = ('"identity":"' + identity + '"}').encode()
    assert raw.endswith(suffix)
    candidate = raw[: -len(suffix)] + b'"identity":""}'
    return "sha256:" + hashlib.sha256(candidate).hexdigest()


def load_canonical(path):
    raw = path.read_bytes()
    assert raw and not raw.endswith(b"\n")
    value = json.loads(raw)
    return value, raw


def main():
    matrix, matrix_raw = load_canonical(MATRIX)
    prereg, prereg_raw = load_canonical(PREREG)
    assert set(matrix) == {"schema_version", "study_id", "parent_commit", "parent_p5r_mechanism_evidence_sha256", "reference_wheels_commit", "numpy_source_commit", "numpy_source_archive_sha256", "shuffle_seed", "trials_per_treatment", "platforms", "profiles", "treatments", "cases", "identity"}
    assert matrix["identity"] == MATRIX_ID == digest_without_identity(matrix_raw, MATRIX_ID)
    assert prereg["identity"] == PREREG_ID == digest_without_identity(prereg_raw, PREREG_ID)
    assert matrix["parent_p5r_mechanism_evidence_sha256"] == prereg["parent_p5r_mechanism_evidence_sha256"] == PARENT
    assert prereg["case_matrix_identity"] == MATRIX_ID
    assert matrix["platforms"] == ["darwin_arm64", "linux_amd64"]
    assert matrix["profiles"] == ["cold_end_to_end", "preprovisioned_numpy_ready_equivalent_capacity"]
    assert matrix["treatments"] == ["original_recompute", "prepared_ndarray_reuse"]
    economics = [case for case in matrix["cases"] if case["economics_eligible"]]
    controls = [case for case in matrix["cases"] if not case["economics_eligible"]]
    assert len(matrix["cases"]) == 18 and len(economics) == 10 and len(controls) == 8
    assert matrix["trials_per_treatment"] == 3
    coordinates = len(matrix["platforms"]) * len(matrix["profiles"]) * len(economics) * len(matrix["treatments"]) * matrix["trials_per_treatment"]
    assert coordinates == 240
    assert prereg["require_universal_positive_economics"] is False
    assert prereg["economics_interpretation"] == "mixed_or_negative_cells_are_valid_results_and_do_not_fail_mechanism_closure"
    combined = matrix_raw + prereg_raw
    for forbidden in (b'"observed_', b'"trial_records"', b'"gate_passed"', b'"median_nanos"', b'"observed_speedup"'):
        assert forbidden not in combined
    print(json.dumps({"status": "pass", "case_matrix_identity": MATRIX_ID, "preregistration_identity": PREREG_ID, "case_count": 18, "economics_case_count": 10, "adversarial_case_count": 8, "campaign_coordinate_count": coordinates, "timing_samples_observed": 0, "universal_positive_economics_required": False}, sort_keys=True, separators=(",", ":")))


if __name__ == "__main__":
    main()
