#!/usr/bin/env python3
"""Fail-closed verifier for bounded workstation Host-test evidence."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re

HEX40 = re.compile(r"^[0-9a-f]{40}$")
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
IDENTIFIER = re.compile(r"^[A-Za-z0-9_.:-]{1,128}$")
SUM_LINE = re.compile(r"^([0-9a-f]{64})  ([^\n]+)$")
SUITES = {"baseline", "prepared-family", "evaluation", "evaluation-sweeps", "plm-fixed-cost", "source-stream-timing", "plm-prefix-scaling", "thesis-experiments"}
BUILDERS = {f"gpu{number}.doc.ic.ac.uk" for number in range(31, 36)}
HOSTS = {"gpu31", "gpu32", "gpu33", "gpu34", "gpu35"}


def file_digest(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def evidence_files(root: pathlib.Path) -> set[str]:
    files: set[str] = set()
    for directory, names, filenames in os.walk(root, followlinks=False):
        parent = pathlib.Path(directory)
        for name in names:
            if (parent / name).is_symlink():
                raise ValueError("symlinked evidence directory")
        for name in filenames:
            candidate = parent / name
            if candidate.is_symlink() or not candidate.is_file():
                raise ValueError("non-regular evidence file")
            relative = candidate.relative_to(root).as_posix()
            if relative != "SHA256SUMS":
                files.add(relative)
    return files


def write_checksums(root: pathlib.Path) -> None:
    files = sorted(evidence_files(root))
    lines = [f"{file_digest(root / relative)}  {relative}" for relative in files]
    (root / "SHA256SUMS").write_text("\n".join(lines) + "\n")


def verify_acceptance_report(path: pathlib.Path, source_commit: str, source_tree: str) -> None:
    report = json.loads(path.read_text())
    exact_keys = {
        "schema_version", "source_commit", "source_tree", "artifact_sha256",
        "execution_profile_sha256", "input_sha256", "family_sha256",
        "physical_disposition", "created", "terminal", "selected_root_sha256", "members",
    }
    if set(report) != exact_keys or report.get("schema_version") != "pysolate.prepared-family-acceptance.v1":
        raise ValueError("invalid prepared-family acceptance report schema")
    if report.get("source_commit") != source_commit or report.get("source_tree") != source_tree:
        raise ValueError("prepared-family report source mismatch")
    for key in ("artifact_sha256", "execution_profile_sha256", "input_sha256", "family_sha256", "selected_root_sha256"):
        if not DIGEST.fullmatch(str(report.get(key, ""))):
            raise ValueError(f"invalid prepared-family report digest: {key}")
    if report.get("physical_disposition") not in {"private_copy", "private_cow", "ordinary_fresh"}:
        raise ValueError("invalid prepared-family report disposition")
    members = report.get("members")
    if not isinstance(members, list) or report.get("created") != len(members) or report.get("terminal") != len(members):
        raise ValueError("invalid prepared-family report member counts")
    member_keys = {
        "schema_version", "family_sha256", "input_sha256", "member_id", "run_id",
        "agent_run_id", "turn_seq", "output_item_seq", "segment_seq", "invocation_id",
        "invocation_attempt", "execution_id", "plan_sha256", "grants_sha256",
        "physical_disposition", "outcome", "final_workspace_sha256",
    }
    observed_roots: set[str] = set()
    observed_members: set[int] = set()
    for member in members:
        if not isinstance(member, dict) or set(member) != member_keys:
            raise ValueError("invalid prepared-family member schema")
        if member.get("schema_version") != "pysolate.prepared-family-member.v1" or member.get("family_sha256") != report["family_sha256"] or member.get("input_sha256") != report["input_sha256"] or member.get("physical_disposition") != report["physical_disposition"]:
            raise ValueError("prepared-family member identity drift")
        member_id = member.get("member_id")
        if not isinstance(member_id, int) or member_id <= 0 or member_id in observed_members:
            raise ValueError("invalid or duplicate prepared-family member")
        observed_members.add(member_id)
        for key in ("agent_run_id", "invocation_id", "execution_id"):
            if not IDENTIFIER.fullmatch(str(member.get(key, ""))):
                raise ValueError(f"invalid prepared-family member identifier: {key}")
        if member.get("run_id") != member.get("execution_id") or not isinstance(member.get("invocation_attempt"), int) or member["invocation_attempt"] <= 0:
            raise ValueError("invalid prepared-family execution join")
        for key in ("plan_sha256", "grants_sha256", "final_workspace_sha256"):
            if not DIGEST.fullmatch(str(member.get(key, ""))):
                raise ValueError(f"invalid prepared-family member digest: {key}")
        if member.get("outcome") != "ok":
            raise ValueError("Host acceptance report contains non-success member")
        observed_roots.add(member["final_workspace_sha256"])
    if report["selected_root_sha256"] not in observed_roots:
        raise ValueError("prepared-family selected root was not observed")


def verify(root: pathlib.Path, source_commit: str | None = None, source_tree: str | None = None, suite: str | None = None, target: str | None = None) -> dict[str, object]:
    if root.is_symlink():
        raise ValueError("evidence root must be a real directory")
    root = root.resolve()
    if not root.is_dir():
        raise ValueError("evidence root must be a real directory")
    ready_path = root / "RESULT.READY"
    sums_path = root / "SHA256SUMS"
    log_path = root / "test.log"
    for required in (ready_path, sums_path, log_path):
        if not required.is_file() or required.is_symlink():
            raise ValueError(f"missing regular evidence file: {required.name}")

    checksums: dict[str, str] = {}
    for line in sums_path.read_text().splitlines():
        match = SUM_LINE.fullmatch(line)
        if match is None:
            raise ValueError("malformed SHA256SUMS")
        relative = pathlib.PurePosixPath(match.group(2))
        if relative.is_absolute() or ".." in relative.parts or relative.as_posix() == "SHA256SUMS":
            raise ValueError("checksum path escapes or recurses")
        name = relative.as_posix()
        if name in checksums:
            raise ValueError("duplicate checksum path")
        checksums[name] = match.group(1)
    if set(checksums) != evidence_files(root):
        raise ValueError("SHA256SUMS does not cover the exact evidence file set")
    for relative, expected in checksums.items():
        if file_digest(root.joinpath(*pathlib.PurePosixPath(relative).parts)) != expected:
            raise ValueError(f"checksum mismatch: {relative}")

    result = json.loads(ready_path.read_text())
    v1_keys = {
        "schema_version", "source_commit", "source_tree", "builder", "target",
        "suite", "passed", "go_version", "duration_millis",
    }
    version = result.get("schema_version")
    if version == "pysolate.workstation-host-test.v1":
        if set(result) != v1_keys:
            raise ValueError("invalid Host-test result schema")
        acceptance_report = False
    elif version == "pysolate.workstation-host-test.v2":
        if set(result) != v1_keys | {"acceptance_report"} or not isinstance(result.get("acceptance_report"), bool):
            raise ValueError("invalid Host-test result schema")
        acceptance_report = result["acceptance_report"]
    else:
        raise ValueError("invalid Host-test result schema")
    if not HEX40.fullmatch(str(result.get("source_commit", ""))) or not HEX40.fullmatch(str(result.get("source_tree", ""))):
        raise ValueError("invalid source identity")
    if source_commit is not None and result["source_commit"] != source_commit:
        raise ValueError("source commit mismatch")
    if source_tree is not None and result["source_tree"] != source_tree:
        raise ValueError("source tree mismatch")
    builder = result.get("builder")
    if builder not in BUILDERS or result.get("target") != "linux/amd64":
        raise ValueError("unexpected builder or target")
    if target is not None:
        if target not in HOSTS or builder != f"{target}.doc.ic.ac.uk":
            raise ValueError("unexpected builder or target")
    if result.get("suite") not in SUITES or (suite is not None and result["suite"] != suite):
        raise ValueError("suite mismatch")

    report_path = root / "acceptance-report.json"
    if acceptance_report != (version == "pysolate.workstation-host-test.v2" and result["suite"] == "prepared-family"):
        raise ValueError("acceptance-report declaration mismatch")
    if acceptance_report:
        if not report_path.is_file() or report_path.is_symlink():
            raise ValueError("missing prepared-family acceptance report")
        verify_acceptance_report(report_path, result["source_commit"], result["source_tree"])
    elif report_path.exists():
        raise ValueError("unexpected prepared-family acceptance report")
    if result.get("passed") is not True:
        raise ValueError("Host-test suite did not pass")
    if not isinstance(result.get("go_version"), str) or not re.fullmatch(r"go1\.25(?:\.[0-9]+)?", result["go_version"]):
        raise ValueError("unexpected Go version")
    if not isinstance(result.get("duration_millis"), int) or result["duration_millis"] < 0:
        raise ValueError("invalid duration")
    return result


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=pathlib.Path)
    parser.add_argument("--source-commit")
    parser.add_argument("--source-tree")
    parser.add_argument("--suite", choices=sorted(SUITES))
    parser.add_argument("--target", choices=sorted(HOSTS))
    args = parser.parse_args()
    print(json.dumps(verify(args.root, args.source_commit, args.source_tree, args.suite, args.target), sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
