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


def download(root: pathlib.Path) -> None:
    root.mkdir(parents=True, exist_ok=True)
    os.chmod(root, 0o700)
    specs = [
        ("codeact-50.json", "xingyaoww/code-act", "default", "codeact", 50),
        ("open-swe-10.json", "nvidia/Open-SWE-Traces", "openhands", "minimax_m25", 10),
    ]
    sources = []
    for filename, dataset, config, split, length in specs:
        query = urllib.parse.urlencode({"dataset": dataset, "config": config, "split": split, "offset": 0, "length": length})
        url = "https://datasets-server.huggingface.co/rows?" + query
        raw = urllib.request.urlopen(url, timeout=120).read()
        if len(json.loads(raw).get("rows", [])) != length:
            raise ValueError("short dataset response")
        path = root / filename
        path.write_bytes(raw)
        os.chmod(path, 0o600)
        sources.append({"dataset": dataset, "config": config, "split": split, "offset": 0, "length": length, "url": url,
                        "bytes": len(raw), "sha256": digest(raw), "file": filename})
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
    arguments = parser.parse_args()
    if arguments.command == "download":
        download(arguments.root)
    elif arguments.command == "analyze":
        analyze(arguments.root, arguments.public_output, arguments.private_output, arguments.limit)
    else:
        probe(arguments.selection, arguments.runner, arguments.artifact, arguments.manifest, arguments.source_commit,
              arguments.public_output, arguments.private_output)


if __name__ == "__main__":
    main()
