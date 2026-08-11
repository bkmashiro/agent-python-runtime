#!/usr/bin/env python3
"""Generate and validate target-Guest import execution qualification evidence."""

from __future__ import annotations

import argparse
import json
import pathlib
from typing import Any

PROBE_ID = "guest-import-exec-v1"
PROFILES = {"base"}
STATUSES = {"qualified", "import_failed", "operation_failed"}
MAX_RESULTS = 64

_BASE_PROBES = (
    ("agent_runtime", "import"),
    ("base64", "roundtrip"),
    ("collections", "counter"),
    ("csv", "roundtrip"),
    ("datetime", "date_isoformat"),
    ("decimal", "add"),
    ("fractions", "add"),
    ("functools", "reduce"),
    ("hashlib", "sha256"),
    ("itertools", "islice"),
    ("json", "roundtrip"),
    ("math", "sqrt"),
    ("pathlib", "pure_path"),
    ("re", "fullmatch"),
    ("statistics", "mean"),
    ("sys", "version_info"),
    ("urllib", "parse"),
    ("xml", "etree_roundtrip"),
)

PROBE_CODE = r'''import importlib
import sys

probe_id = "guest-import-exec-v1"
name = inputs["module"]
operation = inputs["operation"]
status = "qualified"
error = ""
try:
    import_name = {
        "parse": "urllib.parse",
        "etree_roundtrip": "xml.etree.ElementTree",
    }.get(operation, name)
    module = importlib.import_module(import_name)
except Exception as exc:
    status = "import_failed"
    error = type(exc).__name__
else:
    try:
        if operation == "import":
            assert module is not None
        elif operation == "roundtrip" and name == "base64":
            assert module.b64decode(module.b64encode(b"pysolate")) == b"pysolate"
        elif operation == "counter":
            assert module.Counter("abca")["a"] == 2
        elif operation == "roundtrip" and name == "csv":
            import io
            stream = io.StringIO()
            module.writer(stream).writerow(["a", "b"])
            stream.seek(0)
            assert next(module.reader(stream)) == ["a", "b"]
        elif operation == "date_isoformat":
            assert module.date(2026, 8, 10).isoformat() == "2026-08-10"
        elif operation == "add" and name == "decimal":
            assert module.Decimal("0.1") + module.Decimal("0.2") == module.Decimal("0.3")
        elif operation == "add" and name == "fractions":
            assert module.Fraction(1, 3) + module.Fraction(1, 6) == module.Fraction(1, 2)
        elif operation == "reduce":
            assert module.reduce(lambda left, right: left + right, [1, 2, 3]) == 6
        elif operation == "sha256":
            assert module.sha256(b"pysolate").hexdigest() == "44754aae58d2947fb04d390f792e1846c4988619065a5826db1ed4a5c01082b4"
        elif operation == "islice":
            assert list(module.islice(range(10), 2, 5)) == [2, 3, 4]
        elif operation == "roundtrip" and name == "json":
            assert module.loads(module.dumps({"ok": [1, 2]}, sort_keys=True)) == {"ok": [1, 2]}
        elif operation == "sqrt":
            assert module.sqrt(81) == 9
        elif operation == "pure_path":
            assert str(module.PurePosixPath("a") / "b") == "a/b"
        elif operation == "fullmatch":
            assert module.fullmatch(r"[a-z]+", "pysolate") is not None
        elif operation == "mean":
            assert module.mean([1, 2, 3]) == 2
        elif operation == "version_info":
            assert module.version_info.major == 3
        elif operation == "parse":
            assert module.urlparse("https://example.test/a").path == "/a"
        elif operation == "etree_roundtrip":
            assert module.fromstring("<root><item /></root>").tag == "root"
        else:
            raise ValueError("unknown qualification operation")
    except Exception as exc:
        status = "operation_failed"
        error = type(exc).__name__
result = {
    "schema_version": 1,
    "artifact_profile": inputs["artifact_profile"],
    "probe": probe_id,
    "implementation": sys.implementation.name,
    "python_version": ".".join(str(part) for part in sys.version_info[:3]),
    "name": name,
    "operation": operation,
    "status": status,
    "error": error,
}
'''


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def strict_json_loads(value: str) -> Any:
    try:
        return json.loads(value, object_pairs_hook=_strict_object)
    except (json.JSONDecodeError, TypeError) as exc:
        raise ValueError("invalid JSON") from exc


def required_roots(profile: str) -> set[str]:
    if profile not in PROFILES:
        raise ValueError("unsupported artifact profile")
    return {"agent_runtime", "json", "sys"}


def probe_specs(profile: str) -> list[dict[str, str]]:
    if profile not in PROFILES:
        raise ValueError("unsupported artifact profile")
    return [{"name": name, "operation": operation} for name, operation in sorted(_BASE_PROBES)]


