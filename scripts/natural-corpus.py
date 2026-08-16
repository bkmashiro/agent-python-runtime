#!/usr/bin/env python3
"""Download and deterministically census a bounded natural agent-program pilot."""

from __future__ import annotations

import argparse
import ast
import builtins
import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
from collections import Counter
from typing import Any


DOWNLOAD_SCHEMA = "pysolate.natural-corpus-download.v1"
CENSUS_SCHEMA = "pysolate.natural-corpus-census.v1"
PRIVATE_SCHEMA = "pysolate.natural-corpus-private-selection.v1"
PROBE_PRIVATE_SCHEMA = "pysolate.natural-corpus-private-probe.v1"
PROBE_PUBLIC_SCHEMA = "pysolate.natural-corpus-probe.v1"
SOURCE_SCHEMA = DOWNLOAD_SCHEMA
MANIFEST_SCHEMA = "pysolate.natural-corpus-manifest.v1"
OPPORTUNITY_SCHEMA = "pysolate.natural-corpus-opportunity.v1"
ITEM_STATES = ("included", "rejected", "unclassifiable", "truncated")
DATASET_CONTRACTS = {
    "xingyaoww/code-act": ("default", "codeact", "python_action", "apache-2.0"),
    "nvidia/Open-SWE-Traces": ("openhands", "minimax_m25", "tool_trajectory", "cc-by-4.0"),
}
MAX_SOURCE_BYTES = 16 * 1024 * 1024
MAX_CODE_BYTES = 64 * 1024
MAX_ACTIONS = 1000
EXECUTE_RE = re.compile(r"<execute>(.*?)</execute>", re.IGNORECASE | re.DOTALL)
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
SAFE_STDLIB = {
    "array", "bisect", "calendar", "collections", "csv", "datetime", "decimal", "fractions",
    "functools", "heapq", "itertools", "json", "math", "operator", "random", "re",
    "sqlite3", "statistics", "string", "time", "typing",
}
FORBIDDEN_NAMES = {"__import__", "compile", "eval", "exec", "input", "open"}
FORBIDDEN_IMPORTS = {"asyncio", "http", "multiprocessing", "os", "pathlib", "shutil", "socket", "subprocess", "urllib"}


