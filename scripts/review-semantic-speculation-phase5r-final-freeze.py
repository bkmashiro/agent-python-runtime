#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
HARNESS = ROOT / "docs/evidence/semantic-speculation-phase5r-harness-freeze-v2.json"
ARTIFACT = ROOT / "docs/evidence/semantic-speculation-phase5r-guest-artifact-freeze-v2.json"
PRIVATE_DIST = Path("/Users/yuzhe/.hermes/evidence/pysolate/semantic-phase5r-remediation-cb3131e/dist")
EXPECTED_HARNESS = "sha256:56e0725fe8831fd7f5ed4e6b5c16faed391caad60177ef6f4ffd2d715781826e"


def digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def main() -> None:
    artifact = json.loads(ARTIFACT.read_text())
    assert artifact["schema_version"] == "pysolate.semantic-speculation-phase5r-guest-artifact-freeze.v2"
    assert artifact["final_mechanism_matrix_artifact"] is True
    assert artifact["parent_phase5_timing_samples_observed"] == 0
    wasm = PRIVATE_DIST / "agent-python-runtime.wasm"
    manifest_path = PRIVATE_DIST / "manifest.json"
    assert wasm.stat().st_size == artifact["artifact_size_bytes"]
    assert digest(wasm.read_bytes()) == artifact["artifact_sha256"]
    assert digest(manifest_path.read_bytes()) == artifact["manifest_sha256"]
    manifest = json.loads(manifest_path.read_text())
    assert manifest["build"]["repository_commit"] == artifact["source_commit"]

    harness = json.loads(HARNESS.read_text())
    assert harness["schema_version"] == "pysolate.semantic-speculation-phase5r-harness-freeze.v2"
    assert harness["harness_identity"] == EXPECTED_HARNESS
    assert harness["guest_artifact_sha256"] == artifact["artifact_sha256"]
    assert harness["mechanism_case_count"] == 11
    assert harness["economics_policy"] == "cost_profile_only_not_gate"
    assert harness["parent_phase5_timing_samples_observed"] == 0
    source_set = harness["source_set"]
    assert source_set["schema_version"] == "pysolate.semantic-speculation-phase5r-harness-source-set.v2"
    for row in source_set["files"]:
        assert row["sha256"] == digest((ROOT / row["path"]).read_bytes())
    assert digest(json.dumps(source_set, separators=(",", ":")).encode()) == EXPECTED_HARNESS
    assert EXPECTED_HARNESS in (ROOT / "research/semanticspeculation/phase5r_harness.go").read_text()
    print(json.dumps({"artifact_sha256": artifact["artifact_sha256"], "harness_identity": EXPECTED_HARNESS, "mechanism_case_count": 11, "parent_phase5_timing_samples_observed": 0, "status": "pass"}, separators=(",", ":")))


if __name__ == "__main__":
    main()
