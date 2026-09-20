#!/usr/bin/env python3
"""Qualify the committed agent-core profile against a real Guest artifact."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_PROFILE = ROOT / "guest/build/profiles/agent-core.json"
DEFAULT_GUEST = ROOT / "dist/pysolate.wasm"

OPERATIONS = {
    "json_roundtrip": """
import json
value = json.loads(json.dumps({"answer": 42}, sort_keys=True))["answer"]
assert value == 42
""",
    "csv_roundtrip": """
import csv, io
value = list(csv.reader(io.StringIO("name,value\\nalpha,7\\n")))[1]
assert value == ["alpha", "7"]
""",
    "text_processing": """
import textwrap
value = textwrap.dedent("    alpha\\n    beta\\n").splitlines()
assert value == ["alpha", "beta"]
""",
    "regex_capture": """
import re
value = re.fullmatch(r"item-(\\d+)", "item-42").group(1)
assert value == "42"
""",
    "datetime_iso": """
from datetime import datetime, timezone
value = datetime(2026, 9, 20, 12, 30, tzinfo=timezone.utc).isoformat()
assert value == "2026-09-20T12:30:00+00:00"
""",
    "sha256": """
import hashlib
value = hashlib.sha256(b"agent-core").hexdigest()
assert value == "d85eea53b6c68d99f0eab28846b63be8f734f72b9156c376802a77625ed4d5b7"
""",
    "html_parse": """
from html.parser import HTMLParser
class TextParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.parts = []
    def handle_data(self, data):
        self.parts.append(data)
parser = TextParser()
parser.feed("<p>Hello <b>agent</b></p>")
value = "".join(parser.parts)
assert value == "Hello agent"
""",
    "xml_parse": """
import xml.etree.ElementTree as ET
value = ET.fromstring("<items><item id='7'/></items>").find("item").attrib["id"]
assert value == "7"
""",
    "numpy_core": """
import numpy as np
matrix = np.array([[1, 2], [3, 4]], dtype=np.int64)
value = {"sum": int(matrix.sum()), "dot": int(np.dot(matrix[0], matrix[1]))}
assert value == {"sum": 10, "dot": 11}
""",
}


def load_profile(path: Path) -> dict:
    profile = json.loads(path.read_text(encoding="utf-8"))
    if profile.get("schema_version") != 1 or profile.get("profile") != "agent-core":
        raise ValueError("unsupported artifact profile")
    qualification = profile.get("qualification")
    if not isinstance(qualification, list) or not qualification:
        raise ValueError("profile qualification must be a non-empty list")
    names = set()
    for item in qualification:
        if set(item) != {"name", "operation"} or not all(isinstance(item[key], str) and item[key] for key in item):
            raise ValueError("invalid qualification entry")
        if item["name"] in names:
            raise ValueError(f"duplicate qualification name: {item['name']}")
        names.add(item["name"])
        if item["operation"] != "workspace_edit_acceptance" and item["operation"] not in OPERATIONS:
            raise ValueError(f"unknown qualification operation: {item['operation']}")
    return profile


def guest_source(profile: dict) -> str:
    sections = [
        "import sys\n",
        "checks = {}\n",
    ]
    for item in profile["qualification"]:
        operation = item["operation"]
        if operation == "workspace_edit_acceptance":
            continue
        name = item["name"]
        body = textwrap.dedent(OPERATIONS[operation]).strip()
        sections.append(f"try:\n{textwrap.indent(body, '    ')}\n    checks[{name!r}] = {{'ok': True, 'value': value}}\n")
        sections.append(f"except BaseException as error:\n    checks[{name!r}] = {{'ok': False, 'error': f'{{type(error).__name__}}: {{error}}'}}\n")
    sections.append("import numpy as _profile_numpy\n")
    sections.append("result = {'runtime': {'python': '.'.join(str(x) for x in sys.version_info[:3]), 'numpy': _profile_numpy.__version__}, 'checks': checks}\n")
    return "".join(sections)


def run_json(command: list[str], cwd: Path) -> dict:
    completed = subprocess.run(command, cwd=cwd, text=True, capture_output=True)
    if completed.returncode != 0:
        raise RuntimeError(
            f"command failed ({completed.returncode}): {' '.join(command)}\n"
            f"stdout:\n{completed.stdout}\nstderr:\n{completed.stderr}"
        )
    lines = [line for line in completed.stdout.splitlines() if line.strip()]
    if not lines:
        raise RuntimeError(f"command produced no JSON: {' '.join(command)}")
    return json.loads(lines[-1])


def qualify(profile_path: Path, guest: Path) -> dict:
    profile = load_profile(profile_path)
    artifact = guest.read_bytes()
    with tempfile.NamedTemporaryFile("w", suffix=".py", encoding="utf-8", delete=False) as source_file:
        source_path = Path(source_file.name)
        source_file.write(guest_source(profile))
    try:
        guest_result = run_json(
            ["go", "run", "./cmd/pysolate", "-wasm", str(guest), "-source", str(source_path), "-timeout", "120s"],
            ROOT,
        )
    finally:
        source_path.unlink(missing_ok=True)

    checks = dict(guest_result["checks"])
    if any(item["operation"] == "workspace_edit_acceptance" for item in profile["qualification"]):
        try:
            workspace = run_json(["go", "run", "./examples/workspace-edit", "-guest", str(guest)], ROOT)
            checks["workspace"] = {
                "ok": workspace["changes"]["added"] == 1
                and workspace["changes"]["modified"] == 2
                and workspace["external_writes"] == 1,
                "value": {
                    "added": workspace["changes"]["added"],
                    "modified": workspace["changes"]["modified"],
                    "deleted": workspace["changes"]["deleted"],
                    "external_writes": workspace["external_writes"],
                },
            }
        except Exception as error:
            checks["workspace"] = {"ok": False, "error": f"{type(error).__name__}: {error}"}

    expected = {item["name"] for item in profile["qualification"]}
    observed_runtime = guest_result["runtime"]
    passed = (
        set(checks) == expected
        and all(value.get("ok") is True for value in checks.values())
        and observed_runtime.get("python") == profile["runtime"]["cpython"]
        and observed_runtime.get("numpy") == profile["runtime"]["numpy"]
    )
    return {
        "schema_version": 1,
        "profile": profile["profile"],
        "target": profile["target"],
        "artifact": {
            "path": str(guest),
            "bytes": len(artifact),
            "sha256": hashlib.sha256(artifact).hexdigest(),
        },
        "declared_runtime": profile["runtime"],
        "observed_runtime": observed_runtime,
        "checks": checks,
        "passed": passed,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", type=Path, default=DEFAULT_PROFILE)
    parser.add_argument("--guest", type=Path, default=DEFAULT_GUEST)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    report = qualify(args.profile.resolve(), args.guest.resolve())
    encoded = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded, encoding="utf-8")
    sys.stdout.write(encoded)
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