def canonical_json(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n").encode()


def digest(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def _target_names(node: ast.AST) -> set[str]:
    names: set[str] = set()
    for child in ast.walk(node):
        if isinstance(child, ast.Name) and isinstance(child.ctx, (ast.Store, ast.Param)):
            names.add(child.id)
        elif isinstance(child, ast.arg):
            names.add(child.arg)
        elif isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            names.add(child.name)
        elif isinstance(child, ast.alias):
            names.add(child.asname or child.name.split(".")[0])
    return names


def classify_code(code: str) -> tuple[str, str, list[str], list[str]]:
    raw = code.encode()
    if not raw or len(raw) > MAX_CODE_BYTES or "\x00" in code:
        return "rejected", "source_bounds", [], []
    try:
        tree = ast.parse(code)
    except SyntaxError:
        return "rejected", "parse_error", [], []
    imports = sorted({alias.name.split(".")[0] for node in ast.walk(tree)
                      if isinstance(node, (ast.Import, ast.ImportFrom))
                      for alias in (node.names if isinstance(node, ast.Import) else [ast.alias(name=node.module or "")])
                      if alias.name})
    if any(name in FORBIDDEN_IMPORTS for name in imports):
        return "rejected", "forbidden_api", imports, []
    if any(name not in SAFE_STDLIB for name in imports):
        return "rejected", "third_party_import", imports, []
    loads = {node.id for node in ast.walk(tree) if isinstance(node, ast.Name) and isinstance(node.ctx, ast.Load)}
    if loads & FORBIDDEN_NAMES:
        return "rejected", "forbidden_api", imports, []
    defined = _target_names(tree) | set(dir(builtins))
    unresolved = sorted(loads - defined)
    if unresolved:
        return "rejected", "environment_dependency", imports, unresolved[:16]
    return "probe_candidate", "", imports, []


def extract_codeact(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    items: list[dict[str, Any]] = []
    for outer in rows:
        row = outer.get("row")
        if not isinstance(row, dict) or not isinstance(row.get("id"), str) or not isinstance(row.get("conversations"), list):
            raise ValueError("invalid CodeAct row")
        action_index = 0
        for message in row["conversations"]:
            if not isinstance(message, dict) or message.get("role") != "assistant" or not isinstance(message.get("content"), str):
                continue
            for match in EXECUTE_RE.finditer(message["content"]):
                action_index += 1
                if len(items) >= MAX_ACTIONS:
                    raise ValueError("too many CodeAct actions")
                code = match.group(1).strip()
                status, reason, imports, unresolved = classify_code(code)
                tree = ast.parse(code) if status == "probe_candidate" else None
                top_level_execution = bool(tree and any(not isinstance(node, (ast.Import, ast.ImportFrom, ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.Pass)) for node in tree.body))
                record_sha = digest(row["id"].encode())
                item_id = digest((record_sha + "\x00" + str(action_index)).encode())
                public = {
                    "item_id": item_id,
                    "record_sha256": record_sha,
                    "action_index": action_index,
                    "code_sha256": digest(code.encode()),
                    "code_bytes": len(code.encode()),
                    "imports": imports,
                    "status": status,
                    "reason": reason,
                    "unresolved_name_count": len(unresolved),
                    "top_level_execution": top_level_execution,
                }
                items.append({"code": code, "status": status, "reason": reason, "imports": imports, "unresolved": unresolved, "public": public})
    return items


def summarize_open_swe(rows: list[dict[str, Any]]) -> dict[str, Any]:
    languages: Counter[str] = Counter()
    outcomes: Counter[str] = Counter()
    tool_calls = 0
    python_resolved = 0
    for outer in rows:
        row = outer.get("row")
        if not isinstance(row, dict) or not isinstance(row.get("language"), str) or not isinstance(row.get("trajectory"), list):
            raise ValueError("invalid Open-SWE row")
        language = row["language"].lower()
        outcome = str(row.get("resolved"))
        languages[language] += 1
        outcomes[outcome] += 1
        if language == "python" and row.get("resolved") == 1:
            python_resolved += 1
        for message in row["trajectory"]:
            if isinstance(message, dict) and isinstance(message.get("tool_calls"), list):
                tool_calls += len(message["tool_calls"])
    return {
        "languages": dict(sorted(languages.items())),
        "outcomes": dict(sorted(outcomes.items())),
        "python_trajectories": languages["python"],
        "python_resolved": python_resolved,
        "tool_calls": tool_calls,
    }


def load_source_bundle(root: pathlib.Path) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    if not root.is_dir() or root.stat().st_mode & 0o077:
        raise ValueError("private source root must be mode 0700")
    manifest = json.loads((root / "download-manifest.json").read_text())
    if manifest.get("schema_version") != DOWNLOAD_SCHEMA or not isinstance(manifest.get("sources"), list) or len(manifest["sources"]) != 2:
        raise ValueError("invalid download manifest")
    decoded: dict[str, dict[str, Any]] = {}
    for source in manifest["sources"]:
        if not isinstance(source, dict) or not DIGEST_RE.fullmatch(str(source.get("sha256", ""))):
            raise ValueError("invalid source descriptor")
        filename = source.get("file")
        if not isinstance(filename, str) or pathlib.PurePath(filename).name != filename:
            raise ValueError("invalid source filename")
        raw = (root / filename).read_bytes()
        if len(raw) == 0 or len(raw) > MAX_SOURCE_BYTES or len(raw) != source.get("bytes") or digest(raw) != source["sha256"]:
            raise ValueError("source identity mismatch")
        value = json.loads(raw)
        if not isinstance(value.get("rows"), list) or len(value["rows"]) != source.get("length"):
            raise ValueError("source row count mismatch")
        decoded[str(source.get("dataset"))] = value
    try:
        return manifest, decoded["xingyaoww/code-act"], decoded["nvidia/Open-SWE-Traces"]
    except KeyError as error:
        raise ValueError("required source missing") from error


def build_census(source_manifest: dict[str, Any], codeact: dict[str, Any], open_swe: dict[str, Any], limit: int) -> tuple[dict[str, Any], dict[str, Any]]:
    if limit < 1 or limit > 32:
        raise ValueError("invalid selection limit")
    codeact_rows = codeact.get("rows")
    open_swe_rows = open_swe.get("rows")
    if not isinstance(codeact_rows, list) or not isinstance(open_swe_rows, list):
        raise ValueError("invalid source rows")
    items = extract_codeact(codeact_rows)
    reason_counts = Counter(item["reason"] or "included" for item in items)
    import_counts = Counter(name for item in items for name in item["imports"])
    probe_candidates = [item for item in items if item["status"] == "probe_candidate"]
    selected_items = sorted(enumerate(probe_candidates), key=lambda pair: (
        not pair[1]["public"]["top_level_execution"],
        bool(pair[1]["imports"]),
        pair[0],
    ))[:limit]
    selected_items = [item for _, item in selected_items]
    public_sources = [{key: source[key] for key in ("dataset", "config", "split", "offset", "length", "bytes", "sha256")}
                      for source in source_manifest["sources"]]
    report = {
        "schema_version": CENSUS_SCHEMA,
        "sources": public_sources,
        "denominators": {
            "codeact_records": len(codeact_rows),
            "codeact_actions": len(items),
            "open_swe_trajectories": len(open_swe_rows),
        },
        "codeact": {
            "probe_candidates": reason_counts["included"],
            "reasons": dict(sorted(reason_counts.items())),
            "imports": dict(sorted(import_counts.items())),
            "items": [item["public"] for item in items],
        },
        "open_swe": summarize_open_swe(open_swe_rows),
        "selected_probe_count": len(selected_items),
        "limitations": [
            "Open-SWE pilot is the first ten mixed-language rows, not a Python-only cohort.",
            "CodeAct probe_candidate is static compatibility screening, not task success.",
            "No model judgement or reconstructed missing trajectory state is used.",
        ],
    }
    report_sha = digest(canonical_json(report))
    selection = {
        "schema_version": PRIVATE_SCHEMA,
        "public_report_sha256": report_sha,
        "programs": [{**item["public"], "code": item["code"]} for item in selected_items],
    }
    return report, selection


def _manifest_identity(manifest: dict[str, Any]) -> str:
    return digest(canonical_json({key: value for key, value in manifest.items() if key != "identity"}))


def _public_sources(source_manifest: dict[str, Any]) -> list[dict[str, Any]]:
    return [{key: source[key] for key in ("dataset", "config", "split", "offset", "length", "bytes", "sha256", "dataset_revision_observed", "license_id")}
            for source in source_manifest["sources"]]


def build_corpus_manifest(codeact_rows: list[dict[str, Any]], open_swe_rows: list[dict[str, Any]], source_manifest: dict[str, Any], completed_probe_ids: set[str]) -> dict[str, Any]:
    items: list[dict[str, Any]] = []
    truncated_records = {digest(str(outer["row"]["id"]).encode()) for outer in codeact_rows if outer.get("truncated_cells")}
    for action in extract_codeact(codeact_rows):
        public = action["public"]
        state = "included" if action["status"] == "probe_candidate" else "rejected"
        reason = "static_compatible" if state == "included" else action["reason"]
        if public["record_sha256"] in truncated_records:
            state, reason = "truncated", "dataset_cell_truncated"
        items.append({
            "item_id": public["item_id"], "dataset": "xingyaoww/code-act", "item_kind": "python_action",
            "source_record_sha256": public["record_sha256"], "source_index": public["action_index"],
            "source_sha256": public["code_sha256"], "source_bytes": public["code_bytes"],
            "provenance_class": "public_dataset", "collection_adapter": "hf_rows_api_v1",
            "oracle_class": "not_available", "privacy_class": "public_body_private_cache",
            "authority_class": "not_recorded", "expected_backend": "cpython_wasi" if state == "included" else "not_eligible",
            "state": state, "reason": reason, "imports": public["imports"],
            "probe_state": "completed" if public["item_id"] in completed_probe_ids else "not_run",
        })
    for outer in open_swe_rows:
        row = outer["row"]
        language = str(row.get("language", "")).lower()
        resolved = row.get("resolved")
        trajectory_bytes = canonical_json(row.get("trajectory", []))
        record_sha = digest(str(row.get("instance_id", "")).encode())
        if outer.get("truncated_cells"):
            state, reason = "truncated", "dataset_cell_truncated"
        elif language == "python" and resolved in (0, 1):
            state, reason = "included", "python_with_resolved_oracle"
        elif language == "python":
            state, reason = "unclassifiable", "python_oracle_unavailable"
        else:
            state, reason = "rejected", "language_not_python"
        items.append({
            "item_id": digest(("open_swe\x00" + record_sha).encode()), "dataset": "nvidia/Open-SWE-Traces", "item_kind": "tool_trajectory",
            "source_record_sha256": record_sha, "source_index": int(outer.get("row_idx", 0)),
            "source_sha256": digest(trajectory_bytes), "source_bytes": len(trajectory_bytes),
            "provenance_class": "public_dataset", "collection_adapter": "hf_rows_api_v1",
            "oracle_class": "swe_resolved" if resolved in (0, 1) else "not_available",
            "privacy_class": "public_body_private_cache", "authority_class": "not_recorded",
            "expected_backend": "host_tool_trajectory" if state == "included" else "not_eligible",
            "state": state, "reason": reason, "imports": [], "probe_state": "not_run",
        })
    items.sort(key=lambda item: item["item_id"])
    states = Counter(item["state"] for item in items)
    manifest = {
        "schema_version": MANIFEST_SCHEMA,
        "sources": _public_sources(source_manifest),
        "denominator": {
            "items": len(items), "codeact_actions": sum(item["dataset"] == "xingyaoww/code-act" for item in items),
            "open_swe_trajectories": sum(item["dataset"] == "nvidia/Open-SWE-Traces" for item in items),
            "states": {state: states.get(state, 0) for state in ITEM_STATES},
        },
        "items": items,
    }
    manifest["identity"] = _manifest_identity(manifest)
    validate_corpus_manifest(manifest)
    return manifest


def validate_corpus_manifest(manifest: dict[str, Any]) -> None:
    if set(manifest) != {"schema_version", "identity", "sources", "denominator", "items"} or manifest.get("schema_version") != MANIFEST_SCHEMA:
        raise ValueError("invalid corpus manifest envelope")
    if not DIGEST_RE.fullmatch(str(manifest.get("identity", ""))) or manifest["identity"] != _manifest_identity(manifest):
        raise ValueError("corpus manifest identity mismatch")
    items = manifest.get("items")
    if not isinstance(items, list) or len(items) > MAX_ACTIONS:
        raise ValueError("invalid corpus item count")
    sources = manifest.get("sources")
    source_keys = {"dataset", "config", "split", "offset", "length", "bytes", "sha256", "dataset_revision_observed", "license_id"}
    if not isinstance(sources, list) or len(sources) != len(DATASET_CONTRACTS):
        raise ValueError("invalid corpus sources")
    seen_sources: set[str] = set()
    for source in sources:
        if not isinstance(source, dict) or set(source) != source_keys or source.get("dataset") not in DATASET_CONTRACTS:
            raise ValueError("invalid corpus source descriptor")
        config, split, _, license_id = DATASET_CONTRACTS[source["dataset"]]
        if source["config"] != config or source["split"] != split or source["license_id"] != license_id or source["dataset"] in seen_sources or not DIGEST_RE.fullmatch(str(source["sha256"])) or not re.fullmatch(r"[0-9a-f]{40}", str(source["dataset_revision_observed"])):
            raise ValueError("invalid corpus source identity")
        if not all(isinstance(source[key], int) and source[key] >= 0 for key in ("offset", "length", "bytes")) or source["length"] > MAX_ACTIONS or source["bytes"] > MAX_SOURCE_BYTES:
            raise ValueError("invalid corpus source bounds")
        seen_sources.add(source["dataset"])
    expected_keys = {"item_id", "dataset", "item_kind", "source_record_sha256", "source_index", "source_sha256", "source_bytes", "provenance_class", "collection_adapter", "oracle_class", "privacy_class", "authority_class", "expected_backend", "state", "reason", "imports", "probe_state"}
    ids: list[str] = []
    for item in items:
        if not isinstance(item, dict) or set(item) != expected_keys:
            raise ValueError("invalid corpus item fields")
        if item["state"] not in ITEM_STATES or not all(DIGEST_RE.fullmatch(str(item[key])) for key in ("item_id", "source_record_sha256", "source_sha256")):
            raise ValueError("invalid corpus item identity or state")
        if not isinstance(item["source_index"], int) or item["source_index"] < 0 or not isinstance(item["source_bytes"], int) or not 0 <= item["source_bytes"] <= 4 * 1024 * 1024:
            raise ValueError("invalid corpus source bounds")
        if not isinstance(item["reason"], str) or not item["reason"] or len(item["reason"]) > 128:
            raise ValueError("invalid corpus item reason")
        if not isinstance(item["imports"], list) or item["imports"] != sorted(set(item["imports"])) or len(item["imports"]) > 128:
            raise ValueError("invalid corpus imports")
        if item["dataset"] not in DATASET_CONTRACTS or item["item_kind"] != DATASET_CONTRACTS[item["dataset"]][2]:
            raise ValueError("invalid corpus dataset class")
        if item["provenance_class"] != "public_dataset" or item["collection_adapter"] != "hf_rows_api_v1" or item["privacy_class"] != "public_body_private_cache" or item["authority_class"] != "not_recorded" or item["probe_state"] not in ("completed", "not_run"):
            raise ValueError("invalid corpus policy class")
        if item["dataset"] == "xingyaoww/code-act":
            expected_id = digest((item["source_record_sha256"] + "\x00" + str(item["source_index"])).encode())
            if item["oracle_class"] != "not_available" or item["expected_backend"] != ("cpython_wasi" if item["state"] == "included" else "not_eligible"):
                raise ValueError("invalid CodeAct contract")
        else:
            expected_id = digest(("open_swe\x00" + item["source_record_sha256"]).encode())
            if item["oracle_class"] not in ("swe_resolved", "not_available") or item["expected_backend"] != ("host_tool_trajectory" if item["state"] == "included" else "not_eligible") or item["imports"]:
                raise ValueError("invalid Open-SWE contract")
        if item["item_id"] != expected_id or item["probe_state"] == "completed" and (item["dataset"] != "xingyaoww/code-act" or item["state"] != "included"):
            raise ValueError("corpus item binding mismatch")
        ids.append(item["item_id"])
    if ids != sorted(ids) or len(ids) != len(set(ids)):
        raise ValueError("corpus item identities must be unique and sorted")
    counts = Counter(item["state"] for item in items)
    denominator = manifest.get("denominator", {})
    if denominator.get("items") != len(items) or denominator.get("codeact_actions") != sum(item["dataset"] == "xingyaoww/code-act" for item in items) or denominator.get("open_swe_trajectories") != sum(item["dataset"] == "nvidia/Open-SWE-Traces" for item in items) or denominator.get("states") != {state: counts.get(state, 0) for state in ITEM_STATES}:
        raise ValueError("corpus denominator mismatch")
    source_lengths = {source["dataset"]: source["length"] for source in sources}
    if source_lengths["nvidia/Open-SWE-Traces"] != denominator["open_swe_trajectories"]:
        raise ValueError("Open-SWE source denominator mismatch")
    encoded = canonical_json(manifest).decode()
    if any(marker in encoded for marker in ('/Users/', '~/.hermes', 'file://', '"code":', '"body":', '"trajectory":', '"conversations":', '"patch":', '"path":', '"url":')):
        raise ValueError("private path or body leaked into corpus manifest")


def build_opportunity_report(manifest_identity: str, codeact_rows: list[dict[str, Any]], open_swe_rows: list[dict[str, Any]]) -> dict[str, Any]:
    if not DIGEST_RE.fullmatch(manifest_identity):
        raise ValueError("invalid manifest identity")
    code_actions: list[tuple[str, str]] = []
    per_codeact: list[list[str]] = []
    for outer in codeact_rows:
        record_sha = digest(str(outer["row"]["id"]).encode())
        hashes: list[str] = []
        for message in outer["row"].get("conversations", []):
            if message.get("role") != "assistant":
                continue
            for match in EXECUTE_RE.finditer(message.get("content", "")):
                code_sha = digest(match.group(1).strip().encode())
                hashes.append(code_sha)
                code_actions.append((record_sha, code_sha))
        per_codeact.append(hashes)
    code_counts = Counter(value for _, value in code_actions)
    cross_record_code = sum(1 for value, count in code_counts.items()
                            if count > 1 and len({record for record, candidate in code_actions if candidate == value}) > 1)

    bash_calls: list[tuple[str, str]] = []
    per_trajectory: list[list[str]] = []
    parallel_messages = 0
    parallel_exact_duplicates = 0
    for outer in open_swe_rows:
        record_sha = digest(str(outer["row"].get("instance_id", "")).encode())
        commands: list[str] = []
        for message in outer["row"].get("trajectory", []):
            message_commands: list[str] = []
            for call in message.get("tool_calls") or []:
                function = call.get("function") or {}
                if function.get("name") != "execute_bash":
                    continue
                try:
                    arguments = json.loads(function.get("arguments") or "{}")
                except (TypeError, json.JSONDecodeError):
                    continue
                command = arguments.get("command")
                if isinstance(command, str):
                    normalized = re.sub(r"\s+", " ", command).strip()
                    commands.append(normalized)
                    message_commands.append(normalized)
                    bash_calls.append((record_sha, normalized))
            if len(message_commands) > 1:
                parallel_messages += 1
                parallel_exact_duplicates += len(message_commands) - len(set(message_commands))
        per_trajectory.append(commands)
    bash_counts = Counter(command for _, command in bash_calls)
    cross_trajectory_commands = sum(1 for command, count in bash_counts.items()
                                    if count > 1 and len({record for record, candidate in bash_calls if candidate == command}) > 1)
    report = {
        "schema_version": OPPORTUNITY_SCHEMA,
        "manifest_identity": manifest_identity,
        "codeact": {
            "actions": len(code_actions), "exact_unique_actions": len(code_counts),
            "exact_duplicate_instances": sum(count - 1 for count in code_counts.values()),
            "cross_record_duplicate_groups": cross_record_code,
            "records_with_sequential_duplicates": sum(len(values) != len(set(values)) for values in per_codeact),
            "overlap_evidence": "not_recorded",
        },
        "open_swe": {
            "bash_calls": len(bash_calls), "exact_unique_bash_calls": len(bash_counts),
            "exact_duplicate_bash_instances": sum(count - 1 for count in bash_counts.values()),
            "sequential_duplicate_bash_calls": sum(len(values) - len(set(values)) for values in per_trajectory),
            "cross_trajectory_duplicate_groups": cross_trajectory_commands,
            "parallel_bash_messages": parallel_messages, "parallel_exact_duplicate_calls": parallel_exact_duplicates,
        },
        "sharing_gate": {
            "verdict": "insufficient_evidence", "recommendation": "do_not_implement_sharing_pass",
            "source_equivalence": "partially_observed", "overlap": "not_observed",
            "authority_equivalence": "not_recorded", "workspace_base_equivalence": "not_recorded",
            "reason": "duplicates are sequential retries or isolated repeats without overlap and Host-owned equivalence evidence",
        },
    }
    validate_opportunity_report(report)
    return report


def validate_opportunity_report(report: dict[str, Any]) -> None:
    if set(report) != {"schema_version", "manifest_identity", "codeact", "open_swe", "sharing_gate"} or report.get("schema_version") != OPPORTUNITY_SCHEMA or not DIGEST_RE.fullmatch(str(report.get("manifest_identity", ""))):
        raise ValueError("invalid opportunity report envelope")
    codeact = report.get("codeact")
    open_swe = report.get("open_swe")
    gate = report.get("sharing_gate")
    if not isinstance(codeact, dict) or set(codeact) != {"actions", "exact_unique_actions", "exact_duplicate_instances", "cross_record_duplicate_groups", "records_with_sequential_duplicates", "overlap_evidence"}:
        raise ValueError("invalid CodeAct opportunity fields")
    if not isinstance(open_swe, dict) or set(open_swe) != {"bash_calls", "exact_unique_bash_calls", "exact_duplicate_bash_instances", "sequential_duplicate_bash_calls", "cross_trajectory_duplicate_groups", "parallel_bash_messages", "parallel_exact_duplicate_calls"}:
        raise ValueError("invalid Open-SWE opportunity fields")
    if not all(isinstance(value, int) and value >= 0 for key, value in codeact.items() if key != "overlap_evidence") or codeact["overlap_evidence"] != "not_recorded":
        raise ValueError("invalid CodeAct opportunity counts")
    if not all(isinstance(value, int) and value >= 0 for value in open_swe.values()):
        raise ValueError("invalid Open-SWE opportunity counts")
    if codeact["actions"] - codeact["exact_unique_actions"] != codeact["exact_duplicate_instances"] or codeact["cross_record_duplicate_groups"] > codeact["exact_duplicate_instances"]:
        raise ValueError("inconsistent CodeAct opportunity counts")
    if open_swe["bash_calls"] - open_swe["exact_unique_bash_calls"] != open_swe["exact_duplicate_bash_instances"] or open_swe["sequential_duplicate_bash_calls"] > open_swe["exact_duplicate_bash_instances"] or open_swe["parallel_exact_duplicate_calls"] > open_swe["exact_duplicate_bash_instances"]:
        raise ValueError("inconsistent Open-SWE opportunity counts")
    expected_gate = {
        "verdict": "insufficient_evidence", "recommendation": "do_not_implement_sharing_pass",
        "source_equivalence": "partially_observed", "overlap": "not_observed",
        "authority_equivalence": "not_recorded", "workspace_base_equivalence": "not_recorded",
        "reason": "duplicates are sequential retries or isolated repeats without overlap and Host-owned equivalence evidence",
    }
    if gate != expected_gate:
        raise ValueError("invalid sharing gate")
    encoded = canonical_json(report).decode()
    if any(marker in encoded for marker in ('/Users/', '~/.hermes', 'file://', '"code":', '"body":', '"command":', '"trajectory":')):
        raise ValueError("private path or body leaked into opportunity report")


def download(root: pathlib.Path) -> None:
    root.mkdir(parents=True, exist_ok=True)
    os.chmod(root, 0o700)
    specs = [
        ("codeact-50.json", "xingyaoww/code-act", "default", "codeact", 50),
        ("open-swe-10.json", "nvidia/Open-SWE-Traces", "openhands", "minimax_m25", 10),
    ]
    sources = []
    for filename, dataset, config, split, length in specs:
        metadata_url = "https://huggingface.co/api/datasets/" + dataset
        metadata = json.loads(urllib.request.urlopen(metadata_url, timeout=30).read())
        revision = metadata.get("sha")
        license_id = (metadata.get("cardData") or {}).get("license")
        expected_license = DATASET_CONTRACTS[dataset][3]
        if not re.fullmatch(r"[0-9a-f]{40}", str(revision)) or license_id != expected_license:
            raise ValueError("dataset revision or license metadata mismatch")
        query = urllib.parse.urlencode({"dataset": dataset, "config": config, "split": split, "offset": 0, "length": length})
        url = "https://datasets-server.huggingface.co/rows?" + query
        raw = urllib.request.urlopen(url, timeout=120).read()
        if len(json.loads(raw).get("rows", [])) != length:
            raise ValueError("short dataset response")
        path = root / filename
        path.write_bytes(raw)
        os.chmod(path, 0o600)
        sources.append({"dataset": dataset, "config": config, "split": split, "offset": 0, "length": length, "url": url,
                        "bytes": len(raw), "sha256": digest(raw), "file": filename,
                        "dataset_revision_observed": revision, "license_id": license_id})
    path = root / "download-manifest.json"
    path.write_bytes(canonical_json({"schema_version": DOWNLOAD_SCHEMA, "sources": sources}))
    os.chmod(path, 0o600)


def analyze(root: pathlib.Path, public_output: pathlib.Path, private_output: pathlib.Path, limit: int) -> None:
    source_manifest, codeact, open_swe = load_source_bundle(root)
    report, selection = build_census(source_manifest, codeact, open_swe, limit)
    public_output.parent.mkdir(parents=True, exist_ok=True)
    public_output.write_bytes(canonical_json(report))
    private_output.write_bytes(canonical_json(selection))
    os.chmod(private_output, 0o600)


def _probe_program(code: str, timeout: float = 3.0) -> dict[str, Any]:
    probe_code = code + "\nresult={'probe':'completed'}"
    started = time.monotonic()
    try:
        completed = subprocess.run(
            [sys.executable, "-I", "-c", probe_code], check=False, capture_output=True,
            text=True, timeout=timeout, env={"PATH": os.environ.get("PATH", "")},
        )
        return {
            "status": "ok" if completed.returncode == 0 else "failed",
            "exit_code": completed.returncode,
            "elapsed_ms": round((time.monotonic() - started) * 1000, 3),
            "stdout": completed.stdout[-32768:],
            "stderr": completed.stderr[-32768:],
        }
    except subprocess.TimeoutExpired as error:
        return {
            "status": "timed_out", "exit_code": None,
            "elapsed_ms": round((time.monotonic() - started) * 1000, 3),
            "stdout": (error.stdout or "")[-32768:] if isinstance(error.stdout, str) else "",
            "stderr": (error.stderr or "")[-32768:] if isinstance(error.stderr, str) else "",
        }


def run_probes(selection: dict[str, Any], runner: pathlib.Path, artifact: pathlib.Path, manifest: pathlib.Path, source_commit: str) -> tuple[dict[str, Any], dict[str, Any]]:
    if selection.get("schema_version") != PRIVATE_SCHEMA or not DIGEST_RE.fullmatch(str(selection.get("public_report_sha256", ""))):
        raise ValueError("invalid private selection")
    programs = selection.get("programs")
    if not isinstance(programs, list) or not 1 <= len(programs) <= 32:
        raise ValueError("invalid selected programs")
    if not re.fullmatch(r"[0-9a-f]{40}", source_commit):
        raise ValueError("invalid source commit")
    for path in (runner, artifact, manifest):
        if not path.is_file():
            raise ValueError("probe dependency missing")
    artifact_sha = digest(artifact.read_bytes())
    manifest_sha = digest(manifest.read_bytes())
    runner_sha = digest(runner.read_bytes())
    private_programs = []
    public_programs = []
    for index, program in enumerate(programs, 1):
        if not isinstance(program, dict) or program.get("status") != "probe_candidate" or not isinstance(program.get("code"), str):
            raise ValueError("invalid selected program")
        code = program["code"]
        if len(code.encode()) != program.get("code_bytes") or digest(code.encode()) != program.get("code_sha256"):
            raise ValueError("selected code identity mismatch")
        imports = program.get("imports")
        if not isinstance(imports, list) or any(not isinstance(name, str) for name in imports):
            raise ValueError("invalid selected imports")
        baseline = _probe_program(code)
        request = {"run_id": f"natural-probe-{index:04d}", "code": code + "\nresult={'probe':'completed'}", "inputs": {}}
        allowed_imports = imports or ["json"]
        config = {
            "timeout_ms": 5000, "max_request_bytes": 1048576, "max_response_bytes": 1048576,
            "execution_profile": {"id": "base", "allowed_imports": allowed_imports},
        }
        with tempfile.TemporaryDirectory(prefix="natural-probe-") as directory:
            request_path = pathlib.Path(directory) / "request.json"
            config_path = pathlib.Path(directory) / "config.json"
            request_path.write_bytes(canonical_json(request)); config_path.write_bytes(canonical_json(config))
            started = time.monotonic()
            try:
                with request_path.open("rb") as stdin:
                    completed = subprocess.run(
                        [str(runner), "-artifact", str(artifact), "-manifest", str(manifest), "-config", str(config_path)],
                        stdin=stdin, capture_output=True, timeout=10,
                    )
                guest_elapsed = round((time.monotonic() - started) * 1000, 3)
                stdout = completed.stdout[-1048576:]
                stderr = completed.stderr[-32768:]
                try:
                    response = json.loads(stdout) if stdout else None
                except json.JSONDecodeError:
                    response = None
                guest_status = str(response.get("status")) if completed.returncode == 0 and isinstance(response, dict) else ("failed" if completed.returncode != 124 else "timed_out")
            except subprocess.TimeoutExpired as error:
                completed = None
                guest_elapsed = round((time.monotonic() - started) * 1000, 3)
                stdout = error.stdout or b""; stderr = error.stderr or b""; response = None; guest_status = "timed_out"
        result_sha = digest(canonical_json(response.get("result"))) if isinstance(response, dict) and "result" in response else ""
        public_programs.append({
            "item_id": program["item_id"], "code_sha256": program["code_sha256"], "code_bytes": program["code_bytes"],
            "imports": imports, "oracle_class": "completion_only", "baseline_status": baseline["status"],
            "guest_status": guest_status, "profile_bound": True, "result_sha256": result_sha,
            "baseline_elapsed_ms": baseline["elapsed_ms"], "guest_elapsed_ms": guest_elapsed,
        })
        private_programs.append({
            **public_programs[-1], "code": code, "baseline": baseline,
            "guest_exit_code": completed.returncode if completed is not None else None,
            "guest_stdout": stdout.decode(errors="replace"), "guest_stderr": stderr.decode(errors="replace"),
            "guest_response": response,
        })
    identity = {
        "source_commit": source_commit, "artifact_sha256": artifact_sha,
        "artifact_manifest_sha256": manifest_sha, "runner_sha256": runner_sha,
        "selection_sha256": selection["public_report_sha256"],
    }
    public = {
        "schema_version": PROBE_PUBLIC_SCHEMA, "identity": identity,
        "aggregate": {
            "programs": len(public_programs),
            "baseline_ok": sum(program["baseline_status"] == "ok" for program in public_programs),
            "guest_ok": sum(program["guest_status"] == "ok" for program in public_programs),
            "matched_completion": sum(program["baseline_status"] == "ok" and program["guest_status"] == "ok" for program in public_programs),
        },
        "programs": public_programs,
        "limitations": [
            "completion_only checks bounded execution compatibility, not original task correctness",
            "profile_bound means Host-bound source/import profile; no source-bound tool decision is claimed",
            "timings are single-run smoke observations, not performance evidence",
        ],
    }
    private = {"schema_version": PROBE_PRIVATE_SCHEMA, "identity": identity, "programs": private_programs}
    return private, public


def probe(selection_path: pathlib.Path, runner: pathlib.Path, artifact: pathlib.Path, manifest: pathlib.Path,
          source_commit: str, public_output: pathlib.Path, private_output: pathlib.Path) -> None:
    selection = json.loads(selection_path.read_text())
    private, public = run_probes(selection, runner, artifact, manifest, source_commit)
    public_output.parent.mkdir(parents=True, exist_ok=True)
    public_output.write_bytes(canonical_json(public))
    private_output.write_bytes(canonical_json(private)); os.chmod(private_output, 0o600)


def generate_contract(root: pathlib.Path, probe_path: pathlib.Path, manifest_output: pathlib.Path, opportunity_output: pathlib.Path) -> None:
    source_manifest, codeact, open_swe = load_source_bundle(root)
    probe_report = json.loads(probe_path.read_text())
    if probe_report.get("schema_version") != PROBE_PUBLIC_SCHEMA or not isinstance(probe_report.get("programs"), list):
        raise ValueError("invalid public probe report")
    completed = {program["item_id"] for program in probe_report["programs"]
                 if isinstance(program, dict) and program.get("guest_status") == "ok" and DIGEST_RE.fullmatch(str(program.get("item_id", "")))}
    manifest = build_corpus_manifest(codeact["rows"], open_swe["rows"], source_manifest, completed)
    opportunity = build_opportunity_report(manifest["identity"], codeact["rows"], open_swe["rows"])
    manifest_output.parent.mkdir(parents=True, exist_ok=True)
    opportunity_output.parent.mkdir(parents=True, exist_ok=True)
    manifest_output.write_bytes(canonical_json(manifest))
    opportunity_output.write_bytes(canonical_json(opportunity))


def main() -> None:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    download_parser = subparsers.add_parser("download")
    download_parser.add_argument("--root", type=pathlib.Path, required=True)
    analyze_parser = subparsers.add_parser("analyze")
    analyze_parser.add_argument("--root", type=pathlib.Path, required=True)
    analyze_parser.add_argument("--public-output", type=pathlib.Path, required=True)
    analyze_parser.add_argument("--private-output", type=pathlib.Path, required=True)
    analyze_parser.add_argument("--limit", type=int, default=8)
    probe_parser = subparsers.add_parser("probe")
    probe_parser.add_argument("--selection", type=pathlib.Path, required=True)
    probe_parser.add_argument("--runner", type=pathlib.Path, required=True)
    probe_parser.add_argument("--artifact", type=pathlib.Path, required=True)
    probe_parser.add_argument("--manifest", type=pathlib.Path, required=True)
    probe_parser.add_argument("--source-commit", required=True)
    probe_parser.add_argument("--public-output", type=pathlib.Path, required=True)
    probe_parser.add_argument("--private-output", type=pathlib.Path, required=True)
    contract_parser = subparsers.add_parser("contract")
    contract_parser.add_argument("--root", type=pathlib.Path, required=True)
    contract_parser.add_argument("--probe", type=pathlib.Path, required=True)
    contract_parser.add_argument("--manifest-output", type=pathlib.Path, required=True)
    contract_parser.add_argument("--opportunity-output", type=pathlib.Path, required=True)
    arguments = parser.parse_args()
    if arguments.command == "download":
        download(arguments.root)
    elif arguments.command == "analyze":
        analyze(arguments.root, arguments.public_output, arguments.private_output, arguments.limit)
    elif arguments.command == "probe":
        probe(arguments.selection, arguments.runner, arguments.artifact, arguments.manifest, arguments.source_commit,
              arguments.public_output, arguments.private_output)
    else:
        generate_contract(arguments.root, arguments.probe, arguments.manifest_output, arguments.opportunity_output)


if __name__ == "__main__":
    main()
