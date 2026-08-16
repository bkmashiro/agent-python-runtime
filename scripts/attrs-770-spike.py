#!/usr/bin/env python3
"""Validate the private attrs-770 spike and emit a body-safe report."""

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any, Dict


SCHEMA = "pysolate.attrs-770-spike.v1"
PRIVATE_SCHEMA = "pysolate.attrs-770-spike-private.v1"
DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
COMMIT = re.compile(r"[0-9a-f]{40}")
BASE_COMMIT = "58d2adce57f2c4e447eb12b892ebbb09cccbdcc3"
INSTANCE_ID = "python-attrs__attrs-770"
EXPECTED_NATIVE_ERROR = "TypeError"
ARTIFACT_MISMATCH = "execution profile artifact mismatch"
SOURCE_REJECTION = "execution profile source comparison failed"


def digest_bytes(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def load_json(path: pathlib.Path) -> Dict[str, Any]:
    try:
        value = json.loads(path.read_text())
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError("invalid JSON evidence") from error
    if not isinstance(value, dict):
        raise ValueError("JSON evidence must be an object")
    return value


def require_digest(value: Any, name: str) -> str:
    if not isinstance(value, str) or DIGEST.fullmatch(value) is None:
        raise ValueError(name + " must be a sha256 identity")
    return value


def validate_private_manifest(root: pathlib.Path) -> Dict[str, Any]:
    manifest = load_json(root / "private-manifest.json")
    if manifest.get("schema_version") != PRIVATE_SCHEMA or manifest.get("instance_id") != INSTANCE_ID:
        raise ValueError("unexpected private manifest identity")
    if manifest.get("repo") != "python-attrs/attrs" or manifest.get("base_commit") != BASE_COMMIT:
        raise ValueError("unexpected source identity")
    if manifest.get("dataset_revision") != "ad4805a5aa7de70d99cab0bb8f99b15304c76de0" or manifest.get("dataset_license_id") != "cc-by-4.0" or manifest.get("repository_license_id") != "mit":
        raise ValueError("unexpected source provenance")
    if manifest.get("resolved") != 1 or manifest.get("fail_to_pass") != 1 or manifest.get("pass_to_pass") != 237:
        raise ValueError("unexpected oracle contract")
    for key in ("raw_source_sha256", "corpus_manifest_identity", "corpus_item_id", "source_record_sha256", "trajectory_source_sha256"):
        require_digest(manifest.get(key), key)
    expected = {
        "model_patch_sha256": digest_bytes((root / "model.patch").read_bytes()),
        "oracle_sha256": digest_bytes((root / "oracle_runner.py").read_bytes()),
    }
    for key, value in expected.items():
        if manifest.get(key) != value:
            raise ValueError(key + " mismatch")
    return manifest


def validate_native(root: pathlib.Path) -> Dict[str, str]:
    base = load_json(root / "native-base.json")
    patched = load_json(root / "native-patched.json")
    if base.get("oracle") != "failed" or base.get("error_type") != EXPECTED_NATIVE_ERROR:
        raise ValueError("native base did not reproduce RED")
    if patched != {"oracle": "passed", "module": "__main__", "name": "test"}:
        raise ValueError("native patched did not reproduce GREEN")
    return {"base": "expected_failure", "patched": "passed"}


def validate_guest_lane(root: pathlib.Path, lane: str) -> Dict[str, Any]:
    response = load_json(root / ("guest-unbound-" + lane + ".json"))
    metrics = response.get("metrics")
    receipt = response.get("workspace_receipt")
    if not isinstance(metrics, dict) or metrics.get("capability_calls") != 0 or response.get("receipts") != []:
        raise ValueError("guest lane used unexpected Host capabilities")
    if not isinstance(receipt, dict) or receipt.get("policy") != "discard" or receipt.get("disposition") != "discarded":
        raise ValueError("guest workspace was not discarded")
    initial = require_digest(receipt.get("initial_workspace_sha256"), "initial workspace")
    final = require_digest(receipt.get("final_workspace_sha256"), "final workspace")
    tree = require_digest(receipt.get("final_tree_sha256"), "final tree")
    if initial != final:
        raise ValueError("workspace changed")
    entries = receipt.get("entry_count")
    total_bytes = receipt.get("total_bytes")
    if not isinstance(entries, int) or not 1 <= entries <= 32 or not isinstance(total_bytes, int) or not 1 <= total_bytes <= 262144:
        raise ValueError("workspace bounds mismatch")
    if lane == "base":
        error = response.get("error")
        if response.get("status") != "error" or not isinstance(error, dict) or error.get("error_type") != EXPECTED_NATIVE_ERROR:
            raise ValueError("guest base did not reproduce RED")
        status = "expected_failure"
    else:
        expected = {"oracle": "passed", "module": "__main__", "name": "test"}
        if response.get("status") != "ok" or response.get("result") != expected or response.get("error") is not None:
            raise ValueError("guest patched did not reproduce GREEN")
        status = "passed"
    return {
        "status": status,
        "workspace_sha256": initial,
        "workspace_tree_sha256": tree,
        "entry_count": entries,
        "total_bytes": total_bytes,
    }


def validate_rejection(root: pathlib.Path, prefix: str, expected: str) -> None:
    for lane in ("base", "patched"):
        stdout = (root / (prefix + "-" + lane + ".json")).read_text()
        stderr = (root / (prefix + "-" + lane + ".stderr")).read_text().strip()
        if stdout != "" or stderr != expected:
            raise ValueError("verified profile rejection mismatch")


def validate_exit_codes(root: pathlib.Path) -> None:
    actual = load_json(root / "exit-codes.json")
    expected = {
        "native": {"base": 1, "patched": 0},
        "guest_unbound": {"base": 0, "patched": 0},
        "guest_bound_requested_package": {"base": 2, "patched": 2},
        "guest_bound_source_reject": {"base": 2, "patched": 2},
    }
    if actual != expected:
        raise ValueError("exit-code evidence mismatch")


def build_report(root: pathlib.Path, artifact: pathlib.Path, artifact_manifest: pathlib.Path,
                 runner: pathlib.Path, runner_source_commit: str) -> Dict[str, Any]:
    if COMMIT.fullmatch(runner_source_commit) is None:
        raise ValueError("invalid runner source commit")
    private = validate_private_manifest(root)
    native = validate_native(root)
    guest_base = validate_guest_lane(root, "base")
    guest_patched = validate_guest_lane(root, "patched")
    validate_rejection(root, "guest-bound", ARTIFACT_MISMATCH)
    validate_rejection(root, "guest-bound-source-reject", SOURCE_REJECTION)
    validate_exit_codes(root)
    report = {
        "schema_version": SCHEMA,
        "instance": {
            "instance_id": INSTANCE_ID,
            "repository": "python-attrs/attrs",
            "dataset_revision": private["dataset_revision"],
            "dataset_license_id": private["dataset_license_id"],
            "repository_license_id": private["repository_license_id"],
            "corpus_manifest_identity": private["corpus_manifest_identity"],
            "corpus_item_id": private["corpus_item_id"],
            "source_record_sha256": private["source_record_sha256"],
            "trajectory_source_sha256": private["trajectory_source_sha256"],
            "resolved": private["resolved"],
            "base_commit": BASE_COMMIT,
            "oracle_class": "single_public_fail_to_pass_semantic_case",
            "fail_to_pass": private["fail_to_pass"],
            "pass_to_pass": private["pass_to_pass"],
            "model_patch_sha256": private["model_patch_sha256"],
            "native_oracle_sha256": private["oracle_sha256"],
            "guest_source_sha256": digest_bytes((root / "guest-source.py").read_bytes()),
        },
        "identity": {
            "runner_source_commit": runner_source_commit,
            "runner_sha256": digest_bytes(runner.read_bytes()),
            "artifact_sha256": digest_bytes(artifact.read_bytes()),
            "artifact_manifest_sha256": digest_bytes(artifact_manifest.read_bytes()),
        },
        "native_oracle": native,
        "guest_oracle": {"base": guest_base["status"], "patched": guest_patched["status"]},
        "runtime_feasibility": {
            "verdict": "validated",
            "profile_bound": False,
            "artifact_manifest_supplied": False,
            "physical_guest_executed": True,
            "base_workspace": {key: value for key, value in guest_base.items() if key != "status"},
            "patched_workspace": {key: value for key, value in guest_patched.items() if key != "status"},
        },
        "verified_profile_admission": {
            "verdict": "unsupported_fail_closed",
            "physical_guest_started": False,
            "requested_package_binding": "artifact_mismatch",
            "source_without_package_permission": "source_comparison_failed",
        },
        "decision": {
            "current_profile_supports_workspace_package": False,
            "package_or_shard_profile_requires_separate_design": True,
            "implement_package_profile_now": False,
        },
        "limitations": [
            "unbound runtime feasibility is not verified artifact-profile admission",
            "the standalone semantic oracle is not the full SWE-bench or attrs compatibility matrix",
            "single-run observations are not performance evidence",
            "this experiment does not provide multi-agent overlap or sharing evidence",
        ],
    }
    encoded = json.dumps(report, separators=(",", ":"), sort_keys=True)
    for marker in ("/Users/", "~/.hermes", "diff --git", "traceback", "type() doesn't", "workspace-"):
        if marker in encoded:
            raise ValueError("public report contains private body or path")
    return report


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=pathlib.Path, required=True)
    parser.add_argument("--artifact", type=pathlib.Path, required=True)
    parser.add_argument("--manifest", type=pathlib.Path, required=True)
    parser.add_argument("--runner", type=pathlib.Path, required=True)
    parser.add_argument("--runner-source-commit", required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    arguments = parser.parse_args()
    report = build_report(arguments.root, arguments.artifact, arguments.manifest, arguments.runner,
                          arguments.runner_source_commit)
    arguments.output.parent.mkdir(parents=True, exist_ok=True)
    arguments.output.write_text(json.dumps(report, separators=(",", ":"), sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
