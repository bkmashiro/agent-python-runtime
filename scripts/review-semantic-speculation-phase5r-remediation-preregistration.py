#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
path = root / "docs/evidence/semantic-speculation-phase5r-remediation-preregistration-v1.json"
data = json.loads(path.read_text())
identity = data.pop("identity")
raw = json.dumps(data, sort_keys=True, separators=(",", ":")).encode()
assert identity == "sha256:" + hashlib.sha256(raw).hexdigest()
assert data["parent_no_go_commit"] == "a72338215d528a92b97a50f83526679e9318c3b9"
assert data["parent_matrix_identity"] == "sha256:e4025295cc47cdc62925f4a4e0b0d3f072726de9aff983c75a0b9187fd355cee"
assert data["parent_preregistration_identity"] == "sha256:9db34a4fa8091bd9875132457dfcf9515fbf78802a5f0453029a4f52e1f776c6"
assert data["parent_artifact_sha256"] == "sha256:621f5fcec3f4bc7fc3550aa8fd1a275e7a6c09017518f535395c5bae84a297cb"
assert data["parent_mechanism_evidence_sha256"] == "sha256:7cde07a6c48bb9ef885aee7aed0865710650dfc8bf037c2dee44bcc672a15b42"
assert data["timing_samples_observed"] == 0
assert data["mechanism_gate"]["required_pass_count"] == 11
assert len(data["mechanism_gate"]["case_ids"]) == 11
assert data["economics_policy"] == "stage_cost_characterization_only_not_a_promotion_gate"
assert "run_original_80_record_economics_campaign" in data["forbidden"]
print(json.dumps({"identity": identity, "mechanism_case_count": 11, "status": "pass", "timing_samples_observed": 0}, sort_keys=True, separators=(",", ":")))
