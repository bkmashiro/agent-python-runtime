#!/usr/bin/env python3
"""Validate and acknowledge one immutable Phase 6 Slurm result archive."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import subprocess
import tarfile
import time

EXPECTED_SCHEMA_VERSION = 11
EXPECTED_EVIDENCE_KIND = "cow-pressure"
MAX_ARCHIVE_BYTES = 256 << 20
MAX_EXTRACTED_BYTES = 256 << 20
MAX_MEMBER_BYTES = 64 << 20
MAX_MEMBERS = 256
MAX_CONTROL_BYTES = 256
HEX40 = re.compile(r"^[0-9a-f]{40}$")
HEX64 = re.compile(r"^[0-9a-f]{64}$")
JOB = re.compile(r"^[0-9]+$")
HOST = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$")
STAGE = re.compile(r"^/vol/bitbucket/ys25/[A-Za-z0-9._-]+$")
TERMINAL_STATES = {
    "COMPLETED", "FAILED", "CANCELLED", "TIMEOUT", "OUT_OF_MEMORY",
    "NODE_FAIL", "PREEMPTED", "BOOT_FAIL",
}
ACKED_TIME = re.compile(r"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")
REMOTE_BOUNDED_READER = r"""
import os
import stat
import sys

path = sys.argv[1]
maximum = int(sys.argv[2])
flags = os.O_RDONLY | os.O_NOFOLLOW | getattr(os, "O_CLOEXEC", 0)
descriptor = os.open(path, flags)
try:
    identity = os.fstat(descriptor)
    if not stat.S_ISREG(identity.st_mode) or identity.st_size > maximum:
        raise RuntimeError("remote file is not a bounded regular file")
    remaining = identity.st_size
    while remaining:
        chunk = os.read(descriptor, min(1 << 20, remaining))
        if not chunk:
            raise RuntimeError("remote file truncated during bounded read")
        sys.stdout.buffer.write(chunk)
        remaining -= len(chunk)
    sys.stdout.buffer.flush()
finally:
    os.close(descriptor)
