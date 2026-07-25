#!/usr/bin/env python3
"""Generate the pinned, safe BFCL v4 subset used by this repository.

This script never downloads data. Pass a checkout of the pinned Gorilla revision via
--source-root and an empty destination via --output-root.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
from pathlib import Path
from typing import Any

PINNED_REVISION = "6ea57973c7a6097fd7c5915698c54c17c5b1b6c8"
REPOSITORY = "https://github.com/ShishirPatil/gorilla"
DATA_REL = Path("berkeley-function-call-leaderboard/bfcl_eval/data")
SOURCE_PATHS = (
    Path("LICENSE"),
    DATA_REL / "BFCL_v4_parallel_multiple.json",
    DATA_REL / "possible_answer/BFCL_v4_parallel_multiple.json",
    DATA_REL / "BFCL_v4_multi_turn_base.json",
    DATA_REL / "possible_answer/BFCL_v4_multi_turn_base.json",
    DATA_REL / "multi_turn_func_doc/gorilla_file_system.json",
)
SENSITIVE_KEY = re.compile(r"password|token|secret|credential|passport|card", re.I)
SENSITIVE_VALUE = re.compile(r"bearer\s|api[_ -]?key|authorization", re.I)


def sha256(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def load_jsonl(path: Path) -> tuple[list[dict[str, Any]], list[bytes]]:
    rows: list[dict[str, Any]] = []
    raw: list[bytes] = []
    for line in path.read_bytes().splitlines():
        if not line.strip():
            continue
        rows.append(json.loads(line))
        raw.append(line)
    return rows, raw


def normalize_schema(value: Any) -> Any:
    if isinstance(value, list):
        return [normalize_schema(item) for item in value]
    if not isinstance(value, dict):
        return value
    result = {key: normalize_schema(item) for key, item in value.items()}
    aliases = {"dict": "object", "float": "number"}
    if isinstance(result.get("type"), str):
        result["type"] = aliases.get(result["type"], result["type"])
    return result


def contains_sensitive(value: Any) -> bool:
    if isinstance(value, dict):
        return any(SENSITIVE_KEY.search(str(key)) or contains_sensitive(item) for key, item in value.items())
    if isinstance(value, list):
        return any(contains_sensitive(item) for item in value)
    return isinstance(value, str) and bool(SENSITIVE_VALUE.search(value))


def supported_schema(schema: Any) -> bool:
    if not isinstance(schema, dict) or "$ref" in schema:
        return False
    allowed_types = {"array", "boolean", "integer", "null", "number", "object", "string"}
    type_value = schema.get("type")
    if type_value is not None:
        if isinstance(type_value, str):
            if type_value not in allowed_types:
                return False
        elif isinstance(type_value, list):
            if not type_value or any(not isinstance(item, str) or item not in allowed_types for item in type_value):
                return False
        else:
            return False
    properties = schema.get("properties", {})
    if not isinstance(properties, dict) or any(not supported_schema(value) for value in properties.values()):
        return False
    items = schema.get("items")
    if items is not None:
        if isinstance(items, list):
            if any(not supported_schema(value) for value in items):
                return False
        elif not supported_schema(items):
            return False
    for keyword in ("allOf", "anyOf", "oneOf", "prefixItems"):
        value = schema.get(keyword, [])
        if not isinstance(value, list) or any(not supported_schema(item) for item in value):
            return False
    additional = schema.get("additionalProperties")
    if isinstance(additional, dict) and not supported_schema(additional):
        return False
    return True


def safe_function_tools(row: dict[str, Any]) -> bool:
    tools = row.get("function")
    return isinstance(tools, list) and bool(tools) and all(
        isinstance(tool, dict)
        and isinstance(tool.get("parameters"), dict)
        and supported_schema(normalize_schema(tool["parameters"]))
        for tool in tools
    )


def rank(track: str, source_id: str) -> str:
    material = f"agent-python-runtime/bfcl-v4/{track}/v1:{source_id}".encode()
    return hashlib.sha256(material).hexdigest()


def pick(rows: list[dict[str, Any]], track: str, eligible) -> list[dict[str, Any]]:
    candidates = [row for row in rows if eligible(row)]
    candidates.sort(key=lambda row: (rank(track, row["id"]), row["id"]))
    if len(candidates) < 10:
        raise SystemExit(f"only {len(candidates)} eligible {track} tasks")
    return candidates[:10]


def split_for(index: int) -> str:
    return "dev" if index < 5 else "evaluation"


def tool_doc(row: dict[str, Any]) -> dict[str, Any]:
    output = {
        "name": row["name"],
        "description": row.get("description", ""),
        "parameters": normalize_schema(row["parameters"]),
    }
    if "response" in row:
        output["response"] = normalize_schema(row["response"])
    return output


def write_json(path: Path, value: Any) -> bytes:
    data = (json.dumps(value, indent=2, sort_keys=True, ensure_ascii=False) + "\n").encode()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return data


def source_ref(source_id: str, source_record: dict[str, Any], adaptation: str) -> dict[str, Any]:
    return {
        "benchmark": "BFCL",
        "version": "v4",
        "revision": PINNED_REVISION,
        "source_id": source_id,
        "record_sha256": sha256(canonical(source_record)),
        "license": "Apache-2.0",
        "adaptation": adaptation,
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True, type=Path)
    parser.add_argument("--output-root", required=True, type=Path)
    args = parser.parse_args()
    source_root = args.source_root.resolve()
    output_root = args.output_root.resolve()

    revision = subprocess.check_output(
        ["git", "-C", str(source_root), "rev-parse", "HEAD"], text=True
    ).strip()
    if revision != PINNED_REVISION:
        raise SystemExit(f"BFCL revision {revision} does not match pinned {PINNED_REVISION}")
    for relative in SOURCE_PATHS:
        if not (source_root / relative).is_file():
            raise SystemExit(f"missing source file: {relative}")
    if output_root.exists() and any(output_root.iterdir()):
        raise SystemExit(f"output root must be empty: {output_root}")
    output_root.mkdir(parents=True, exist_ok=True)

    data = source_root / DATA_REL
    stateless_rows, _ = load_jsonl(data / "BFCL_v4_parallel_multiple.json")
    stateless_answers, _ = load_jsonl(data / "possible_answer/BFCL_v4_parallel_multiple.json")
    stateless_answer_by_id = {row["id"]: row for row in stateless_answers}
    selected_stateless = pick(
        stateless_rows,
        "stateless_function_calling",
        lambda row: row.get("id") in stateless_answer_by_id
        and not contains_sensitive(row)
        and safe_function_tools(row),
    )

    stateful_rows, _ = load_jsonl(data / "BFCL_v4_multi_turn_base.json")
    stateful_answers, _ = load_jsonl(data / "possible_answer/BFCL_v4_multi_turn_base.json")
    stateful_answer_by_id = {row["id"]: row for row in stateful_answers}
    filesystem_docs, _ = load_jsonl(data / "multi_turn_func_doc/gorilla_file_system.json")
    filesystem_tools = [tool_doc(row) for row in filesystem_docs if row["name"] not in {"rm", "rmdir"}]

    def safe_stateful(row: dict[str, Any]) -> bool:
        answer = stateful_answer_by_id.get(row.get("id"))
        if answer is None or row.get("involved_classes") != ["GorillaFileSystem"]:
            return False
        if contains_sensitive(row.get("initial_config", {})):
            return False
        trace = json.dumps(answer.get("ground_truth", []), ensure_ascii=False)
        return "rm(" not in trace and "rmdir(" not in trace

    selected_stateful = pick(stateful_rows, "stateful_local_tools", safe_stateful)

    entries: list[dict[str, Any]] = []
    for track, selected in (
        ("stateless_function_calling", selected_stateless),
        ("stateful_local_tools", selected_stateful),
    ):
        for index, row in enumerate(selected):
            split = split_for(index)
            source_id = row["id"]
            answer = (
                stateless_answer_by_id[source_id]
                if track == "stateless_function_calling"
                else stateful_answer_by_id[source_id]
            )
            task_id = f"bfcl-v4-{track.replace('_', '-')}-{source_id}"
            task = {
                "version": "external-agentic-task/v1",
                "id": task_id,
                "split": split,
                "track": track,
                "source": source_ref(
                    source_id,
                    row,
                    "Normalized JSON Schema aliases; selected by fixed hash before any model run; no task semantics changed.",
                ),
                "interaction": {
                    "mode": "single_turn" if track == "stateless_function_calling" else "multi_turn",
                    "turns": row["question"],
                },
                "tools": (
                    [tool_doc(tool) for tool in row["function"]]
                    if track == "stateless_function_calling"
                    else filesystem_tools
                ),
                "environment": {
                    "kind": "stateless" if track == "stateless_function_calling" else "local_stateful",
                    "initial_state": {} if track == "stateless_function_calling" else row["initial_config"],
                },
                "oracle": {
                    "kind": "expected_call_trace",
                    "turns": answer["ground_truth"],
                },
                "safety": {
                    "network_disabled": True,
                    "real_world_effects": False,
                    "credentials": "none",
                },
            }
            relative = f"tasks/{split}/{task_id}.json"
            task_data = write_json(output_root / relative, task)
            entries.append(
                {
                    "id": task_id,
                    "path": relative,
                    "sha256": sha256(task_data),
                    "split": split,
                    "track": track,
                }
            )

    entries.sort(key=lambda entry: entry["path"])
    source_files = [
        {"path": relative.as_posix(), "sha256": sha256((source_root / relative).read_bytes())}
        for relative in SOURCE_PATHS
    ]
    manifest = {
        "version": "external-agentic-dataset-manifest/v1",
        "dataset_id": "agentic-external-bfcl-v4-safe-subset-v1",
        "selection_policy": (
            "Before model execution, rank source IDs by SHA-256 with a fixed public salt; select ten "
            "stateless parallel/multiple tasks and ten credential-free, network-free, non-destructive "
            "GorillaFileSystem tasks; first five per track are dev and remaining five evaluation."
        ),
        "sources": [
            {
                "benchmark": "BFCL",
                "version": "v4",
                "repository": REPOSITORY,
                "revision": PINNED_REVISION,
                "license": "Apache-2.0",
                "license_url": f"{REPOSITORY}/blob/{PINNED_REVISION}/LICENSE",
                "source_files": source_files,
            }
        ],
        "tasks": entries,
    }
    write_json(output_root / "manifest.json", manifest)
    print(json.dumps({"tasks": len(entries), "dev": 10, "evaluation": 10}, sort_keys=True))


if __name__ == "__main__":
    main()
