import hashlib
import json
import pathlib
import subprocess
import sys

EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
TASK_ID = "11"
REFERENCE_TOOL = "update_reservation_flights"
SCHEMA = "pysolate.tau2-write-boundary-audit.v1"


def file_sha(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def decide(checks: dict[str, object]) -> str:
    required = {
        "upstream_world_is_fresh_per_constructor",
        "reference_is_a_real_write",
        "official_oracle_uses_independent_fresh_worlds",
        "attempt_world_bound_to_workspace_disposition",
        "existing_effect_contract_matches_handler_behavior",
        "existing_adapter_has_attempt_persistent_write_state",
        "final_state_disposition_join_exists",
        "cancel_or_failure_discards_attempt_world",
    }
    if set(checks) != required or not all(isinstance(value, bool) for value in checks.values()):
        raise ValueError("invalid qualification check set")
    return "qualified_for_discussion" if all(checks.values()) else "unsupported_effect_class"


def audit(source_root: pathlib.Path, repo_root: pathlib.Path) -> dict:
    revision = subprocess.run(["git", "rev-parse", "HEAD"], cwd=source_root, check=True, capture_output=True, text=True).stdout.strip()
    if revision != EXPECTED_REVISION:
        raise ValueError("source revision mismatch")
    sys.path.insert(0, str(source_root / "src"))
    from tau2.environment.toolkit import MUTATES_STATE_ATTR, TOOL_TYPE_ATTR  # type: ignore[import-not-found]
    from tau2.runner import build_environment, get_tasks  # type: ignore[import-not-found]

    task = get_tasks("airline", task_ids=[TASK_ID])[0]
    actions = task.evaluation_criteria.actions or []
    if len(actions) != 1 or actions[0].name != REFERENCE_TOOL:
        raise ValueError("task reference action mismatch")
    if task.initial_state is not None:
        raise ValueError("task unexpectedly carries an initial state")

    env_a = build_environment("airline")
    env_b = build_environment("airline")
    db_a = env_a.tools.db
    db_b = env_b.tools.db
    if db_a is None or db_b is None:
        raise ValueError("airline DB unavailable")
    reservation_id = actions[0].arguments["reservation_id"]
    reservation_a = db_a.reservations[reservation_id]
    reservation_b = db_b.reservations[reservation_id]
    before_hash = env_a.get_db_hash()
    before_db_identity = id(db_a)
    env_a.set_state(initialization_data=None, initialization_actions=None, message_history=[])

    method = getattr(env_a.tools, REFERENCE_TOOL)
    runtime_registry = (repo_root / "runtime/capability/registry.go").read_text()
    read_adapter = (repo_root / "scripts/tau2-read-adapter.py").read_text()
    evaluator_source = (source_root / "src/tau2/evaluator/evaluator_env.py").read_text()
    evidence = {
        "fresh_environment_db_identity_distinct": id(db_a) != id(db_b),
        "fresh_environment_target_identity_distinct": id(reservation_a) != id(reservation_b),
        "fresh_environment_initial_hash_equal": env_a.get_db_hash() == env_b.get_db_hash(),
        "empty_set_state_replaces_db": id(env_a.tools.db) != before_db_identity,
        "empty_set_state_changes_hash": env_a.get_db_hash() != before_hash,
        "reference_tool_type": str(getattr(method, TOOL_TYPE_ATTR).value),
        "reference_tool_mutates_state": bool(getattr(method, MUTATES_STATE_ATTR)),
        "runtime_external_write_effect_present": 'EffectExternalWrite' in runtime_registry or '"external_write"' in runtime_registry,
        "runtime_workspace_write_effect_present": 'EffectWorkspaceWrite = "workspace_write"' in runtime_registry,
        "current_adapter_scopes_task_11": 'value["task_id"] != "3"' not in read_adapter,
        "current_adapter_exposes_reference_tool": REFERENCE_TOOL in read_adapter,
    }
    source_files = {
        "airline_environment": "src/tau2/domains/airline/environment.py",
        "airline_tools": "src/tau2/domains/airline/tools.py",
        "base_environment": "src/tau2/environment/environment.py",
        "runner_build": "src/tau2/runner/build.py",
        "environment_evaluator": "src/tau2/evaluator/evaluator_env.py",
        "tasks": "data/tau2/domains/airline/tasks.json",
    }
    checks = {
        "upstream_world_is_fresh_per_constructor": evidence["fresh_environment_db_identity_distinct"] and evidence["fresh_environment_target_identity_distinct"] and evidence["fresh_environment_initial_hash_equal"],
        "reference_is_a_real_write": evidence["reference_tool_type"] == "write" and evidence["reference_tool_mutates_state"],
        "official_oracle_uses_independent_fresh_worlds": "predicted_environment = environment_constructor(" in evaluator_source and "gold_environment = environment_constructor(" in evaluator_source,
        "attempt_world_bound_to_workspace_disposition": False,
        "existing_effect_contract_matches_handler_behavior": False,
        "existing_adapter_has_attempt_persistent_write_state": evidence["current_adapter_scopes_task_11"] and evidence["current_adapter_exposes_reference_tool"],
        "final_state_disposition_join_exists": False,
        "cancel_or_failure_discards_attempt_world": False,
    }
    decision = decide(checks)
    report = {
        "schema_version": SCHEMA,
        "source": {
            "repository": "https://github.com/sierra-research/tau2-bench",
            "revision": revision,
            "task_id": TASK_ID,
            "task_sha256": hashlib.sha256(json.dumps(task.model_dump(mode="json"), sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
            "files": {name: {"path": rel, "sha256": file_sha(source_root / rel)} for name, rel in source_files.items()},
        },
        "reference": {"tool": REFERENCE_TOOL, "effect": "write", "mutates_state": True},
        "observations": evidence,
        "qualification_checks": checks,
        "decision": decision,
        "blocking_gaps": sorted(name for name, passed in checks.items() if not passed),
        "interpretation": {
            "upstream_task_local_state_isolation_observed": checks["upstream_world_is_fresh_per_constructor"],
            "pysolate_write_mapping_qualified": decision == "qualified_for_discussion",
            "write_executed_during_audit": False,
            "new_runtime_capability_added": False,
        },
    }
    canonical = json.dumps(report, sort_keys=True, separators=(",", ":")).encode()
    report["identity"] = "sha256:" + hashlib.sha256(canonical).hexdigest()
    return report


def main() -> int:
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--repo-root", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    report = audit(pathlib.Path(args.source_root).resolve(), pathlib.Path(args.repo_root).resolve())
    pathlib.Path(args.output).write_text(json.dumps(report, sort_keys=True, separators=(",", ":")) + "\n")
    print(report["identity"].removeprefix("sha256:"))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
