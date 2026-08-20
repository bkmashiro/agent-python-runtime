#!/usr/bin/env python3
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
evidence = json.loads((root / "docs/evidence/semantic-speculation-phase5-mechanism-gate-evidence-v1.json").read_text())
matrix = json.loads((root / "docs/evidence/semantic-speculation-phase5-case-matrix-v1.json").read_text())
prereg = json.loads((root / "docs/evidence/semantic-speculation-phase5-preregistration-v1.json").read_text())
expected_ids = [case["id"] for case in matrix["cases"]]
observed = evidence["cases"]
if evidence["matrix_identity"] != matrix["identity"] or evidence["preregistration_identity"] != prereg["identity"]:
    raise SystemExit("phase 5 mechanism evidence lineage mismatch")
if [case["id"] for case in observed] != expected_ids or len(set(expected_ids)) != 11:
    raise SystemExit("phase 5 mechanism evidence case coverage mismatch")
passed = sum(case["status"] == "pass" for case in observed)
failed = sum(case["status"] == "fail" for case in observed)
summary = evidence["summary"]
if (passed, failed) != (summary["passed"], summary["failed"]) or summary["case_count"] != 11:
    raise SystemExit("phase 5 mechanism evidence aggregate mismatch")
if summary["mechanism_gate_passed"] or summary["economics_campaign_authorized"] or failed == 0:
    raise SystemExit("phase 5 mechanism no-go was not preserved")
if summary["failure_action"] != "record_no_go_retain_original_execution_and_do_not_expand_transport_or_authority":
    raise SystemExit("phase 5 mechanism failure action drift")
state = evidence["campaign_state"]
if state != {"phase5_gate_passed": False, "timing_campaign_started": False, "timing_samples_observed": 0}:
    raise SystemExit("phase 5 mechanism evidence observed or authorized timing")
print(json.dumps({"case_count": 11, "failed": failed, "mechanism_gate_passed": False, "status": "no_go_verified", "timing_samples_observed": 0}, separators=(",", ":")))
