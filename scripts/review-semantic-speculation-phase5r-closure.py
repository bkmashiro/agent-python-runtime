#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MECHANISM = ROOT / "docs/evidence/semantic-speculation-phase5r-mechanism-evidence-v1.json"
COST = ROOT / "docs/evidence/semantic-speculation-phase5r-cost-evidence-v1.json"
PARENT = ROOT / "docs/evidence/semantic-speculation-phase5-mechanism-gate-evidence-v1.json"
EXPECTED_ARTIFACT = "sha256:62454f9689ae4a11e45d51398e1d605be91b58b472eaafc26a994cb5669f62e9"
EXPECTED_HARNESS = "sha256:ea88c6f2de16e5932c5211158b94c9d1efaa5d598096a0643def9276e153d042"


def digest(path: Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> None:
    mechanism = json.loads(MECHANISM.read_text())
    cost = json.loads(COST.read_text())
    parent = json.loads(PARENT.read_text())
    assert digest(PARENT) == mechanism["parent_mechanism_evidence_sha256"]
    assert parent["campaign_state"]["phase5_gate_passed"] is False
    assert parent["campaign_state"]["timing_samples_observed"] == 0
    assert mechanism["parent_phase5_timing_samples_observed"] == 0
    assert mechanism["artifact_sha256"] == EXPECTED_ARTIFACT
    assert mechanism["harness_identity"] == EXPECTED_HARNESS
    assert mechanism["case_count"] == mechanism["passed"] == 11
    assert mechanism["failed"] == 0
    assert set(mechanism["platforms"]) == {"darwin_arm64", "linux_amd64"}
    assert all(row["status"] == "pass" for row in mechanism["platforms"].values())
    assert mechanism["platforms"]["linux_amd64"]["private_cow_required"] is True
    invariants = mechanism["mechanism_invariants"]
    assert invariants["logical_calls"] == 0
    assert invariants["orphaned_physical"] == 0
    assert invariants["broker_available"] is False
    assert invariants["workspace_mounted"] is False
    assert invariants["fallback_or_replay"] is False
    assert invariants["authority_expansion"] is False
    assert all(invariants[key] is True for key in (
        "result_error_traceback_logs_parity",
        "fresh_independent_scratch_and_final_guests",
        "post_teardown_claim_consume_reject_discard_projected",
        "source_and_ast_bound",
        "unsafe_timeout_not_counted_as_semantic_rejection",
    ))
    assert invariants["single_use_capsule_max_bytes"] == 256
    assert mechanism["economics_campaign_started"] is False
    assert mechanism["economics_records_observed"] == 0
    assert mechanism["body_free"] is True
    assert cost["artifact_sha256"] == EXPECTED_ARTIFACT
    assert cost["promotion_gate"] is False
    assert cost["original_phase5_campaign"] is False
    assert cost["original_phase5_timing_samples_observed"] == 0
    assert cost["p5r_diagnostic_record_count"] == len(cost["records"]) == 4
    assert {row["platform"] for row in cost["records"]} == {"darwin_arm64", "linux_amd64"}
    assert {row["treatment"] for row in cost["records"]} == {"original", "prepared_region_derived"}
    assert all(row["critical_wall_milliseconds"] > 0 for row in cost["records"])
    assert cost["body_free"] is True
    print(json.dumps({"artifact_sha256": EXPECTED_ARTIFACT, "harness_identity": EXPECTED_HARNESS, "mechanism_passed": 11, "p5r_cost_records": 4, "parent_phase5_timing_samples_observed": 0, "status": "pass"}, separators=(",", ":")))


if __name__ == "__main__":
    main()
