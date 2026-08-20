#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
HARNESS = ROOT / "docs/evidence/semantic-speculation-phase5r-harness-freeze-v3.json"
ARTIFACT = ROOT / "docs/evidence/semantic-speculation-phase5r-guest-artifact-freeze-v3.json"
EXPECTED_IDENTITY = "sha256:28bf534099b1ace3d5bd8a5018e0b709a4377c05385c2ca248d5b6d8ae19b127"
EXPECTED_ARTIFACT = "sha256:bc619e88f53f349aa450a1f6d17974ba0891b0ccde13068c560a28b4bce9b06c"
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

harness = json.loads(HARNESS.read_text())
artifact = json.loads(ARTIFACT.read_text())
assert harness["schema_version"] == "pysolate.semantic-speculation-phase5r-harness-freeze.v3"
assert harness["harness_identity"] == EXPECTED_IDENTITY
assert harness["guest_artifact_sha256"] == EXPECTED_ARTIFACT
assert harness["mechanism_case_count"] == 11
assert harness["economics_campaign_authorized"] is False
assert harness["parent_phase5_timing_samples_observed"] == 0
source = harness["source_set"]
assert source["schema_version"] == "pysolate.semantic-speculation-phase5r-harness-source-set.v3"
assert [row["path"] for row in source["files"]] == EXPECTED_PATHS
for row in source["files"]:
    assert row["sha256"] == digest((ROOT / row["path"]).read_bytes())
assert digest(json.dumps(source, separators=(",", ":")).encode()) == EXPECTED_IDENTITY
assert artifact["schema_version"] == "pysolate.semantic-speculation-phase5r-guest-artifact-freeze.v3"
assert artifact["artifact_sha256"] == EXPECTED_ARTIFACT
assert artifact["manifest_sha256"] == "sha256:9bf5c8d80c2e8aaf11ddd96308597180a9b37c01f891d08f36e85e3b4dd32be2"
assert artifact["source_commit"] == "fa5f0eb7fe42c176c465dadb91e2f7b6ed3cb7c1"
assert artifact["analyzer_identity_generation"] == "pysolate.semantic-analyzer.v9"
print(json.dumps({"artifact_sha256": EXPECTED_ARTIFACT, "harness_identity": EXPECTED_IDENTITY, "mechanism_case_count": 11, "parent_phase5_timing_samples_observed": 0, "status": "pass"}, separators=(",", ":")))