"""


def run(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {' '.join(args)}\n{result.stderr}")
    return result


def ssh(host: str, command: str, *, check: bool = True) -> subprocess.CompletedProcess[str]:
    return run("ssh", "-o", "BatchMode=yes", host, command, check=check)


def copy_bounded(source, destination, maximum: int) -> int:
    total = 0
    while True:
        chunk = source.read(min(1 << 20, maximum - total + 1))
        if not chunk:
            return total
        total += len(chunk)
        if total > maximum:
            raise RuntimeError("download exceeded its local transfer bound")
        destination.write(chunk)


def download_bounded(host: str, remote_path: str, local_path: Path, maximum: int) -> int:
    remote_command = (
        f"/usr/bin/python3 -c {shlex.quote(REMOTE_BOUNDED_READER)} "
        f"{shlex.quote(remote_path)} {maximum}"
    )
    process = subprocess.Popen(
        ["ssh", "-o", "BatchMode=yes", host, remote_command],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    try:
        if process.stdout is None or process.stderr is None:
            raise RuntimeError("bounded SSH pipes are unavailable")
        with local_path.open("xb") as destination:
            size = copy_bounded(process.stdout, destination, maximum)
        return_code = process.wait(timeout=30)
        stderr = process.stderr.read(64 << 10)
        if return_code != 0:
            raise RuntimeError(
                f"bounded remote read failed ({return_code}): "
                f"{stderr.decode('utf-8', errors='replace')}"
            )
        return size
    except BaseException:
        if process.poll() is None:
            process.kill()
        process.wait()
        local_path.unlink(missing_ok=True)
        raise


def unique_json(path: Path, maximum_bytes: int = 16 << 20):
    if path.is_symlink() or not path.is_file() or path.stat().st_size > maximum_bytes:
        raise RuntimeError(f"unsafe JSON file: {path}")

    def reject(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError(f"duplicate JSON key: {key}")
            value[key] = item
        return value

    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=reject)


def unique_json_text(text: str):
    def reject(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError(f"duplicate JSON key: {key}")
            value[key] = item
        return value

    return json.loads(text, object_pairs_hook=reject)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def safe_extract(archive: Path, destination: Path) -> None:
    if archive.is_symlink() or not archive.is_file() or archive.stat().st_size > MAX_ARCHIVE_BYTES:
        raise RuntimeError("archive is missing, symlinked, or oversized")
    destination.mkdir(parents=True, mode=0o700, exist_ok=False)
    with tarfile.open(archive, "r:gz") as package:
        members = package.getmembers()
        if not members or len(members) > MAX_MEMBERS:
            raise RuntimeError("archive member count is invalid")
        names: set[str] = set()
        total_size = 0
        for member in members:
            name = PurePosixPath(member.name)
            normalized = name.as_posix()
            if (name.is_absolute() or ".." in name.parts or "." in name.parts or
                    not (member.isdir() or member.isfile())):
                raise RuntimeError(f"unsafe archive member: {member.name}")
            if normalized in names:
                raise RuntimeError(f"duplicate archive member: {member.name}")
            names.add(normalized)
            if member.size < 0 or member.size > MAX_MEMBER_BYTES:
                raise RuntimeError(f"oversized archive member: {member.name}")
            total_size += member.size
            if total_size > MAX_EXTRACTED_BYTES:
                raise RuntimeError("archive extracted size exceeds the limit")
        package.extractall(destination)


def validate_identity_args(
    *, job: str, host: str, stage: str, expected_revision: str,
    expected_tree: str, expected_artifact_sha256: str,
    expected_artifact_manifest_sha256: str, expected_artifact_source: str,
    validator_sha256: str,
    expected_tier: str, interval: int,
) -> None:
    path = PurePosixPath(stage)
    if not JOB.fullmatch(job):
        raise RuntimeError("invalid Slurm job id")
    if not HOST.fullmatch(host):
        raise RuntimeError("host contains unsafe characters")
    if (not STAGE.fullmatch(stage) or ".." in path.parts or "." in path.parts or
            str(path) != stage.rstrip("/")):
        raise RuntimeError("stage path is unsafe")
    if not HEX40.fullmatch(expected_revision) or not HEX40.fullmatch(expected_tree):
        raise RuntimeError("Host source identity is invalid")
    if (not HEX64.fullmatch(expected_artifact_sha256) or
            not HEX64.fullmatch(expected_artifact_manifest_sha256) or
            not HEX40.fullmatch(expected_artifact_source)):
        raise RuntimeError("artifact identity is invalid")
    if not HEX64.fullmatch(validator_sha256):
        raise RuntimeError("validator identity is invalid")
    if expected_tier not in {"canary", "small", "formal"}:
        raise RuntimeError("expected tier is invalid")
    if interval < 60:
        raise RuntimeError("polling interval must be at least 60 seconds")


def expected_plan_cells(repo: Path, tier: str, formal_selection: Path | None) -> list[dict]:
    command = ["python3", str(repo / "tools/phase6_matrix.py"), "plan", "--tier", tier]
    if tier == "formal":
        if formal_selection is None or formal_selection.is_symlink() or not formal_selection.is_file():
            raise RuntimeError("formal tier requires a safe local selection file")
        command.extend(("--formal-selection", str(formal_selection)))
    elif formal_selection is not None:
        raise RuntimeError("formal selection is forbidden outside formal tier")
    plan = unique_json_text(run(*command).stdout)
    if (not isinstance(plan, dict) or plan.get("schema_version") != 1 or
            plan.get("plan_kind") != "phase6-numpy-density-matrix" or plan.get("tier") != tier):
        raise RuntimeError("canonical matrix plan identity drifted")
    cells = plan.get("cells")
    if not isinstance(cells, list) or not cells or not all(isinstance(cell, dict) for cell in cells):
        raise RuntimeError("canonical matrix plan has no valid cells")
    return cells


def validate_record_set(records, expected_cells: list[dict], tier: str) -> list[str]:
    if not isinstance(records, list) or len(records) != len(expected_cells):
        raise RuntimeError("result manifest matrix is incomplete")
    evidence_names: list[str] = []
    seen: set[str] = set()
    for record, expected_cell in zip(records, expected_cells, strict=True):
        if not isinstance(record, dict) or record.get("cell") != expected_cell:
            raise RuntimeError("result manifest cell set drifted")
        suffix = f"-r{expected_cell.get('repetition')}" if tier == "formal" else ""
        expected_name = f"{expected_cell.get('cell_id')}{suffix}.json"
        evidence_name = record.get("evidence")
        if evidence_name != expected_name or evidence_name in seen:
            raise RuntimeError("result manifest evidence set drifted")
        seen.add(evidence_name)
        evidence_names.append(evidence_name)
    return evidence_names


def parse_squeue_state(text: str, expected_job: str) -> str:
    lines = [line for line in text.splitlines() if line]
    if not lines:
        return ""
    if len(lines) != 1:
        raise RuntimeError("squeue returned an ambiguous job set")
    fields = lines[0].split("|")
    if len(fields) != 2 or fields[0] != expected_job or not fields[1]:
        raise RuntimeError("squeue job identity drifted")
    return fields[1].rstrip("+")


def parse_sacct_accounting(text: str, expected_job: str) -> dict[str, str]:
    lines = [line for line in text.splitlines() if line]
    if len(lines) != 1:
        raise RuntimeError("sacct returned an ambiguous job set")
    fields = lines[0].split("|")
    if len(fields) != 9 or fields[-1] != "" or fields[0] != expected_job:
        raise RuntimeError("sacct job identity drifted")
    keys = ("job_id", "state", "exit_code", "elapsed", "max_rss", "requested_memory", "allocated_cpus", "node_list")
    row = dict(zip(keys, fields[:-1], strict=True))
    row["state"] = row["state"].split()[0].rstrip("+")
    return row


def parse_scontrol_job(text: str, expected_job: str) -> dict[str, str]:
    lines = [line for line in text.splitlines() if line]
    if len(lines) != 1:
        raise RuntimeError("scontrol returned an ambiguous job set")
    values: dict[str, str] = {}
    for token in lines[0].split():
        key, separator, value = token.partition("=")
        if separator == "=" and key:
            if key in values:
                raise RuntimeError("scontrol returned duplicate fields")
            values[key] = value
    if values.get("JobId") != expected_job or not values.get("JobState") or not values.get("ExitCode"):
        raise RuntimeError("scontrol job identity drifted")
    values["JobState"] = values["JobState"].split()[0].rstrip("+")
    return values


def parse_tres(value: str) -> dict[str, str]:
    result: dict[str, str] = {}
    for field in value.split(","):
        key, separator, item = field.partition("=")
        if separator != "=" or not key or not item or key in result:
            raise RuntimeError("Slurm TRES is malformed")
        result[key] = item
    return result


def validate_resource_shape(values: dict[str, str]) -> None:
    if (values.get("Partition") != "t4" or values.get("NumNodes") != "1" or
            values.get("NumCPUs") != "4" or values.get("NumTasks") != "1" or
            values.get("CPUs/Task") != "4" or
            values.get("MinMemoryNode") not in {"16G", "16384M"} or
            values.get("TresPerNode") not in {"gres:gpu:tesla_t4:1", "gres/gpu:tesla_t4:1"}):
        raise RuntimeError("Slurm requested resource shape drifted")
    allocated = parse_tres(values.get("AllocTRES", ""))
    if (allocated.get("cpu") != "4" or allocated.get("mem") not in {"16G", "16384M"} or
            allocated.get("node") != "1" or allocated.get("gres/gpu") != "1"):
        raise RuntimeError("Slurm allocated resource shape drifted")


def validate_environment_shape(values: dict[str, str]) -> None:
    cuda = values.get("cuda_visible_devices", "")
    if (values.get("job_partition") != "t4" or values.get("job_num_nodes") != "1" or
            values.get("num_tasks") != "1" or values.get("cpus_per_task") != "4" or
            values.get("memory_per_node") not in {"16G", "16384"} or
            values.get("gpus_on_node") != "1" or not cuda or "," in cuda):
        raise RuntimeError("worker resource environment drifted")


def validate_acked_text(text: str, expected_hash: str) -> None:
    lines = text.splitlines()
    if len(lines) != 1:
        raise RuntimeError("ACKED sentinel must contain exactly one line")
    fields = lines[0].split("  ")
    if len(fields) != 2 or fields[0] != expected_hash or not ACKED_TIME.fullmatch(fields[1]):
        raise RuntimeError("ACKED sentinel identity drifted")


def exact_key_values(path: Path) -> dict[str, str]:
    if path.is_symlink() or not path.is_file() or path.stat().st_size > (64 << 10):
        raise RuntimeError("unsafe key-value evidence file")
    values: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        key, separator, value = line.partition("=")
        if separator != "=" or not re.fullmatch(r"[a-z_]+", key) or key in values:
            raise RuntimeError("malformed or duplicate key-value evidence")
        values[key] = value
    return values


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--job", required=True)
    parser.add_argument("--host", default="gpucluster2")
    parser.add_argument("--stage", required=True)
    parser.add_argument("--local-root", required=True, type=Path)
    parser.add_argument("--validator", required=True, type=Path)
    parser.add_argument("--validator-sha256", required=True)
    parser.add_argument("--repo", required=True, type=Path)
    parser.add_argument("--schema", required=True, type=Path)
    parser.add_argument("--expected-revision", required=True)
    parser.add_argument("--expected-tree", required=True)
    parser.add_argument("--expected-artifact-sha256", required=True)
    parser.add_argument("--expected-artifact-manifest-sha256", required=True)
    parser.add_argument("--expected-artifact-source", required=True)
    parser.add_argument("--expected-tier", choices=("canary", "small", "formal"), required=True)
    parser.add_argument("--formal-selection", type=Path)
    parser.add_argument("--interval", type=int, default=60)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    validate_identity_args(
        job=args.job,
        host=args.host,
        stage=args.stage,
        expected_revision=args.expected_revision,
        expected_tree=args.expected_tree,
        expected_artifact_sha256=args.expected_artifact_sha256,
        expected_artifact_manifest_sha256=args.expected_artifact_manifest_sha256,
        expected_artifact_source=args.expected_artifact_source,
        validator_sha256=args.validator_sha256,
        expected_tier=args.expected_tier,
        interval=args.interval,
    )
    stage = args.stage.rstrip("/")
    repo = args.repo
    if repo.is_symlink() or not repo.is_dir():
        raise RuntimeError("local exact review clone is unsafe")
    if run("git", "-C", str(repo), "rev-parse", "HEAD").stdout.strip() != args.expected_revision:
        raise RuntimeError("local exact review clone revision drifted")
    if run("git", "-C", str(repo), "rev-parse", "HEAD^{tree}").stdout.strip() != args.expected_tree:
        raise RuntimeError("local exact review clone tree drifted")
    if run("git", "-C", str(repo), "status", "--short", "--untracked-files=all").stdout:
        raise RuntimeError("local exact review clone is modified")
    run("git", "-C", str(repo), "verify-commit", "HEAD")

    validator = args.validator
    schema = args.schema
    expected_schema = repo / "benchmark/v1/cow-pressure.schema.json"
    if (validator.is_symlink() or not validator.is_file() or
            schema != expected_schema or schema.is_symlink() or not schema.is_file() or
            sha256(validator) != args.validator_sha256):
        raise RuntimeError("validator or schema identity is unsafe")
    expected_cells = expected_plan_cells(repo, args.expected_tier, args.formal_selection)
    formal_selection_sha256 = sha256(args.formal_selection) if args.formal_selection is not None else "none"

    archive_name = f"result-{args.job}.tar.gz"
    archive_remote = f"{stage}/outbox/{archive_name}"
    checksum_remote = archive_remote + ".sha256"
    ready_remote = f"{stage}/outbox/READY-{args.job}"
    ack_remote = f"{stage}/ACK-{args.job}"
    acked_remote = f"{stage}/outbox/ACKED-{args.job}"
    base_root = args.local_root
    if base_root.is_symlink():
        raise RuntimeError("local recovery root is symlinked")
    base_root.mkdir(parents=True, mode=0o700, exist_ok=True)
    final_root = base_root / f"job-{args.job}"
    local_root = base_root / f".job-{args.job}.partial-{os.getpid()}"
    if final_root.exists() or final_root.is_symlink() or local_root.exists() or local_root.is_symlink():
        raise RuntimeError(f"local recovery target already exists: {final_root}")
    local_root.mkdir(mode=0o700)

    quoted_job = shlex.quote(args.job)
    quoted_archive = shlex.quote(archive_remote)
    quoted_checksum = shlex.quote(checksum_remote)
    quoted_ready = shlex.quote(ready_remote)
    quoted_ack = shlex.quote(ack_remote)
    quoted_acked = shlex.quote(acked_remote)

    def query_state() -> str:
        query = ssh(args.host, f"squeue -h -j {quoted_job} -o '%A|%T' || true")
        value = parse_squeue_state(query.stdout, args.job)
        if value:
            return value
        controller = ssh(args.host, f"scontrol show job -o {quoted_job}", check=False)
        if controller.returncode != 0 or not controller.stdout.strip():
            return ""
        return parse_scontrol_job(controller.stdout, args.job)["JobState"]

    state = ""
    while True:
        if ssh(args.host, f"test -f {quoted_ready}", check=False).returncode == 0:
            break
        state = query_state()
        if state in TERMINAL_STATES:
            raise RuntimeError(f"job reached {state} before publishing READY")
        time.sleep(args.interval)

    ssh(
        args.host,
        f"set -e; test -f {quoted_archive}; test ! -L {quoted_archive}; "
        f"test -f {quoted_checksum}; test ! -L {quoted_checksum}; "
        f"test -f {quoted_ready}; test ! -L {quoted_ready}; "
        f"test ! -e {quoted_ack}; test ! -L {quoted_ack}",
    )
    archive = local_root / "archive.tar.gz"
    checksum = local_root / "archive.tar.gz.sha256"
    ready = local_root / "READY"
    download_bounded(args.host, archive_remote, archive, MAX_ARCHIVE_BYTES)
    download_bounded(args.host, checksum_remote, checksum, MAX_CONTROL_BYTES)
    download_bounded(args.host, ready_remote, ready, MAX_CONTROL_BYTES)
    archive_hash = sha256(archive)
    checksum_fields = checksum.read_text(encoding="utf-8").strip().split()
    ready_fields = ready.read_text(encoding="utf-8").strip().split()
    expected_fields = [archive_hash, archive_name]
    if checksum_fields != expected_fields or ready_fields != expected_fields:
        raise RuntimeError("archive checksum or READY identity mismatch")

    extracted = local_root / "extracted"
    safe_extract(archive, extracted)
    if {path.name for path in extracted.iterdir()} != {"result", "ENVIRONMENT.txt", "RUN_COMPLETE"}:
        raise RuntimeError("archive top-level members drifted")
    run_complete = extracted / "RUN_COMPLETE"
    if run_complete.is_symlink() or not run_complete.is_file() or run_complete.read_text(encoding="utf-8").strip() != args.expected_revision:
        raise RuntimeError("run completion identity drifted")
    environment = exact_key_values(extracted / "ENVIRONMENT.txt")
    validate_environment_shape(environment)
    if (environment.get("job_id") != args.job or environment.get("tier") != args.expected_tier or
            environment.get("source_commit") != args.expected_revision or
            environment.get("formal_selection_sha256") != formal_selection_sha256):
        raise RuntimeError("Slurm environment identity drifted")
    manifests = list(extracted.rglob("manifest.json"))
    if len(manifests) != 1:
        raise RuntimeError(f"expected one result manifest, found {len(manifests)}")
    manifest = unique_json(manifests[0])
    if manifest.get("host_revision") != args.expected_revision:
        raise RuntimeError("result manifest Host identity drifted")
    if manifest.get("tier") != args.expected_tier:
        raise RuntimeError("result manifest tier drifted")
    if manifest.get("schema_version") != 1 or manifest.get("manifest_kind") != "phase6-numpy-density-run":
        raise RuntimeError("result manifest schema drifted")
    if (manifest.get("artifact_sha256") != args.expected_artifact_sha256 or
            manifest.get("artifact_manifest_sha256") != args.expected_artifact_manifest_sha256 or
            manifest.get("artifact_source_revision") != args.expected_artifact_source):
        raise RuntimeError("result manifest artifact identity drifted")
    if manifest.get("binary_sha256") != args.validator_sha256:
        raise RuntimeError("result binary and exact validator identity drifted")
    records = manifest.get("records")
    evidence_names = validate_record_set(records, expected_cells, args.expected_tier)

    verdict_expected = {"valid": True, "schema_version": 11, "evidence_kind": "cow-pressure"}
    result_root = manifests[0].parent
    for record, evidence_name in zip(records, evidence_names, strict=True):
        evidence = result_root / evidence_name
        if not evidence.is_file() or evidence.is_symlink() or sha256(evidence) != record.get("evidence_sha256"):
            raise RuntimeError(f"evidence checksum drifted: {evidence_name}")
        document = unique_json(evidence)
        if document.get("schema_version") != EXPECTED_SCHEMA_VERSION or document.get("evidence_kind") != EXPECTED_EVIDENCE_KIND:
            raise RuntimeError(f"evidence kind/schema drifted: {evidence_name}")
        host_source = document.get("host_source", {})
        if host_source.get("revision") != args.expected_revision or host_source.get("modified") is not False:
            raise RuntimeError(f"evidence Host identity drifted: {evidence_name}")
        artifact = document.get("artifact", {})
        if (artifact.get("sha256") != args.expected_artifact_sha256 or
                artifact.get("source_commit") != args.expected_artifact_source):
            raise RuntimeError(f"evidence artifact identity drifted: {evidence_name}")
        verdict_run = run(str(validator), "-kind=validate-cow-pressure", f"-input={evidence}", f"-schema={schema}")
        verdict = unique_json_text(verdict_run.stdout)
        if verdict != verdict_expected:
            raise RuntimeError(f"non-canonical validator verdict: {evidence_name}: {verdict}")

    local_root.rename(final_root)
    local_root = final_root
    remote_ack = local_root / "ACK.remote"
    remote_ack.write_text(archive_hash + "\n", encoding="utf-8")
    remote_tmp = f"{ack_remote}.tmp-{os.getpid()}"
    quoted_tmp = shlex.quote(remote_tmp)
    ssh(args.host, f"set -e; test ! -e {quoted_ack}; test ! -L {quoted_ack}; test ! -e {quoted_tmp}; test ! -L {quoted_tmp}")
    run("scp", "-q", str(remote_ack), f"{args.host}:{remote_tmp}")
    ssh(
        args.host,
        f"set -e; test -f {quoted_tmp}; test ! -L {quoted_tmp}; "
        f"test \"$(wc -l < {quoted_tmp})\" -eq 1; "
        f"test \"$(cat {quoted_tmp})\" = '{archive_hash}'; chmod 600 {quoted_tmp}; "
        f"ln {quoted_tmp} {quoted_ack}; rm {quoted_tmp}",
    )

    while state not in TERMINAL_STATES:
        state = query_state()
        if state not in TERMINAL_STATES:
            time.sleep(args.interval)
    controller = ssh(args.host, f"scontrol show job -o {quoted_job}")
    (local_root / "scontrol.txt").write_text(controller.stdout, encoding="utf-8")
    controller_row = parse_scontrol_job(controller.stdout, args.job)
    validate_resource_shape(controller_row)
    if (state != "COMPLETED" or controller_row["JobState"] != "COMPLETED" or
            controller_row["ExitCode"] != "0:0" or controller_row.get("Partition") != "t4"):
        raise RuntimeError(f"Slurm terminal state is not exact COMPLETED/0:0 on t4: {state}\n{controller.stdout}")
    accounting = ssh(
        args.host,
        f"sacct -n -X -j {quoted_job} --format=JobIDRaw,State,ExitCode,Elapsed,MaxRSS,ReqMem,AllocCPUS,NodeList -P",
        check=False,
    )
    (local_root / "sacct.txt").write_text(accounting.stdout + accounting.stderr, encoding="utf-8")
    sacct_verified = False
    if accounting.returncode == 0 and accounting.stdout.strip():
        accounting_row = parse_sacct_accounting(accounting.stdout, args.job)
        if accounting_row["state"] != "COMPLETED" or accounting_row["exit_code"] != "0:0":
            raise RuntimeError("sacct disagrees with the Slurm controller record")
        sacct_verified = True
    ssh(args.host, f"set -e; test -f {quoted_acked}; test ! -L {quoted_acked}")
    acked = local_root / "ACKED"
    download_bounded(args.host, acked_remote, acked, MAX_CONTROL_BYTES)
    validate_acked_text(acked.read_text(encoding="utf-8"), archive_hash)

    ack_text = json.dumps({
        "job_id": args.job,
        "archive_sha256": archive_hash,
        "host_revision": args.expected_revision,
        "host_tree": args.expected_tree,
        "artifact_sha256": args.expected_artifact_sha256,
        "artifact_manifest_sha256": args.expected_artifact_manifest_sha256,
        "artifact_source_revision": args.expected_artifact_source,
        "validator_sha256": args.validator_sha256,
        "tier": args.expected_tier,
        "formal_selection_sha256": formal_selection_sha256,
        "validated_records": len(records),
        "slurm_state": state,
        "scontrol_verified": True,
        "sacct_verified": sacct_verified,
    }, sort_keys=True, separators=(",", ":")) + "\n"
    local_ack = local_root / "CONTROLLER_ACK.json"
    local_ack.write_text(ack_text, encoding="utf-8")
    print(ack_text, end="")


if __name__ == "__main__":
    main()
