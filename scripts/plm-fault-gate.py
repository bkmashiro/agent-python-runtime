#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import statistics
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
ARTIFACT = Path(os.environ.get("AGENT_RUNTIME_GUEST", ""))
OUTPUT = Path(sys.argv[1]) if len(sys.argv) > 1 else ROOT / "docs/evidence/plm-v1-fault-matrix.json"
if not OUTPUT.is_absolute():
    OUTPUT = ROOT / OUTPUT
if not ARTIFACT.is_file():
    raise SystemExit("AGENT_RUNTIME_GUEST must point to the exact CPython/WASI artifact")


def run_json(name: str, command: list[str], env: dict[str, str] | None = None) -> dict:
    process = subprocess.run(
        command,
        cwd=ROOT,
        env={**os.environ, **(env or {})},
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        check=False,
    )
    passed: list[dict] = []
    failed: list[str] = []
    packages: list[str] = []
    for line in process.stdout.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        action = event.get("Action")
        test = event.get("Test")
        package = event.get("Package")
        if action == "pass" and test:
            passed.append({"name": test, "elapsed_seconds": event.get("Elapsed", 0)})
        elif action == "fail" and test:
            failed.append(test)
        elif action == "pass" and package:
            packages.append(package)
    if process.returncode != 0 or failed:
        raise SystemExit(f"{name} failed: returncode={process.returncode} tests={failed}")
    top_level_tests = sorted({row["name"] for row in passed if "/" not in row["name"]})
    return {
        "name": name,
        "command": command,
        "returncode": process.returncode,
        "test_pass_events": len(passed),
        "unique_tests": top_level_tests,
        "subtest_pass_events": sum(1 for row in passed if "/" in row["name"]),
        "package_passes": sorted(set(packages)),
    }


capability = run_json(
    "capability_temporal_faults",
    ["go", "test", "-json", "./runtime/capability", "-run", "TestPLM|TestSplitPhaseTableIsAtomicallyOwned|TestSplitPhaseTableRejectsNil", "-count=1"],
)
race = run_json(
    "capability_repeated_race",
    ["go", "test", "-race", "-json", "./runtime/capability", "-run", "TestPLM|TestSplitPhaseTableIsAtomicallyOwned|TestSplitPhaseTableRejectsNil", "-count=10"],
)
setup = run_json(
    "setup_failure",
    ["go", "test", "-json", "./runtime/engine/wazero", "-run", "TestSplitPhaseSetupFailureFinalizesSourceTimeCandidate", "-count=10"],
)
exact = run_json(
    "exact_guest",
    ["go", "test", "-json", "./integration/e2e", "-run", "TestRealGuestPLM", "-count=1"],
    {"AGENT_RUNTIME_GUEST": str(ARTIFACT)},
)

all_tests = set(capability["unique_tests"] + setup["unique_tests"] + exact["unique_tests"])
required = {
    "immutable_ready_and_not_ready": ["TestPLMImmutableCandidateAdoptsAtOriginalLinearizationPoint", "TestPLMImmutableCandidateMayCompleteAfterLinearizationStarts"],
    "snapshot_exact_and_mismatch": ["TestPLMTemporalModesAdoptOnlyUnderSoundEvidence"],
    "version_changes": ["TestPLMVersionChangeAfterValidationDoesNotMoveTheLogicalPoint", "TestPLMSeededTwoCallProgramsRefineSequentialVisibleOrder"],
    "lease_expiry_revocation_clock": ["TestPLMVersionedAndLeasedCandidatesRequireCurrentEvidence", "TestPLMLeaseRevocationAndClockEpochMismatchRestartCanonically"],
    "current_transport_and_order": ["TestPLMCurrentPreparesTransportButReadsFinalValueOnlyAtLinearization", "TestPLMCurrentReadsRemainOrderedAndStartOnlyAtTheirLogicalPoints"],
    "authority_and_quota": ["TestPLMLinearizationRejectsChangedHostContextAndProviderQuota"],
    "branch_loop_exception_failure_discard": ["TestRealGuestPLMPreservesBranchLoopInvalidationAndFallback", "TestRealGuestPLMEarlierExceptionDiscardsUnclaimedCandidate", "TestRealGuestPLMPrepareFailureRetriesOnlyAtLinearization"],
    "setup_failure": ["TestSplitPhaseSetupFailureFinalizesSourceTimeCandidate"],
    "cancellation_and_late_completion": ["TestPLMCancellationAndLateCompletionRemainRunOwned"],
    "uncertain_provider_outcome": ["TestPLMUncertainProviderOutcomeIsNeverReplayed", "TestPLMInvalidatedUncertainProviderOutcomeIsNeverReplayed"],
    "foreign_identity": ["TestSplitPhaseTableIsAtomicallyOwnedByOneRunBroker", "TestPLMPrepareRejectsResourceAndSealedContractDrift"],
    "pass_disabled": ["TestRealGuestPLMPassDisabledExecutesUnchangedSource"],
}
missing = {case: tests for case, tests in required.items() if any(test not in all_tests for test in tests)}
if missing:
    raise SystemExit(f"required PLM rows missing: {missing}")

economics = json.loads((ROOT / "docs/evidence/plm-v1-economics.json").read_text())
recalculated = []
for profile in economics["profiles"]:
    baseline = statistics.median(row["total_nanos"] for row in profile["samples"] if row["mode"] == "baseline")
    plm = statistics.median(row["total_nanos"] for row in profile["samples"] if row["mode"] == "plm")
    recalculated.append({
        "profile": profile["name"],
        "baseline_median_nanos": baseline,
        "plm_median_nanos": plm,
        "delta_percent": (plm - baseline) * 100 / baseline,
    })

artifact_sha = "sha256:" + hashlib.sha256(ARTIFACT.read_bytes()).hexdigest()
target_commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
evidence = {
    "schema_version": "pysolate.plm-fault-matrix.v1",
    "target_commit": target_commit,
    "artifact_sha256": artifact_sha,
    "artifact_source_commit": os.environ.get("PLM_ARTIFACT_SOURCE_COMMIT", "unknown"),
    "seed_range": {"first": 0, "last": 15, "count": 16},
    "runs": [capability, race, setup, exact],
    "required_rows": required,
    "required_rows_passed": len(required),
    "visible_projection": ["return_or_exception", "logical_call_count", "receipt_order", "final_external_state"],
    "provider_economic_projection": ["physical_starts", "canonical_restart_decisions", "provider_validation_physical_events"],
    "host_private_projection": ["candidate_disposition", "job_disposition", "validation_cost_units", "bounded_event_count"],
    "raw_bodies_stored": False,
    "economics_recalculated": recalculated,
}
OUTPUT.parent.mkdir(parents=True, exist_ok=True)
OUTPUT.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n")
print(OUTPUT)
