#!/usr/bin/env python3
import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
COMMIT = "5d7a6d7dc8a5ad50017748e1158a086305345b09"
TREE = "d05fb659f5d989ced879c015d8b4b5f6e0a04f45"
ARTIFACT = "sha256:2753cde560f3961a483df53aec334c8fdbb084934e5a62a56d436aea1ae557ad"
FILES = {
    "darwin_arm64": ROOT / "docs/evidence/numpy-producer-admission-phase6-darwin-arm64-v1.json",
    "linux_amd64": ROOT / "docs/evidence/numpy-producer-admission-phase6-linux-amd64-private-cow-v1.json",
}
TOP_LEVEL_KEYS = {
    "schema_version", "platform", "private_cow_required", "source_commit", "source_tree",
    "probe_binary_sha256", "artifact_sha256", "declaration", "admission",
    "producer_analysis_sha256", "final_analysis_sha256", "lineage", "ndarray_descriptor",
    "blob_descriptor", "publication", "materialization_plan", "producer_analysis", "producer_run",
    "final_analysis", "consumer_run", "original_run", "consumer_result", "original_result",
    "result_parity", "supported_producers", "adversarial", "store",
}
ALLOWED_BODY_KEYS = {"body_bytes", "body_sha256"}


def assert_body_safe(value):
    if isinstance(value, dict):
        for key, nested in value.items():
            lowered = key.lower()
            assert not ("body" in lowered and lowered not in ALLOWED_BODY_KEYS), key
            assert lowered not in {"raw", "request", "producer_raw", "raw_body", "body_base64"}, key
            if lowered == "payload":
                assert isinstance(nested, bool), key
            assert_body_safe(nested)
    elif isinstance(value, list):
        for nested in value:
            assert_body_safe(nested)
    elif isinstance(value, str):
        assert len(value) <= 256
subprocess.run(["git", "verify-commit", COMMIT], cwd=ROOT, check=True, stdout=subprocess.DEVNULL)
assert subprocess.check_output(["git", "show", "-s", "--format=%T", COMMIT], cwd=ROOT, text=True).strip() == TREE
for platform, path in FILES.items():
    raw = path.read_bytes()
    assert b"body_base64" not in raw and b"ndarray_body" not in raw and b"result_payload" not in raw
    evidence = json.loads(raw)
    assert set(evidence) == TOP_LEVEL_KEYS
    assert_body_safe(evidence)
    assert evidence["schema_version"] == "pysolate.numpy-producer-admission-probe.v1"
    assert evidence["platform"] == platform and evidence["source_commit"] == COMMIT and evidence["source_tree"] == TREE
    assert evidence["artifact_sha256"] == ARTIFACT and evidence["result_parity"] is True
    admission = evidence["admission"]
    descriptor = evidence["ndarray_descriptor"]
    bindings = descriptor["bindings"]
    lineage = evidence["lineage"]
    assert admission["operation"] == "arange_affine_i64_v1"
    assert admission["execution_profile_id"] == "numpy-core" and admission["no_external_inputs"] is True
    assert admission["analyzer_unknown_observed"] is True and admission["allowed_imports"] == ["base64", "hashlib", "numpy"]
    assert admission["analysis_sha256"] == evidence["producer_analysis_sha256"]
    assert descriptor["schema_version"] == "pysolate.numpy-ndarray-c.v1" and descriptor["dtype"] == "<i8"
    assert descriptor["shape"] == [2, 3] and descriptor["nbytes"] == 48
    for key in ("artifact_sha256", "execution_profile_sha256", "import_closure_sha256", "source_sha256", "inputs_sha256", "pass_registration_sha256"):
        assert bindings[key] == admission[key]
    assert bindings["execution_profile_id"] == admission["execution_profile_id"]
    assert lineage["admission_sha256"] == admission["identity_sha256"]
    assert lineage["decision"]["analysis_sha256"] == admission["analysis_sha256"]
    assert lineage["patch"]["decision_sha256"] == lineage["decision"]["identity_sha256"]
    assert lineage["patch"]["final_source_sha256"] == evidence["materialization_plan"]["final_source_sha256"]
    assert lineage["selection"]["patch_sha256"] == lineage["patch"]["identity_sha256"]
    for key in ("consumer_binding_sha256", "consumer_source_sha256", "final_source_sha256", "inputs_sha256", "request_sha256"):
        assert lineage[key] == evidence["materialization_plan"][key]
    assert lineage["decision"]["source_sha256"] == admission["source_sha256"]
    assert lineage["decision"]["ast_sha256"] == admission["ast_sha256"]
    assert lineage["decision"]["execution_profile_sha256"] == admission["execution_profile_sha256"]
    assert evidence["publication"]["guest_to_host_copy_bytes"] == 48
    assert evidence["materialization_plan"]["host_to_guest_copy_bytes"] == 48
    assert evidence["consumer_result"] == evidence["original_result"] == {"first": 6, "sum": 66, "c_contiguous": True}
    supported = {row["operation"]: row for row in evidence["supported_producers"]}
    expected = {"zeros_f64_v1": 65536, "affine_f64_v1": 1048576, "sum_i64_v1": 8, "matmul_f64_v1": 524288}
    assert set(supported) == set(expected)
    for operation, size in expected.items():
        row = supported[operation]
        assert row["body_bytes"] == size and row["declaration_sha256"].startswith("sha256:") and row["admission_sha256"].startswith("sha256:")
        assert row["execution"]["capability_calls"] == 0 and row["analysis_sha256"].startswith("sha256:")
    adversarial = {row["name"]: row for row in evidence["adversarial"]}
    assert set(adversarial) == {"random", "time", "file", "dynamic_import", "object_dtype", "unknown_call", "profile", "stale_source", "inputs"}
    for name in ("random", "time", "file", "dynamic_import", "object_dtype", "unknown_call"):
        assert adversarial[name]["rejected"] is True and adversarial[name]["exact_guest_analyzed"] is True
    for name in ("profile", "stale_source", "inputs"):
        assert adversarial[name]["rejected"] is True and adversarial[name]["exact_guest_analyzed"] is False
    runs = [evidence[name] for name in ("producer_analysis", "producer_run", "final_analysis", "consumer_run", "original_run")]
    runs += [row["analysis"] for row in supported.values()]
    runs += [row["execution"] for row in supported.values()]
    runs += [adversarial[name]["engine"] for name in ("random", "time", "file", "dynamic_import", "object_dtype", "unknown_call")]
    if platform == "linux_amd64":
        assert evidence["private_cow_required"] is True
        for run in runs:
            assert run["cow_probe"]["memory_cow_candidate"] is True and run["cow_probe"]["cow_selected"] is True and run["cow_probe"]["fallback"] is False
    else:
        assert evidence["private_cow_required"] is False
    assert evidence["store"]["closed"] is True and evidence["store"]["retained_bytes"] == 0
print("PASS numpy producer admission phase6 evidence")
