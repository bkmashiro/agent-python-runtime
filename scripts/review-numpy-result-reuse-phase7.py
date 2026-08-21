#!/usr/bin/env python3
"""Fail-closed review of the frozen NumPy result-reuse Phase 7 campaign."""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[1]
EVIDENCE = REPO / "docs/evidence/numpy-result-reuse-phase7-v1.json"
CONTROLS = REPO / "docs/evidence/numpy-result-reuse-adversarial-controls-v1.json"
DEFAULT_ROOT = Path.home() / ".local/share/pysolate/campaigns/numpy-result-reuse-phase7-1a6596d"
ROOT = Path(os.environ.get("PYSOLATE_NUMPY_CAMPAIGN_ROOT", DEFAULT_ROOT))
MECHANISM_COMMIT = "1d788057d3c183dbdafb28030a95967863ba63cd"
MECHANISM_TREE = "c4518d7ff6cbdc7b14f39722a08d3b7b3ed0ca82"
HARNESS_COMMIT = "1a6596d2cd238e6c441b7ffa798ecb9b1c01c5e9"
HARNESS_TREE = "d98612fa162c9eded44e4d6cf82f52f471cc5cd4"
ARTIFACT_SHA256 = "sha256:2753cde560f3961a483df53aec334c8fdbb084934e5a62a56d436aea1ae557ad"
REPORT_IDENTITY = "sha256:fa6fa1a8b68df5eb0fc5070660609a9800062769789fcd5f9c0a107680184e1e"
FORBIDDEN_KEYS = {"body", "body_base64", "payload", "request", "raw", "result_body", "producer_result"}
TOP_LEVEL_KEYS = {
    "schema_version", "campaign_identity_sha256", "preregistration_identity",
    "case_matrix_identity", "harness_source_commit", "harness_source_tree",
    "artifact_sha256", "local_report_file", "local_report_sha256", "coverage",
    "mechanism_invariants", "economics_interpretation",
    "require_universal_positive_economics", "observed_break_even_cells",
    "break_even_identification", "economics", "adversarial_controls",
    "environment_samples", "measurement_limits", "phase8_decision",
}


