#!/usr/bin/env python3
"""Provider-free preflight for every frozen T2 READ envelope and each Guest shape."""

import argparse
import hashlib
import json
import os
import pathlib
import subprocess
from typing import Any

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
PRIVATE_SCHEMA = "pysolate.tau2-t2-private-preregistration.v1"
PREFLIGHT_SCHEMA = "pysolate.tau2-t2-preflight.v1"
SIGNATURES = {
    "get_reservation_details": ["reservation_id"],
    "get_user_details": ["user_id"],
    "search_direct_flight": ["origin", "destination", "date"],
}


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def sha(body: bytes) -> str:
    return "sha256:" + hashlib.sha256(body).hexdigest()


def validate_guest_turn(turn: dict[str, Any], root: pathlib.Path) -> None:
    required = {"schema_version", "task_id", "content", "request_sha256", "response_sha256", "receipt", "capability_plan_sha256", "plan_document", "grant_policy", "raw_bodies"}
    if not isinstance(turn, dict) or not required.issubset(turn):
        raise ValueError("Guest preflight evidence incomplete")
    bodies = turn["raw_bodies"]
    if not isinstance(bodies, dict) or set(bodies) != {"guest_request", "guest_response"}:
        raise ValueError("Guest raw body references invalid")
    request_body = (root / bodies["guest_request"]).read_bytes()
    response_body = (root / bodies["guest_response"]).read_bytes()
    if sha(request_body) != turn["request_sha256"] or sha(response_body) != turn["response_sha256"]:
        raise ValueError("Guest raw body digest mismatch")
    plan_encoded = json.dumps(turn["plan_document"], separators=(",", ":")).encode()
    if sha(plan_encoded) != turn["capability_plan_sha256"]:
        raise ValueError("canonical Plan identity mismatch")
    policy = json.loads(json.dumps(turn["grant_policy"], sort_keys=True, separators=(",", ":")))
    grant_document = json.dumps({"schema_version": "pysolate.capability-grant.v1", "policy": policy}, separators=(",", ":")).encode()
    grants = turn["plan_document"].get("grants")
    if not isinstance(grants, list) or len(grants) != 1 or grants[0].get("policy_sha256") != sha(grant_document):
        raise ValueError("Grant policy identity mismatch")
    response = json.loads(response_body)
    if response.get("capability_plan_sha256") != turn["capability_plan_sha256"] or response.get("receipts") != [turn["receipt"]]:
        raise ValueError("raw Guest response identity mismatch")


def source_for(tool: str, arguments: dict[str, str]) -> tuple[str, list[str]]:
    names = SIGNATURES.get(tool)
    if names is None or set(arguments) != set(names) or not all(isinstance(arguments[name], str) for name in names):
        raise ValueError("unsupported T2 READ shape")
    values = ", ".join(repr(arguments[name]) for name in names)
    return f"result = tools.{tool}({values})", names


def resolve_paths(args: argparse.Namespace) -> argparse.Namespace:
    args.tau2_python = os.path.abspath(args.tau2_python)
    for name in ("source_root", "repo_root", "artifact", "private_manifest", "evidence_root", "public_output"):
        setattr(args, name, str(pathlib.Path(getattr(args, name)).resolve()))
    return args


def adapter_call(args, task_id: str, tool: str, arguments: dict[str, Any]) -> subprocess.CompletedProcess:
    request = {
        "schema_version": "pysolate.tau2-t2-read-request.v1", "source_revision": REVISION,
        "domain": "airline", "task_id": task_id, "call_id": f"preflight:{task_id}:{tool}",
        "tool": tool, "arguments": arguments,
    }
    return subprocess.run(
        [args.tau2_python, str(pathlib.Path(args.repo_root) / "scripts/tau2-t2-read-adapter.py"),
         "--source-root", args.source_root, "--private-manifest", args.private_manifest, "--task-id", task_id],
        input=canonical(request), capture_output=True, timeout=60,
    )


