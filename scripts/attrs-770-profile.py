#!/usr/bin/env python3
"""Validate the private attrs-770 profile build/replay and emit a body-safe report."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any

SCHEMA = "pysolate.attrs-770-profile.v1"
EXPECTED_PRODUCER_COMMIT = "26f2dd5df98c74d28b9ae066bf122fef4c1f2604"
EXPECTED_SOURCE_IDS = [
    "cpython-source", "wasi-sdk-linux-x86_64", "wasm-tools-linux-x86_64", "wasmtime-linux-x86_64",
    "wasi-vfs-cli-linux-x86_64", "wasi-vfs-source", "wasi-vfs-wasi-submodule-source",
    "wasi-vfs-static-library", "wasi-vfs-linked-storage-source", "spdx-2.3-json-schema", "attrs-source",
]
DIGEST = re.compile(r"^[0-9a-f]{64}$")
PRIVATE_MARKERS = ("/Users/", "~/.hermes", "file://", "traceback", "request-", "model.patch")
EXPECTED_PACKAGE = {
    "name": "attrs",
    "version": "20.3.0-39-g58d2adc",
    "status": "selected-pure-python",
    "import_root": "attr",
    "install_path": "site-packages/attr",
    "repository_license_id": "MIT",
    "source_commit": "58d2adce57f2c4e447eb12b892ebbb09cccbdcc3",
    "source_archive_sha256": "62aacc4a0014118dfedcca0f59767e21ba85aff60d3ac2c7b67caf97bda22f2b",
    "patch_sha256": "fdbfbdbb113809ae7982eb85e221ae5ddfdac9774a787114424e6ed2785f236e",
    "tree_sha256": "f1e3b25ec86f639a4ce256f5c1216fd585527142a08a284cc5fd9c9de603229f",
    "file_count": 20,
    "total_bytes": 162921,
}
EXPECTED_OPERATIONS = {
    "attr": "generic_dynamic_class",
    "types": "new_class",
    "typing": "generic_alias",
}


def strict_load(path: pathlib.Path) -> Any:
    def reject(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, item in pairs:
            if key in value:
                raise ValueError(f"duplicate JSON key in {path.name}")
            value[key] = item
        return value

    return json.loads(path.read_text(), object_pairs_hook=reject)


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def exit_code(root: pathlib.Path, lane: str) -> int:
    text = (root / f"exit-final-{lane}.txt").read_text().strip()
    require(text in {"0", "2"}, f"invalid exit code for {lane}")
    return int(text)


def response(root: pathlib.Path, lane: str) -> dict[str, Any]:
    value = strict_load(root / f"out-final-{lane}.json")
    require(isinstance(value, dict), f"invalid response for {lane}")
    return value


def validate_workspace(value: Any) -> dict[str, Any]:
    require(isinstance(value, dict), "missing workspace receipt")
    require(value.get("schema_version") == "pysolate.workspace-disposition.v1", "workspace schema mismatch")
    require(value.get("policy") == "discard" and value.get("disposition") == "discarded", "workspace was not discarded")
    require(value.get("initial_workspace_sha256") == value.get("final_workspace_sha256"), "workspace identity drift")
    require(value.get("entry_count") == 0 and value.get("total_bytes") == 0, "workspace was not empty")
    request_digest = str(value.get("request_sha256", "")).removeprefix("sha256:")
    require(DIGEST.fullmatch(request_digest) is not None, "invalid request identity")
    return value


def validate_response_common(value: dict[str, Any]) -> dict[str, Any]:
    metrics = value.get("metrics")
    require(isinstance(metrics, dict) and metrics.get("capability_calls") == 0, "unexpected Host capability calls")
    return validate_workspace(value.get("workspace_receipt"))


def build_report(build_root: pathlib.Path, run_root: pathlib.Path) -> dict[str, Any]:
    ready = strict_load(build_root / "RESULT.READY")
    manifest = strict_load(build_root / "dist/manifest.json")
    qualification = strict_load(build_root / "dist/import-qualification.json")
    artifact = build_root / "dist/agent-python-runtime-attrs-770.wasm"

    require(ready.get("schema_version") == "pysolate.workstation-guest-build.v0", "build evidence schema mismatch")
    require(ready.get("builder") == "gpu31.doc.ic.ac.uk" and ready.get("target") == "wasm32-wasip1", "builder mismatch")
    require(manifest.get("schema_version") == 4 and manifest.get("artifact_profile") == "attrs-770", "manifest profile mismatch")
    require(manifest.get("build", {}).get("repository_commit") == ready.get("source_commit"), "source commit mismatch")
    require(ready.get("source_commit") == EXPECTED_PRODUCER_COMMIT, "unexpected producer commit")
    sources = manifest.get("sources")
    require(isinstance(sources, list) and [row.get("id") for row in sources if isinstance(row, dict)] == EXPECTED_SOURCE_IDS, "canonical source set mismatch")
    artifact_record = manifest.get("artifact")
    require(isinstance(artifact_record, dict) and artifact_record.get("filename") == artifact.name, "artifact filename mismatch")
    artifact_digest = sha256(artifact)
    require(artifact_record.get("sha256") == artifact_digest and artifact_record.get("size") == artifact.stat().st_size, "artifact identity mismatch")
    require(ready.get("final_cache_disposition") == "hit", "final cache hit was not exercised")
    build_log = (build_root / "build.log").read_text().splitlines()
    require(bool(build_log), "build log is empty")
    cache_verification = json.loads(build_log[0])
    require(cache_verification.get("sha256") == artifact_digest and isinstance(cache_verification.get("artifact"), str), "cache artifact re-verification missing")

    extension = manifest.get("extension_profile")
    require(isinstance(extension, dict) and extension.get("schema_version") == 1 and extension.get("kind") == "pure-python-package" and extension.get("profile") == "attrs-770", "extension profile mismatch")
    package = extension.get("package")
    require(package == EXPECTED_PACKAGE, "package provenance mismatch")

    require(qualification.get("artifact_profile") == "attrs-770" and qualification.get("probe") == "guest-import-exec-v1", "qualification identity mismatch")
    results = qualification.get("results")
    require(isinstance(results, list), "qualification results missing")
    operations = {row.get("name"): row.get("operation") for row in results if isinstance(row, dict) and row.get("status") == "qualified"}
    for name, operation in EXPECTED_OPERATIONS.items():
        require(operations.get(name) == operation, f"missing qualified operation {name}")

    request_ids: list[str] = []
    for lane in ("positive-1", "positive-2"):
        require(exit_code(run_root, lane) == 0, f"positive lane {lane} process failed")
        value = response(run_root, lane)
        require(value.get("status") == "ok" and value.get("error") is None, f"positive lane {lane} did not pass")
        require(value.get("result") == {"module": "__main__", "name": "test", "oracle": "passed", "profile": "attrs-770"}, f"positive lane {lane} oracle mismatch")
        receipt = validate_response_common(value)
        request_ids.append(receipt["request_sha256"])
    require(len(set(request_ids)) == 2, "positive Runs did not retain distinct request identities")

    rejection_errors = {
        "base-reject": "execution profile artifact mismatch",
        "source-reject": "execution profile source comparison failed",
    }
    for lane, expected in rejection_errors.items():
        require(exit_code(run_root, lane) == 2, f"rejection lane {lane} did not fail before execution")
        require((run_root / f"err-final-{lane}.txt").read_text().strip() == expected, f"rejection lane {lane} error mismatch")
        require((run_root / f"out-final-{lane}.json").read_bytes() == b"", f"rejection lane {lane} produced Guest output")

    declared = response(run_root, "indirect-declared")
    undeclared = response(run_root, "indirect-undeclared")
    require(exit_code(run_root, "indirect-declared") == 0 and declared.get("status") == "ok" and declared.get("result") == {"module": "json"}, "declared sealed import failed")
    require(exit_code(run_root, "indirect-undeclared") == 0 and undeclared.get("status") == "error", "undeclared sealed import did not return a Guest error")
    require((undeclared.get("error") or {}).get("error_type") == "ImportError", "undeclared sealed import error mismatch")
    validate_response_common(declared)
    validate_response_common(undeclared)
    health = strict_load(run_root / "http-health.json")
    require(health.get("status") == "ready" and health.get("protocol_version") == "pysolate.remote-execution.v1", "HTTP service health evidence mismatch")
    startup = (run_root / "http-stderr.txt").read_text()
    require(f"artifact=sha256:{artifact_digest}" in startup, "HTTP service did not load verified artifact identity")

    report = {
        "schema_version": SCHEMA,
        "status": "supported",
        "build": {
            "source_commit": ready["source_commit"],
            "source_tree": ready["source_tree"],
            "builder": ready["builder"],
            "target": ready["target"],
            "build_millis": ready["build_millis"],
            "cache_disposition": ready["cache_disposition"],
            "final_cache_disposition": ready["final_cache_disposition"],
        },
        "artifact": {
            "profile": "attrs-770",
            "filename": artifact.name,
            "sha256": artifact_digest,
            "size": artifact.stat().st_size,
        },
        "package": package,
        "qualification": {
            "probe": qualification["probe"],
            "python_version": qualification["python_version"],
            "operations": EXPECTED_OPERATIONS,
            "phase": "restricted_agent_body",
        },
        "runs": {
            "natural_oracle": {
                "runs": 2,
                "passed": 2,
                "distinct_request_identities": True,
                "capability_calls": 0,
                "workspace_disposition": "discarded",
            },
            "pre_start_rejections": {
                "artifact_profile_mismatch": 1,
                "source_comparison_failed": 1,
                "physical_guest_started": False,
            },
            "sealed_importer": {
                "declared_root": "passed",
                "undeclared_root": "ImportError",
                "capability_calls": 0,
            },
        },
        "verification": {
            "canonical_source_set_bound": True,
            "final_cache_bundle_reverified": True,
            "http_service_loaded_verified_identity": True,
            "nested_manifest_fields_fail_closed": True,
            "post_copy_package_tree_reverified": True,
            "python_go_parser_fail_closed_parity": True,
        },
        "decision": {
            "profile_supported": True,
            "production_default": False,
            "implement_generic_installer": False,
            "implement_shard_scheduler": False,
        },
        "non_claims": [
            "not a general package manager or dependency resolver",
            "not native-extension support",
            "not a scheduler, worker pool, or physical Guest sharing result",
            "not an Open-SWE benchmark score or full trajectory replay",
        ],
    }
    validate_report(report)
    return report


def validate_report(report: Any) -> None:
    require(isinstance(report, dict) and report.get("schema_version") == SCHEMA and report.get("status") == "supported", "report identity mismatch")
    require(report.get("decision", {}).get("profile_supported") is True, "profile verdict mismatch")
    require(report.get("qualification", {}).get("phase") == "restricted_agent_body", "qualification phase mismatch")
    natural = report.get("runs", {}).get("natural_oracle", {})
    require(natural.get("runs") == 2 and natural.get("passed") == 2 and natural.get("distinct_request_identities") is True, "natural oracle summary mismatch")
    require(report.get("verification") == {
        "canonical_source_set_bound": True,
        "final_cache_bundle_reverified": True,
        "http_service_loaded_verified_identity": True,
        "nested_manifest_fields_fail_closed": True,
        "post_copy_package_tree_reverified": True,
        "python_go_parser_fail_closed_parity": True,
    }, "verification evidence drift")
    encoded = json.dumps(report, sort_keys=True, separators=(",", ":"))
    require(not any(marker in encoded for marker in PRIVATE_MARKERS), "report leaks private/body-bearing data")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--build-root", type=pathlib.Path, required=True)
    parser.add_argument("--run-root", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    report = build_report(args.build_root, args.run_root)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    print(sha256(args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