def digest(path: Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def run(*args: str) -> None:
    subprocess.run(args, cwd=REPO, check=True)


def body_safe(value: object) -> None:
    if isinstance(value, dict):
        assert not (set(value) & FORBIDDEN_KEYS)
        for key, child in value.items():
            assert len(key) <= 96
            body_safe(child)
    elif isinstance(value, list):
        assert len(value) <= 500
        for child in value:
            body_safe(child)
    elif isinstance(value, str):
        assert len(value) <= 512


def main() -> None:
    evidence = json.loads(EVIDENCE.read_text())
    assert set(evidence) == TOP_LEVEL_KEYS
    body_safe(evidence)
    assert evidence["schema_version"] == "pysolate.numpy-reuse-campaign-evidence.v1"
    assert evidence["campaign_identity_sha256"] == REPORT_IDENTITY
    assert evidence["harness_source_commit"] == HARNESS_COMMIT
    assert evidence["harness_source_tree"] == HARNESS_TREE
    assert evidence["artifact_sha256"] == ARTIFACT_SHA256
    assert evidence["economics_interpretation"] == "mixed_or_negative_cells_are_valid_results_and_do_not_fail_mechanism_closure"
    assert evidence["require_universal_positive_economics"] is False
    assert evidence["observed_break_even_cells"] == 0
    assert evidence["break_even_identification"] == "not_identified_from_coupled_sparse_grid"
    assert evidence["phase8_decision"] == "not_entered_all_40_observed_cells_negative_despite_measured_multi_consumer_coordinates"

    coverage = evidence["coverage"]
    assert coverage["records"] == 240 and coverage["cells"] == 80
    assert coverage["economics"] == 40 and coverage["adversarial_controls"] == 8
    assert len(coverage["platforms"]) == 2
    for platform in coverage["platforms"]:
        assert platform["records"] == 120
        assert platform["cold_records"] == platform["warm_records"] == 60
        assert platform["original_records"] == platform["reuse_records"] == 60
        assert platform["identity_unique"] is True
        assert platform["peak_resident_memory_bytes_min"] > 0
        assert platform["peak_resident_memory_bytes_max"] >= platform["peak_resident_memory_bytes_min"]
        assert platform["placement_fallbacks"] == 0
        if platform["platform"] == "linux_amd64":
            assert platform["warm_private_cow_guests"] == platform["warm_physical_guests"] > 0
        else:
            assert platform["platform"] == "darwin_arm64"
            assert platform["warm_private_cow_guests"] == 0

    invariants = evidence["mechanism_invariants"]
    for key in (
        "all_process_success", "all_protocol_ok", "all_result_parity",
        "all_fresh_guests", "all_no_authority_expansion", "all_no_replay",
    ):
        assert invariants[key] is True
    assert invariants["reuse_blob_consumed_records"] == 120
    assert invariants["reuse_lease_consumed_count"] == 216
    assert invariants["nonconsumed_reuse_leases"] == 0

    economics = evidence["economics"]
    assert len(economics) == 40
    assert all(item["observed_break_even"] is False for item in economics)
    assert all(item["net_saved_nanos"] < 0 and item["speedup_ratio"] < 1 for item in economics)
    assert all(item["break_even_identification"] == "not_identified_from_coupled_sparse_grid" for item in economics)
    assert all(item["break_even_compute_nanos"] is None for item in economics)
    assert all(item["break_even_consumer_count"] is None for item in economics)
    assert all(item["break_even_lead_gap_nanos"] is None for item in economics)
    assert len(evidence["adversarial_controls"]) == 8
    assert all(item["passed"] is True for item in evidence["adversarial_controls"])
    assert {item["platform"] for item in evidence["environment_samples"]} == {"darwin_arm64", "linux_amd64"}
    assert all(item["phase"] == "post_campaign" for item in evidence["environment_samples"])

    assert subprocess.check_output(["git", "rev-parse", f"{MECHANISM_COMMIT}^{{tree}}"], cwd=REPO, text=True).strip() == MECHANISM_TREE
    assert subprocess.check_output(["git", "rev-parse", f"{HARNESS_COMMIT}^{{tree}}"], cwd=REPO, text=True).strip() == HARNESS_TREE
    run("git", "verify-commit", MECHANISM_COMMIT)
    run("git", "verify-commit", HARNESS_COMMIT)
    run("python3", "scripts/review-numpy-result-reuse-preregistration.py")
    run("python3", "scripts/review-numpy-result-reuse-controls.py")

    darwin = ROOT / "darwin-arm64.jsonl"
    linux = ROOT / "linux-amd64.jsonl"
    report = ROOT / "report.json"
    assert darwin.is_file() and linux.is_file() and report.is_file()
    files = {item["platform"]: item for item in coverage["platforms"]}
    assert digest(darwin) == files["darwin_arm64"]["record_file_sha256"]
    assert digest(linux) == files["linux_amd64"]["record_file_sha256"]
    assert digest(report) == evidence["local_report_sha256"]
    local_report = json.loads(report.read_text())
    assert local_report["identity_sha256"] == REPORT_IDENTITY
    assert len(local_report["records"]) == 240
    assert len({item["identity_sha256"] for item in local_report["records"]}) == 240

    with tempfile.TemporaryDirectory(prefix="numpy-phase7-review-") as temp:
        regenerated = Path(temp) / "report.json"
        run(
            "go", "run", "./cmd/numpy-reuse-report",
            "--darwin", str(darwin), "--linux", str(linux),
            "--controls", str(CONTROLS), "--output", str(regenerated),
        )
        assert regenerated.read_bytes() == report.read_bytes()

    print("PASS numpy result reuse phase7 records=240 cells=80 economics=40 observed_break_even=0")


if __name__ == "__main__":
    main()