def guest_shape(args, evidence_root: pathlib.Path, task_id: str, tool: str, arguments: dict[str, str], index: int) -> dict[str, Any]:
    source, names = source_for(tool, arguments)
    source_path = evidence_root / f"shape-{index:02d}.py"
    output_path = evidence_root / f"shape-{index:02d}.json"
    source_path.write_text(source)
    source_path.chmod(0o600)
    env = os.environ.copy()
    env.update({
        "AGENT_RUNTIME_GUEST": args.artifact,
        "PYSOLATE_TAU2_PYTHON": args.tau2_python,
        "PYSOLATE_TAU2_SOURCE_ROOT": args.source_root,
        "PYSOLATE_TAU2_T2_PRIVATE_MANIFEST": args.private_manifest,
        "PYSOLATE_TAU2_T2_TASK_ID": task_id,
        "PYSOLATE_TAU2_DYNAMIC_SOURCE_FILE": str(source_path),
        "PYSOLATE_TAU2_DYNAMIC_OUTPUT_FILE": str(output_path),
        "PYSOLATE_TAU2_EXPECTED_CAPABILITY": "tau2.airline." + tool,
        "PYSOLATE_TAU2_EXPECTED_ARGUMENTS": canonical(arguments).decode(),
        "PYSOLATE_TAU2_EXPECTED_ARGUMENT_NAMES": canonical(names).decode(),
    })
    completed = subprocess.run(
        ["go", "test", "./integration/e2e", "-run", "^TestTau2T2DynamicModelTurnThroughRealGuest$", "-count=1"],
        cwd=args.repo_root, env=env, capture_output=True, text=True, timeout=240,
    )
    if completed.returncode != 0:
        raise RuntimeError("T2 Guest shape failed: " + (completed.stderr + completed.stdout)[-1200:])
    result = json.loads(output_path.read_text())
    validate_guest_turn(result, evidence_root)
    receipt = result.get("receipt")
    if result.get("schema_version") != "pysolate.tau2-t2-dynamic-turn-private.v1" or result.get("task_id") != task_id or not isinstance(receipt, dict) or receipt.get("outcome") != "ok" or not isinstance(receipt.get("source"), dict):
        raise ValueError("invalid T2 Guest shape evidence")
    return {"task_id": task_id, "tool": tool, "source_sha256": sha(source.encode()), "response_sha256": result["response_sha256"], "receipt_id": receipt["receipt_id"], "source_bound": True}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--repo-root", required=True)
    parser.add_argument("--tau2-python", required=True)
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--private-manifest", required=True)
    parser.add_argument("--evidence-root", required=True)
    parser.add_argument("--public-output", required=True)
    args = resolve_paths(parser.parse_args())
    private = json.loads(pathlib.Path(args.private_manifest).read_text())
    if private.get("schema_version") != PRIVATE_SCHEMA or private.get("source", {}).get("revision") != REVISION:
        raise ValueError("private preregistration mismatch")
    preregistration_identity = private.get("public_identity")
    if not isinstance(preregistration_identity, str) or not preregistration_identity.startswith("sha256:"):
        raise ValueError("public preregistration identity missing")
    evidence_root = pathlib.Path(args.evidence_root)
    evidence_root.mkdir(parents=True, exist_ok=True)
    evidence_root.chmod(0o700)

    adapter_results = []
    shape_examples: dict[str, tuple[str, dict[str, str]]] = {}
    for task in private["tasks"]:
        for action in task["reference_actions"]:
            completed = adapter_call(args, task["task_id"], action["name"], action["arguments"])
            if completed.returncode != 0:
                raise RuntimeError(f"adapter preflight failed for task {task['task_id']}: {completed.stderr[-500:]!r}")
            response = json.loads(completed.stdout)
            if response.get("task_id") != task["task_id"] or response.get("tool") != action["name"] or not isinstance(response.get("content"), str):
                raise ValueError("adapter preflight identity mismatch")
            adapter_results.append({"task_id": task["task_id"], "tool": action["name"], "arguments_sha256": sha(canonical(action["arguments"])), "response_sha256": sha(response["content"].encode())})
            shape_examples.setdefault(action["name"], (task["task_id"], action["arguments"]))
    if set(shape_examples) != set(SIGNATURES):
        raise ValueError("not all READ shapes are represented")

    first_task = private["tasks"][0]
    first_action = first_task["reference_actions"][0]
    wrong = dict(first_action["arguments"])
    first_key = next(iter(wrong))
    wrong[first_key] = "pysolate-out-of-scope-control"
    negative = adapter_call(args, first_task["task_id"], first_action["name"], wrong)
    if negative.returncode == 0:
        raise ValueError("out-of-scope adapter control was accepted")

    shapes = []
    for index, tool in enumerate(sorted(shape_examples), 1):
        task_id, arguments = shape_examples[tool]
        shapes.append(guest_shape(args, evidence_root, task_id, tool, arguments, index))
    private_result = {
        "schema_version": "pysolate.tau2-t2-private-preflight.v1", "source_revision": REVISION, "preregistration_identity": preregistration_identity,
        "adapter_calls": adapter_results, "guest_shapes": shapes, "negative_out_of_scope_rejected": True,
        "provider_calls": 0, "task_bodies_included": False, "tool_response_bodies_included": False,
        "raw_guest_bodies_verified": True, "canonical_plan_grant_verified": True,
        "pre_provider_repair_budget_used": 1,
        "pre_provider_repairs": ["absolute_data_paths_preserving_venv_python_symlink"],
    }
    private_path = evidence_root / "preflight-private.json"
    private_path.write_bytes(canonical(private_result) + b"\n")
    private_path.chmod(0o600)
    public = {
        "schema_version": PREFLIGHT_SCHEMA, "classification": "PREFLIGHT_SUPPORTED", "source_revision": REVISION, "preregistration_identity": preregistration_identity,
        "tasks": len(private["tasks"]), "reference_action_envelopes": len(adapter_results),
        "real_guest_shapes": [{"tool": item["tool"], "source_bound": item["source_bound"]} for item in shapes],
        "negative_out_of_scope_rejected": True, "provider_calls": 0,
        "raw_guest_bodies_verified": True, "canonical_plan_grant_verified": True,
        "pre_provider_repair_budget_used": 1,
        "pre_provider_repairs": ["absolute_data_paths_preserving_venv_python_symlink"],
        "task_bodies_included": False, "arguments_included": False, "tool_response_bodies_included": False,
    }
    public_path = pathlib.Path(args.public_output)
    public_path.parent.mkdir(parents=True, exist_ok=True)
    public_path.write_bytes(canonical(public) + b"\n")
    print(hashlib.sha256(public_path.read_bytes()).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
