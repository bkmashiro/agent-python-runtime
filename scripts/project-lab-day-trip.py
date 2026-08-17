#!/usr/bin/env python3
"""Project a reviewed private day-trip Harness result into the public Lab schema."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
from typing import Any

SCHEMA = "pysolate.public-day-trip.v1"
CANDIDATES = ("brighton", "oxford")
SKILLS = ("budget-checking", "itinerary-formatting", "travel-research")


def load_json(path: Path, *, private: bool = False) -> Any:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"not a regular file: {path}")
    if private and path.stat().st_mode & 0o077:
        raise ValueError("private input is accessible to group or others")
    if path.stat().st_size <= 0 or path.stat().st_size > 1 << 20:
        raise ValueError("input is empty or over 1 MiB")

    def unique_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key: {key}")
            result[key] = value
        return result

    return json.loads(path.read_text(), object_pairs_hook=unique_pairs, parse_constant=lambda value: (_ for _ in ()).throw(ValueError(value)))


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def require_digest(value: Any, message: str) -> None:
    require(isinstance(value, str) and value.startswith("sha256:") and len(value) == 71 and all(c in "0123456789abcdef" for c in value[7:]), message)


def project(result: dict[str, Any], fixture_root: Path, source_commit: str) -> dict[str, Any]:
    require(len(source_commit) == 40 and all(c in "0123456789abcdef" for c in source_commit), "invalid source commit")
    system = (fixture_root / "system.md").read_text()
    skills = [{"id": skill, "body": (fixture_root / "skills" / f"{skill}.md").read_text()} for skill in SKILLS]
    request = load_json(fixture_root / "workspace" / "request.json")
    api = load_json(fixture_root / "workspace" / "deterministic-api-fixture.json")
    planning = result["planning"]
    candidates = result["candidates"]
    executions = result["executions"]
    selection = result["selection"]
    branch = result["branch"]
    final = result["final"]
    require(planning["candidate_ids"] == list(CANDIDATES), "candidate plan drift")
    require(len(candidates) == len(executions) == 2, "candidate evidence incomplete")
    require([candidate["candidate_id"] for candidate in candidates] == list(CANDIDATES), "candidate order or identity drift")
    require([execution["candidate_id"] for execution in executions] == list(CANDIDATES), "execution order or identity drift")
    require(selection["selected_candidate_id"] == branch["selected_candidate_id"] == final["selected_candidate_id"] == "oxford", "selection drift")
    require(branch["discarded_candidate_ids"] == ["brighton"], "discarded branch drift")
    require_digest(branch["selected_root_sha256"], "selected root identity drift")

    public_agents: list[dict[str, Any]] = []
    for candidate, execution in zip(candidates, executions):
        candidate_id = candidate["candidate_id"]
        require(candidate_id in CANDIDATES and candidate_id == execution["candidate_id"], "candidate identity mismatch")
        require_digest(execution["workspace_sha256"], "workspace identity drift")
        observed = execution["output"]
        require(observed["candidate_id"] == candidate_id, "observed output identity mismatch")
        expected_total = observed["rail"]["total_cost_gbp"] + observed["attraction"]["entry_cost_gbp"] * request["travellers"]
        require(abs(observed["total_cost_gbp"] - expected_total) < 0.001, "observed total mismatch")
        require(observed["weather"] == api["weather"][candidate_id], "weather evidence mismatch")
        require(observed["rail"] == api["rail"][candidate_id], "rail evidence mismatch")
        require(observed["attraction"] == api["attractions"][candidate_id], "attraction evidence mismatch")
        public_agents.append({
            "id": candidate_id,
            "label": f"{candidate_id.title()} candidate",
            "role": "candidate",
            "model_output": {
                "summary": candidate["summary"],
                "python_source": candidate["python_source"],
                "capture": "exact recorded DeepSeek assistant content",
            },
            "runtime": {
                "execution": "fresh isolated WASI Guest",
                "api_waits": [
                    {"capability": "travel.weather", "latency_ms": api["api_latency_ms"]["weather"], "observed": observed["weather"]},
                    {"capability": "travel.rail", "latency_ms": api["api_latency_ms"]["rail"], "observed": observed["rail"]},
                    {"capability": "travel.attractions", "latency_ms": api["api_latency_ms"]["attractions"], "observed": observed["attraction"]},
                ],
                "observed_output": observed,
                "workspace_sha256": execution["workspace_sha256"],
            },
            "disposition": "selected" if candidate_id == branch["selected_candidate_id"] else "discarded",
        })
    selected = next(agent for agent in public_agents if agent["id"] == branch["selected_candidate_id"])
    require(selected["runtime"]["observed_output"]["total_cost_gbp"] == final["total_cost_gbp"], "final total is not observed")
    document: dict[str, Any] = {
        "schema_version": SCHEMA,
        "source_commit": source_commit,
        "title": "A Saturday day trip under £100",
        "subtitle": "Two candidate Agents wait on independent travel data; the main Agent chooses from observed Guest outputs.",
        "provider": {
            "name": "DeepSeek", "model": "deepseek-v4-flash",
            "candidate_outputs": "exact recorded provider content replayed into the verified run",
            "selection_and_final": "live provider content", "raw_envelopes": "withheld", "reasoning_content": "withheld",
        },
        "input": {
            "task_summary": planning["task"], "public_system_instructions": system, "skills": skills,
            "tool_contracts": [
                {"name": f"travel.{name}", "kind": "deterministic delayed public fixture", "latency_ms": api["api_latency_ms"][name]}
                for name in ("weather", "rail", "attractions")
            ],
            "workspace_snapshot": {"request": request, "api_fixture": api},
            "private_fields_withheld": ["provider headers and request IDs", "reasoning content", "credentials", "private CAS paths", "unreviewed tool I/O"],
        },
        "groups": [
            {"id": "input", "label": "Public input", "icon": "inbox", "summary": "Task, system instructions, 3 skills, and deterministic travel API contracts."},
            {"id": "candidates", "label": "Candidate Agents", "icon": "split", "summary": "Brighton and Oxford generated constrained Python independently."},
            {"id": "runtime", "label": "Fresh Guest execution", "icon": "terminal", "summary": "Both private workspace attempts waited on 3 Host APIs and returned observed totals."},
            {"id": "decision", "label": "Main Agent decision", "icon": "check", "summary": "The main Agent compared £118.40 with £78.00, then selected Oxford."},
            {"id": "output", "label": "Final output", "icon": "flag", "summary": "Oxford itinerary and £78.00 observed total."},
        ],
        "agents": public_agents,
        "decision": {
            "model_output": result["selection"], "selected_candidate_id": branch["selected_candidate_id"],
            "discarded_candidate_ids": branch["discarded_candidate_ids"], "selected_root_sha256": branch["selected_root_sha256"],
        },
        "final_output": final,
        "privacy": {"public_projection": "allowlisted reviewed fields only", "private_recording": "experiment-full CAS", "credentials": "never recorded in public artifact"},
    }
    unsigned = json.dumps(document, separators=(",", ":"), ensure_ascii=False).encode()
    document["artifact_sha256"] = "sha256:" + hashlib.sha256(unsigned).hexdigest()
    return document


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--fixture", required=True, type=Path)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    result = load_json(args.input, private=True)
    document = project(result, args.fixture, args.source_commit)
    body = json.dumps(document, separators=(",", ":"), ensure_ascii=False) + "\n"
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.output.with_suffix(args.output.suffix + ".tmp")
    temporary.write_text(body)
    os.chmod(temporary, 0o644)
    os.replace(temporary, args.output)
    print(f"schema={SCHEMA} agents={len(document['agents'])} sha={document['artifact_sha256']}")


if __name__ == "__main__":
    main()
