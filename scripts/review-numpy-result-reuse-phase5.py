#!/usr/bin/env python3
import hashlib
import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
COMMIT = "16c141d051c43a2a89383336b3c4ca11fe9bb0c5"
TREE = "c9bd3255325b12c6ad54b1a78772b439ddb0d9dd"
ARTIFACT = "sha256:1b759febddb9dd0d65760c74b21b07c4f04fa992ce761639fbb9baef155926aa"
BODY = "sha256:f190072c5052f4f440d4a607c25f5bced487c420806c9aab4ca5b0653e72da61"
CASES = {
    "darwin_arm64": (
        "numpy-result-reuse-phase5-darwin-arm64-v1.json",
        "e22a5db558c250877475c648a93734b325f92f689392c5bdc6df67c4beac7ae9",
        "sha256:9bbb55d054955e55fb87bdb56bf0cf84ccf6117b9231efba600bafeb51f0efe2",
        False,
    ),
    "linux_amd64": (
        "numpy-result-reuse-phase5-linux-amd64-private-cow-v1.json",
        "a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38",
        "sha256:062b22ea4facbf50d8a014364a611a804b45c9042dae7c6092d7cf14ffe99d79",
        True,
    ),
}
GUESTS = ("producer", "original_a", "original_b", "consumer_a", "consumer_b", "original_error", "consumer_error")


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def reject_payload(value):
    if isinstance(value, dict):
        assert {"body", "body_base64", "request", "payload", "raw_bytes"}.isdisjoint(value)
        for child in value.values():
            reject_payload(child)
    elif isinstance(value, list):
        for child in value:
            reject_payload(child)


def review(platform, contract):
    name, file_sha, binary_sha, private = contract
    path = ROOT / "docs/evidence" / name
    assert sha(path) == file_sha
    e = json.loads(path.read_text())
    assert e["schema_version"] == "pysolate.numpy-reuse-probe.v1"
    assert e["platform"] == platform and e["private_cow_required"] is private
    assert e["source_commit"] == COMMIT and e["source_tree"] == TREE
    assert e["probe_binary_sha256"] == binary_sha and e["artifact_sha256"] == ARTIFACT
    assert e["body_sha256"] == e["host_hash_after_a"] == BODY and e["body_bytes"] == 48
    assert e["result_parity"] and e["error_traceback_log_parity"] and e["consumer_mutation_isolated"]
    assert e["consumer_a_result"] == e["original_a_result"] == {"first": 999, "sum": 1014, "c_contiguous": True}
    assert e["consumer_b_result"] == e["original_b_result"] == {"first": 0, "sum": 15, "c_contiguous": True}
    assert e["original_error_object"] == e["consumer_error_object"]
    assert e["original_error_logs"] == e["consumer_error_logs"]
    assert e["original_error_object"]["error_type"] == "ValueError"
    assert e["publication"]["guest_to_host_copy_bytes"] == 48
    assert e["publication"]["producer_envelope_bytes"] > 48
    assert e["publication"]["decode_seal_duration_ns"] >= 0
    for key in ("plan_a", "plan_b", "plan_error"):
        plan = e[key]
        assert plan["host_to_guest_copy_bytes"] == 48 and plan["request_bytes"] > 48
        assert plan["request_build_duration_ns"] >= 0
        for field in ("lease_id", "consumer_binding_sha256", "consumer_source_sha256", "final_source_sha256", "inputs_sha256", "request_sha256"):
            assert len(plan[field]) == 71 and plan[field].startswith("sha256:")
    assert e["materialization_duration_a_ns"] >= 0 and e["materialization_duration_b_ns"] >= 0
    for guest in GUESTS:
        row = e[guest]
        assert row["capability_calls"] == 0
        if private:
            assert row["cow_probe"]["cow_selected"] and not row["cow_probe"]["fallback"]
            assert row["cow_probe"]["memory_cow_candidate"]
            assert row["prepared_state"]["prepared_runs"] == 1
            assert row["prepared_image"]["available"]
        else:
            assert not row["cow_probe"]["cow_selected"]
            assert row["prepared_state"]["prepared_runs"] == 0
    store = e["store"]
    assert store["closed"] and store["entry_count"] == 0 and store["retained_bytes"] == 0
    assert sorted(row["state"] for row in store["leases"]) == ["consumed", "consumed", "rejected", "rejected"]
    reject_payload(e)
    return e


def main():
    subprocess.run(["git", "verify-commit", COMMIT], cwd=ROOT, check=True, stdout=subprocess.DEVNULL)
    assert subprocess.check_output(["git", "show", "-s", "--format=%T", COMMIT], cwd=ROOT, text=True).strip() == TREE
    artifact = Path.home() / ".local/share/pysolate/artifacts/numpy-core-47070b1e2ae0/dist/agent-python-runtime-numpy-core.wasm"
    assert "sha256:" + sha(artifact) == ARTIFACT
    darwin = review("darwin_arm64", CASES["darwin_arm64"])
    linux = review("linux_amd64", CASES["linux_amd64"])
    for field in ("descriptor_sha256", "body_sha256", "body_bytes"):
        assert darwin[field] == linux[field]
    print("numpy result reuse phase5 evidence review: PASS")


if __name__ == "__main__":
    main()
