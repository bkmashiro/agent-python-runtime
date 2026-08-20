#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "docs/evidence/semantic-speculation-phase5-harness-freeze-v1.json"
EXPECTED_SCHEMA = "pysolate.semantic-speculation-phase5-harness-freeze.v1"
EXPECTED_SOURCE_SCHEMA = "pysolate.semantic-speculation-phase5-harness-source-set.v1"
EXPECTED_IDENTITY = "sha256:021ddcc7d76eebf1468174274f0b3c59c281625767235eca4a31046f4b6d2bb0"
EXPECTED_PATHS = [
    "research/semanticspeculation/phase5_exact_guest.go",
    "research/semanticspeculation/phase5_runner.go",
    "research/semanticspeculation/phase5_trial.go",
    "research/semanticspeculation/phase5_recorder.go",
    "research/semanticspeculation/phase5_aggregate.go",
    "runtime/semantic/analyzer.go",
    "runtime/preparedregion/contract.go",
    "runtime/preparedregion/scratch_contract.go",
    "runtime/preparedregion/selection.go",
    "runtime/preparedregion/table.go",
    "runtime/engine/wazero/semantic_session.go",
    "runtime/engine/wazero/prepared_region_scratch_capacity.go",
    "runtime/engine/wazero/prepared_region_final_capacity.go",
]

def digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()

def main() -> None:
    evidence = json.loads(EVIDENCE.read_text())
    assert evidence["schema_version"] == EXPECTED_SCHEMA
    assert evidence["harness_identity"] == EXPECTED_IDENTITY
    assert evidence["aggregate_validator"] == "AggregatePhase5Campaign"
    assert evidence["campaign_coordinate_count"] == 80
    assert evidence["timing_samples_observed"] == 0
    source_set = evidence["source_set"]
    assert source_set["schema_version"] == EXPECTED_SOURCE_SCHEMA
    rows = source_set["files"]
    assert [row["path"] for row in rows] == EXPECTED_PATHS
    for row in rows:
        assert row["sha256"] == digest((ROOT / row["path"]).read_bytes())
    canonical = json.dumps(source_set, separators=(",", ":")).encode()
    assert digest(canonical) == EXPECTED_IDENTITY
    print(json.dumps({"campaign_coordinate_count": 80, "harness_identity": EXPECTED_IDENTITY, "status": "pass", "timing_samples_observed": 0}, separators=(",", ":")))

if __name__ == "__main__":
    main()
