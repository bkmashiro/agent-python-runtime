#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "docs/evidence/semantic-speculation-phase5r-guest-artifact-freeze-v1.json"
PRIVATE_DIST = Path("/Users/yuzhe/.hermes/evidence/pysolate/semantic-phase5r-remediation-1c8d560/dist")


def digest(path: Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> None:
    evidence = json.loads(EVIDENCE.read_text())
    assert evidence["schema_version"] == "pysolate.semantic-speculation-phase5r-guest-artifact-freeze.v1"
    assert evidence["preregistration_identity"] == "sha256:7fe51a6338aaf47a26608dae397aa3a397165f127c5726061293f00b2a0bebfc"
    assert evidence["parent_no_go_commit"] == "a72338215d528a92b97a50f83526679e9318c3b9"
    assert evidence["parent_phase5_timing_samples_observed"] == 0
    assert evidence["body_free"] is True
    artifact = PRIVATE_DIST / "agent-python-runtime.wasm"
    manifest = PRIVATE_DIST / "manifest.json"
    assert artifact.stat().st_size == evidence["artifact_size_bytes"]
    assert digest(artifact) == evidence["artifact_sha256"]
    assert digest(manifest) == evidence["manifest_sha256"]
    manifest_body = json.loads(manifest.read_text())
    assert manifest_body["build"]["repository_commit"] == evidence["source_commit"]
    assert "sha256:" + manifest_body["artifact"]["sha256"] == evidence["artifact_sha256"]
    print(json.dumps({"artifact_sha256": evidence["artifact_sha256"], "parent_phase5_timing_samples_observed": 0, "status": "pass"}, separators=(",", ":")))


if __name__ == "__main__":
    main()
