#!/usr/bin/env python3
"""Body-safe, read-only qualification for an exact tau2-bench checkout."""

import argparse
import ast
import hashlib
import json
import pathlib
import re
import subprocess
from collections import Counter
from typing import Any, Dict, Iterable


SCHEMA = "pysolate.tau2-qualification.v1"
SOURCE_URL = "https://github.com/sierra-research/tau2-bench"
REVISION_PATTERN = re.compile(r"^[0-9a-f]{40}$")


def canonical_json(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n").encode()


def digest(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _project_string(pyproject: str, key: str) -> str:
    project = re.search(r"(?ms)^\[project\]\s*(.*?)(?=^\[|\Z)", pyproject)
    if project is None:
        raise ValueError("missing [project] table")
    match = re.search(rf'(?m)^\s*{re.escape(key)}\s*=\s*"([^"]+)"\s*$', project.group(1))
    if match is None:
        raise ValueError("missing project " + key)
    return match.group(1)


def _tool_definitions(path: pathlib.Path) -> Counter:
    tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    result: Counter = Counter()
    for node in ast.walk(tree):
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for decorator in node.decorator_list:
            if not isinstance(decorator, ast.Call) or not decorator.args:
                continue
            function = decorator.func
            if not isinstance(function, ast.Name) or function.id != "is_tool":
                continue
            argument = decorator.args[0]
            if not isinstance(argument, ast.Attribute) or argument.attr not in {"READ", "WRITE", "GENERIC"}:
                raise ValueError("unsupported is_tool effect declaration")
            result[(node.name, argument.attr)] += 1
    return result


def _task_class(action_names: Iterable[str], types: Dict[str, str]) -> str:
    names = list(action_names)
    if not names:
        return "no_reference_action"
    values = [types.get(name) for name in names]
    if any(value is None for value in values):
        return "unclassifiable_reference"
    if "WRITE" in values:
        return "write_reference"
    if set(values) == {"READ"}:
        return "read_only_reference"
    return "generic_or_mixed_reference"


def audit_checkout(root: pathlib.Path, revision: str, domains: Iterable[str]) -> dict:
    root = pathlib.Path(root)
    if not REVISION_PATTERN.fullmatch(revision):
        raise ValueError("revision must be a full lowercase git SHA")
    pyproject = (root / "pyproject.toml").read_text(encoding="utf-8")
    license_text = (root / "LICENSE").read_text(encoding="utf-8")
    license_id = _project_string(pyproject, "license")
    if license_id != "MIT" or not license_text.startswith("MIT License"):
        raise ValueError("unexpected source license")

    domain_reports = {}
    for domain in sorted(set(domains)):
        definitions = _tool_definitions(root / "src" / "tau2" / "domains" / domain / "tools.py")
        types = {}
        definition_counts: Counter = Counter()
        for (name, effect), count in definitions.items():
            if count != 1 or name in types:
                raise ValueError("duplicate tool definition")
            types[name] = effect
            definition_counts[effect] += 1
        tasks = json.loads((root / "data" / "tau2" / "domains" / domain / "tasks.json").read_text(encoding="utf-8"))
        if not isinstance(tasks, list):
            raise ValueError("tasks must be a list")
        classes: Counter = Counter()
        for task in tasks:
            criteria = task.get("evaluation_criteria") or {}
            actions = criteria.get("actions") or []
            names = []
            for action in actions:
                name = action.get("name") if isinstance(action, dict) else None
                names.append(name if isinstance(name, str) else "")
            classes[_task_class(names, types)] += 1
        for key in (
            "read_only_reference",
            "write_reference",
            "generic_or_mixed_reference",
            "no_reference_action",
            "unclassifiable_reference",
        ):
            classes.setdefault(key, 0)
        domain_reports[domain] = {
            "tasks": len(tasks),
            "tool_definitions": {key: definition_counts.get(key, 0) for key in ("READ", "WRITE", "GENERIC")},
            "task_reference_classes": {key: classes[key] for key in sorted(classes)},
        }

    report = {
        "schema_version": SCHEMA,
        "source": {
            "repository": SOURCE_URL,
            "revision": revision,
            "version": _project_string(pyproject, "version"),
            "license": license_id,
        },
        "domains": domain_reports,
        "interpretation": {
            "reference_actions_are_agent_behavior": False,
            "write_mapping_qualified": False,
            "natural_sharing_opportunity_established": False,
        },
    }
    report["identity"] = digest(canonical_json(report))
    return report


def checkout_revision(root: pathlib.Path) -> str:
    process = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=root,
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    return process.stdout.strip()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True, type=pathlib.Path)
    parser.add_argument("--expected-revision", required=True)
    parser.add_argument("--domain", action="append", required=True)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    observed = checkout_revision(args.source_root)
    if observed != args.expected_revision:
        raise SystemExit("source revision mismatch")
    report = audit_checkout(args.source_root, observed, args.domain)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(canonical_json(report))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
