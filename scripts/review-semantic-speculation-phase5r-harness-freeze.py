#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "docs/evidence/semantic-speculation-phase5r-harness-freeze-v1.json"
EXPECTED_IDENTITY = "sha256:394c35f1c671b5e6304bd62afc03facb8d3d31a8dae89ae47f765128007c673e"
EXPECTED_PATHS = [
    "research/semanticspeculation/phase5_exact_guest.go",
    "research/semanticspeculation/phase5_preregistration.go",
    "runtime/semantic/analyzer.go",
    "runtime/preparedregion/contract.go",
    "runtime/preparedregion/scratch_contract.go",
    "runtime/preparedregion/selection.go",
    "runtime/preparedregion/table.go",
    "runtime/engine/wazero/semantic_session.go",
    "runtime/engine/wazero/prepared_region_scratch_capacity.go",
    "runtime/engine/wazero/prepared_region_final_capacity.go",
    "guest/bootstrap/agent_runtime/__init__.py",
    "guest/bootstrap/agent_runtime/ast_support.py",
    "guest/bootstrap/agent_runtime/semantic.py",
    "guest/bootstrap/agent_runtime/prepared_region.py",
    "integration/e2e/prepared_region_helper_test.go",
    "integration/e2e/phase5r_mechanism_matrix_test.go",
    "integration/e2e/phase5r_teardown_evidence_test.go",
]


def digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def main() -> None:
    evidence = json.loads(EVIDENCE.read_text())
    assert evidence["schema_version"] == "pysolate.semantic-speculation-phase5r-harness-freeze.v1"
    assert evidence["harness_identity"] == EXPECTED_IDENTITY
    assert evidence["mechanism_case_count"] == 11
    assert evidence["economics_policy"] == "cost_profile_only_not_gate"
    assert evidence["parent_phase5_timing_samples_observed"] == 0
    source_set = evidence["source_set"]
    assert source_set["schema_version"] == "pysolate.semantic-speculation-phase5r-harness-source-set.v1"
    assert [row["path"] for row in source_set["files"]] == EXPECTED_PATHS
    for row in source_set["files"]:
        assert row["sha256"] == digest((ROOT / row["path"]).read_bytes())
    assert digest(json.dumps(source_set, separators=(",", ":")).encode()) == EXPECTED_IDENTITY
    go_source = (ROOT / "research/semanticspeculation/phase5r_harness.go").read_text()
    assert EXPECTED_IDENTITY in go_source
    print(json.dumps({"harness_identity": EXPECTED_IDENTITY, "mechanism_case_count": 11, "parent_phase5_timing_samples_observed": 0, "status": "pass"}, separators=(",", ":")))


if __name__ == "__main__":
    main()
