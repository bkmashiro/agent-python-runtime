#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
HARNESS = ROOT / "docs/evidence/semantic-speculation-phase5r-harness-freeze-v5.json"
ARTIFACT = ROOT / "docs/evidence/semantic-speculation-phase5r-guest-artifact-freeze-v4.json"
EXPECTED_HARNESS = "sha256:ea88c6f2de16e5932c5211158b94c9d1efaa5d598096a0643def9276e153d042"
EXPECTED_ARTIFACT = "sha256:62454f9689ae4a11e45d51398e1d605be91b58b472eaafc26a994cb5669f62e9"
EXPECTED_MANIFEST = "sha256:012afb9fdc60da4844049b58416aab1fa2a29313fbe4988b2975a6ea211050f7"
EXPECTED_SOURCE = "af8b8404e748b69ad7f1c0ea14275358566f9eff"


def digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def main() -> None:
    harness = json.loads(HARNESS.read_text())
    artifact = json.loads(ARTIFACT.read_text())
    assert harness["schema_version"] == "pysolate.semantic-speculation-phase5r-harness-freeze.v5"
    assert harness["harness_identity"] == EXPECTED_HARNESS
    assert harness["guest_artifact_sha256"] == EXPECTED_ARTIFACT
    assert harness["mechanism_case_count"] == 11
    assert harness["parent_phase5_timing_samples_observed"] == 0
    assert harness["economics_campaign_authorized"] is False
    rows = harness["source_set"]["files"]
    for row in rows:
        assert row["sha256"] == digest((ROOT / row["path"]).read_bytes())
    assert digest(json.dumps(harness["source_set"], separators=(",", ":")).encode()) == EXPECTED_HARNESS
    assert artifact["schema_version"] == "pysolate.semantic-speculation-phase5r-guest-artifact-freeze.v4"
    assert artifact["artifact_sha256"] == EXPECTED_ARTIFACT
    assert artifact["manifest_sha256"] == EXPECTED_MANIFEST
    assert artifact["source_commit"] == EXPECTED_SOURCE
    assert artifact["parent_phase5_timing_samples_observed"] == 0
    root = Path(artifact["local_private_evidence_root"])
    assert digest((root / "agent-python-runtime.wasm").read_bytes()) == EXPECTED_ARTIFACT
    assert digest((root / "manifest.json").read_bytes()) == EXPECTED_MANIFEST
    print(json.dumps({"artifact_sha256": EXPECTED_ARTIFACT, "harness_identity": EXPECTED_HARNESS, "mechanism_case_count": 11, "parent_phase5_timing_samples_observed": 0, "status": "pass"}, separators=(",", ":")))


if __name__ == "__main__":
    main()
