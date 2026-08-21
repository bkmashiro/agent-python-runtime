#!/usr/bin/env python3
import argparse
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "docs/evidence/numpy-core-phase2-mechanism-evidence-v1.json"


def load(path):
    return json.loads(path.read_text())


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def verify_checksums(root, sums_path):
    for line in sums_path.read_text().splitlines():
        expected, relative = line.split("  ", 1)
        candidate = root / relative
        assert candidate.is_file()
        assert sha256(candidate) == expected


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifact-root", type=Path)
    args = parser.parse_args()
    evidence = load(EVIDENCE)
    assert evidence["schema_version"] == "pysolate.numpy-core-phase2-mechanism-evidence.v1"
    assert evidence["status"] == "mechanism_gate_passed"
    assert evidence["historical_p5_unchanged"] == {
        "phase5_gate_passed": False, "pass_count": 6, "fail_count": 5, "timing_samples_observed": 0,
    }
    assert evidence["independent_review"]["status"] == "passed"
    assert evidence["independent_review"]["unresolved_blockers"] == 0
    assert evidence["independent_review"]["fixed_target_commit"] == evidence["source"]["commit"]
    assert evidence["independent_review"]["fixed_target_tree"] == evidence["source"]["tree"]
    assert evidence["independent_review"]["final"] == "PASS blockers=0"
    assert evidence["build"]["external_prebuilt_artifact_used"] is False
    assert evidence["boundary"]["instruction_level_dbi_claimed"] is False
    assert evidence["qualification"]["fresh_process_runs"] == 2
    assert evidence["qualification"]["fresh_results_equal"] is True
    assert evidence["qualification"]["capability_calls"] == [0, 0]
    assert evidence["negative_controls"]["filesystem_write"]["result_published"] is False
    assert evidence["negative_controls"]["base_profile_substitution"]["rejected_before_guest_execution"] is True
    artifact_root = args.artifact_root or Path(evidence["artifact"]["canonical_local_bundle"])
    dist = artifact_root / "dist"
    ready = load(artifact_root / "RESULT.READY")
    assert ready["schema_version"] == "pysolate.workstation-guest-build.v0"
    assert ready["source_commit"] == evidence["source"]["commit"]
    assert ready["source_tree"] == evidence["source"]["tree"]
    verify_checksums(artifact_root, artifact_root / "SHA256SUMS")
    verify_checksums(dist, dist / "SHA256SUMS")
    manifest = load(dist / "manifest.json")
    inventory = load(dist / "import-inventory.json")
    qualification = load(dist / "import-qualification.json")
    build_cache = load(dist / "build-cache.json")
    artifact = dist / evidence["artifact"]["filename"]
    assert sha256(artifact) == evidence["artifact"]["sha256"] == manifest["artifact"]["sha256"]
    assert artifact.stat().st_size == evidence["artifact"]["size_bytes"] == manifest["artifact"]["size"]
    assert manifest["build"]["repository_commit"] == evidence["source"]["commit"]
    assert build_cache["schema_version"] == "pysolate.guest-build-cache-evidence.v1"
    assert build_cache["cache_key"] == ready["cache_key"]
    assert build_cache["layer_sha256"] == ready["cache_layer_sha256"]
    assert build_cache["final_cache_key"] == ready["final_cache_key"]
    assert build_cache["final_cache_disposition"] == ready["final_cache_disposition"]
    assert sha256(dist / "manifest.json") == evidence["artifact"]["manifest_sha256"]
    assert sha256(dist / "import-inventory.json") == evidence["qualification"]["inventory_sha256"]
    assert sha256(dist / "import-qualification.json") == evidence["qualification"]["qualification_sha256"]
    assert manifest["artifact_profile"] == inventory["artifact_profile"] == qualification["artifact_profile"] == "numpy-core"
    selection = manifest["extension_profile"]
    assert selection["identity"] == evidence["artifact"]["selection_identity"]
    assert len(selection["native_modules"]) == evidence["build"]["native_module_count"] == 19
    assert len(selection["support_libraries"]) == evidence["build"]["support_archive_count"] == 2
    assert selection["build"]["link_libraries"] == evidence["build"]["wasi_link_libraries"]
    numpy_rows = [row for row in qualification["results"] if row["name"] == "numpy"]
    assert numpy_rows == [{"error": "", "name": "numpy", "operation": "numpy_core_oracle", "status": "qualified"}]
    assert "numpy" in inventory["discoverable_roots"] and "numpy" in qualification["qualified_roots"]
    print(json.dumps({
        "schema_version": evidence["schema_version"], "status": "PASS",
        "artifact_sha256": evidence["artifact"]["sha256"], "native_modules": 19,
        "support_archives": 2, "fresh_runs": 2, "timing_samples_observed": 0,
    }, sort_keys=True, separators=(",", ":")))


if __name__ == "__main__":
    main()