def build_requests(profile: str) -> list[dict[str, Any]]:
    return [
        {
            "run_id": f"artifact-import-qualification-{probe['name']}",
            "code": PROBE_CODE,
            "inputs": {
                "artifact_profile": profile,
                "module": probe["name"],
                "operation": probe["operation"],
            },
        }
        for probe in probe_specs(profile)
    ]


def _valid_name(value: Any) -> bool:
    return isinstance(value, str) and 0 < len(value) <= 128 and value.isidentifier()


def _extract_result(response: Any, profile: str) -> dict[str, Any]:
    if not isinstance(response, dict) or set(response) < {"status", "result"} or response["status"] != "ok":
        raise ValueError("Guest import qualification probe did not complete")
    row = response["result"]
    expected = {
        "schema_version", "artifact_profile", "probe", "implementation",
        "python_version", "name", "operation", "status", "error",
    }
    if not isinstance(row, dict) or set(row) != expected:
        raise ValueError("invalid import qualification result fields")
    if (
        row["schema_version"] != 1
        or row["artifact_profile"] != profile
        or row["probe"] != PROBE_ID
        or row["implementation"] != "cpython"
        or not isinstance(row["python_version"], str)
        or not 0 < len(row["python_version"]) <= 256
        or not _valid_name(row["name"])
        or not isinstance(row["operation"], str)
        or not 0 < len(row["operation"]) <= 64
        or row["status"] not in STATUSES
        or not isinstance(row["error"], str)
        or len(row["error"]) > 128
        or (row["status"] == "qualified") != (row["error"] == "")
    ):
        raise ValueError("invalid import qualification result")
    return row


def extract_qualification(responses: list[Any], profile: str) -> dict[str, Any]:
    expected_specs = probe_specs(profile)
    if not isinstance(responses, list) or not 1 <= len(responses) <= MAX_RESULTS:
        raise ValueError("invalid import qualification responses")
    rows = [_extract_result(response, profile) for response in responses]
    rows.sort(key=lambda row: row["name"])
    actual_specs = [{"name": row["name"], "operation": row["operation"]} for row in rows]
    if actual_specs != expected_specs:
        raise ValueError("import qualification results do not match probe specification")
    versions = {(row["implementation"], row["python_version"]) for row in rows}
    if len(versions) != 1:
        raise ValueError("import qualification runtime identity drift")
    qualified = [row["name"] for row in rows if row["status"] == "qualified"]
    if not required_roots(profile).issubset(qualified):
        raise ValueError("required profile import root is not qualified")
    implementation, python_version = versions.pop()
    public_rows = [
        {key: row[key] for key in ("name", "operation", "status", "error")}
        for row in rows
    ]
    return {
        "schema_version": 1,
        "artifact_profile": profile,
        "probe": PROBE_ID,
        "implementation": implementation,
        "python_version": python_version,
        "qualified_roots": qualified,
        "results": public_rows,
    }


def validate_qualification(value: Any, profile: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != {
        "schema_version", "artifact_profile", "probe", "implementation",
        "python_version", "qualified_roots", "results",
    }:
        raise ValueError("invalid import qualification fields")
    responses = [
        {
            "status": "ok",
            "result": {
                "schema_version": value["schema_version"],
                "artifact_profile": value["artifact_profile"],
                "probe": value["probe"],
                "implementation": value["implementation"],
                "python_version": value["python_version"],
                **row,
            },
        }
        for row in value.get("results", [])
        if isinstance(row, dict)
    ]
    rebuilt = extract_qualification(responses, profile)
    if rebuilt != value:
        raise ValueError("import qualification catalog is not canonical")
    return value


def load_qualification(path: pathlib.Path, profile: str) -> dict[str, Any]:
    return validate_qualification(strict_json_loads(path.read_text()), profile)


def write_json(path: pathlib.Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    requests_parser = subparsers.add_parser("requests")
    requests_parser.add_argument("--profile", choices=sorted(PROFILES), required=True)
    requests_parser.add_argument("--output-dir", type=pathlib.Path, required=True)
    extract_parser = subparsers.add_parser("extract")
    extract_parser.add_argument("--profile", choices=sorted(PROFILES), required=True)
    extract_parser.add_argument("--responses-dir", type=pathlib.Path, required=True)
    extract_parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()

    if args.command == "requests":
        args.output_dir.mkdir(parents=True, exist_ok=True)
        for index, request in enumerate(build_requests(args.profile)):
            write_json(args.output_dir / f"{index:02d}-{request['inputs']['module']}.json", request)
        return 0
    response_paths = sorted(args.responses_dir.glob("*.json"))
    responses = [strict_json_loads(path.read_text()) for path in response_paths]
    write_json(args.output, extract_qualification(responses, args.profile))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
