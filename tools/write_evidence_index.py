#!/usr/bin/env python3
"""Write a consumer-side evidence index after every required CI gate passes."""

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any, Dict, List


SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")


def file_identity(path: pathlib.Path) -> Dict[str, Any]:
    data = path.read_bytes()
    return {"name": path.name, "size_bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()}


def artifact_checksum_from_sbom(sbom: Dict[str, Any], artifact_name: str) -> str:
    matches = [package for package in sbom.get("packages", []) if package.get("name") == artifact_name]
    if len(matches) != 1:
        raise ValueError("SBOM must contain exactly one artifact package")
    checksums = {
        item.get("algorithm"): item.get("checksumValue")
        for item in matches[0].get("checksums", [])
    }
    value = checksums.get("SHA256")
    if not isinstance(value, str):
        raise ValueError("SBOM artifact SHA256 is missing")
    return value


def build_index(
    *,
    artifact: pathlib.Path,
    manifest_path: pathlib.Path,
    sbom_path: pathlib.Path,
    notices_path: pathlib.Path,
    commit: str,
    run_id: str,
    run_url: str,
) -> Dict[str, Any]:
    if not COMMIT_RE.fullmatch(commit):
        raise ValueError("commit must be a full lowercase Git SHA")
    if not run_id.isdigit() or not run_url.endswith(f"/runs/{run_id}"):
        raise ValueError("workflow run identity is invalid")
    manifest = json.loads(manifest_path.read_text())
    sbom = json.loads(sbom_path.read_text())
    artifact_identity = file_identity(artifact)
    expected = manifest.get("artifact", {})
    if manifest.get("build", {}).get("repository_commit") != commit:
        raise ValueError("manifest commit does not match evidence commit")
    if (
        expected.get("filename") != artifact_identity["name"]
        or expected.get("size") != artifact_identity["size_bytes"]
        or expected.get("sha256") != artifact_identity["sha256"]
    ):
        raise ValueError("manifest artifact identity does not match artifact bytes")
    if artifact_checksum_from_sbom(sbom, artifact.name) != artifact_identity["sha256"]:
        raise ValueError("SBOM artifact identity does not match artifact bytes")
    if artifact_identity["sha256"] not in notices_path.read_text():
        raise ValueError("notices do not bind the artifact digest")

    return {
        "schema_version": 1,
        "evidence_class": "private-ci",
        "repository": {"commit": commit},
        "workflow": {
            "name": "Guest artifact",
            "run_id": run_id,
            "run_url": run_url,
        },
        "artifact": artifact_identity,
        "manifest": file_identity(manifest_path),
        "sbom": file_identity(sbom_path),
        "notices": file_identity(notices_path),
        "tests": {
            "source_lock": "passed",
            "producer_contracts": "passed",
            "artifact_binding": "passed",
            "spdx_schema": "passed",
            "downloaded_bundle": "passed",
            "guest_e2e": "passed",
            "fresh_benchmark": "recorded",
            "prepared_benchmark": "recorded",
        },
        "limitations": [
            "Exact Core Wasm reproducibility is not established: controlled runs 29930351468, 29933187448, and 29934585110 retained data-section drift.",
            "Benchmarks use the production-safe synthetic fixture and are descriptive evidence, not a production latency promise.",
            "This private-CI evidence is not a release, deployment, signature bundle, or public distribution claim.",
        ],
    }


def validate_index(
    evidence: Dict[str, Any],
    artifact: pathlib.Path,
    manifest: pathlib.Path,
    sbom: pathlib.Path,
    notices: pathlib.Path,
) -> List[str]:
    errors: List[str] = []
    if evidence.get("schema_version") != 1 or evidence.get("evidence_class") != "private-ci":
        errors.append("evidence version/class is invalid")
    for key, path in (("artifact", artifact), ("manifest", manifest), ("sbom", sbom), ("notices", notices)):
        if evidence.get(key) != file_identity(path):
            errors.append(f"{key} identity drifted")
    commit = evidence.get("repository", {}).get("commit")
    if not isinstance(commit, str) or not COMMIT_RE.fullmatch(commit):
        errors.append("repository commit is invalid")
    run_id = evidence.get("workflow", {}).get("run_id")
    run_url = evidence.get("workflow", {}).get("run_url")
    if not isinstance(run_id, str) or not run_id.isdigit() or not isinstance(run_url, str) or not run_url.endswith(f"/runs/{run_id}"):
        errors.append("workflow identity is invalid")
    tests = evidence.get("tests")
    if not isinstance(tests, dict) or any(value not in {"passed", "recorded"} for value in tests.values()):
        errors.append("test evidence is incomplete")
    limitations = evidence.get("limitations")
    if not isinstance(limitations, list) or len(limitations) < 3 or not any("not established" in item for item in limitations):
        errors.append("limitations do not disclose reproducibility status")
    for section in ("artifact", "manifest", "sbom", "notices"):
        digest = evidence.get(section, {}).get("sha256")
        if not isinstance(digest, str) or not SHA256_RE.fullmatch(digest):
            errors.append(f"{section} SHA256 is invalid")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifact", required=True, type=pathlib.Path)
    parser.add_argument("--manifest", required=True, type=pathlib.Path)
    parser.add_argument("--sbom", required=True, type=pathlib.Path)
    parser.add_argument("--notices", required=True, type=pathlib.Path)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--run-url", required=True)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    evidence = build_index(
        artifact=args.artifact,
        manifest_path=args.manifest,
        sbom_path=args.sbom,
        notices_path=args.notices,
        commit=args.commit,
        run_id=args.run_id,
        run_url=args.run_url,
    )
    errors = validate_index(evidence, args.artifact, args.manifest, args.sbom, args.notices)
    if errors:
        raise ValueError("; ".join(errors))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
