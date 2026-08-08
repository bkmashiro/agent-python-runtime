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
HEX40 = re.compile(r"^[0-9a-f]{40}$")
HEX64 = re.compile(r"^[0-9a-f]{64}$")
HOST = re.compile(r"^[A-Za-z0-9.-]+$")
STAGE = re.compile(r"^/vol/bitbucket/ys25/[A-Za-z0-9._/-]+$")
TERMINAL_STATES = {
    "COMPLETED", "FAILED", "CANCELLED", "TIMEOUT", "OUT_OF_MEMORY",
    "NODE_FAIL", "PREEMPTED", "BOOT_FAIL",
}


def run(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {' '.join(args)}\n{result.stderr}")
    return result


def ssh(host: str, command: str, *, check: bool = True) -> subprocess.CompletedProcess[str]:
    return run("ssh", "-o", "BatchMode=yes", host, command, check=check)


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
    if not job.isdecimal():
        raise RuntimeError("job must be numeric")
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
    if run("git", "-C", str(repo), "status", "--short").stdout:
        raise RuntimeError("local exact review clone is modified")
    run("git", "-C", str(repo), "verify-commit", "HEAD")

    validator = args.validator
    schema = args.schema
    expected_schema = repo / "benchmark/v1/cow-pressure.schema.json"
    if (validator.is_symlink() or not validator.is_file() or
            schema != expected_schema or schema.is_symlink() or not schema.is_file() or
            sha256(validator) != args.validator_sha256):
        raise RuntimeError("validator or schema identity is unsafe")

    archive_name = f"result-{args.job}.tar.gz"
    archive_remote = f"{stage}/outbox/{archive_name}"
    checksum_remote = archive_remote + ".sha256"
    ready_remote = f"{stage}/outbox/READY-{args.job}"
    ack_remote = f"{stage}/ACK-{args.job}"
    acked_remote = f"{stage}/outbox/ACKED-{args.job}"
    local_root = args.local_root / f"job-{args.job}"
    if local_root.exists() or local_root.is_symlink():
        raise RuntimeError(f"local recovery target already exists: {local_root}")
    local_root.mkdir(parents=True, mode=0o700)

    quoted_job = shlex.quote(args.job)
    quoted_archive = shlex.quote(archive_remote)
    quoted_checksum = shlex.quote(checksum_remote)
    quoted_ready = shlex.quote(ready_remote)
    quoted_ack = shlex.quote(ack_remote)
    quoted_acked = shlex.quote(acked_remote)

    def query_state() -> str:
        query = ssh(args.host, f"squeue -h -j {quoted_job} -o '%T' || true")
        value = query.stdout.strip().splitlines()[0] if query.stdout.strip() else ""
        if not value:
            accounting = ssh(args.host, f"sacct -n -X -j {quoted_job} --format=State -P | head -n 1 || true")
            value = accounting.stdout.strip().split("|")[0].split()[0] if accounting.stdout.strip() else ""
        return value.rstrip("+")

    state = ""
    while True:
        if ssh(args.host, f"test -f {quoted_ready}", check=False).returncode == 0:
            break
        state = query_state()
        if state in TERMINAL_STATES:
            raise RuntimeError(f"job reached {state} before publishing READY")
        time.sleep(args.interval)

    ssh(args.host, f"set -e; test -f {quoted_archive}; test -f {quoted_checksum}; test -f {quoted_ready}; test ! -e {quoted_ack}")
    archive = local_root / "archive.tar.gz"
    checksum = local_root / "archive.tar.gz.sha256"
    ready = local_root / "READY"
    run("scp", "-q", f"{args.host}:{archive_remote}", str(archive))
    run("scp", "-q", f"{args.host}:{checksum_remote}", str(checksum))
    run("scp", "-q", f"{args.host}:{ready_remote}", str(ready))
    archive_hash = sha256(archive)
    checksum_fields = checksum.read_text(encoding="utf-8").strip().split()
    ready_fields = ready.read_text(encoding="utf-8").strip().split()
    expected_fields = [archive_hash, archive_name]
    if checksum_fields != expected_fields or ready_fields != expected_fields:
        raise RuntimeError("archive checksum or READY identity mismatch")

    extracted = local_root / "extracted"
    safe_extract(archive, extracted)
    run_complete = extracted / "RUN_COMPLETE"
    if run_complete.is_symlink() or not run_complete.is_file() or run_complete.read_text(encoding="utf-8").strip() != args.expected_revision:
        raise RuntimeError("run completion identity drifted")
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
    if not isinstance(records, list) or not records:
        raise RuntimeError("result manifest has no records")

    verdict_expected = {"valid": True, "schema_version": 11, "evidence_kind": "cow-pressure"}
    result_root = manifests[0].parent
    for record in records:
        if not isinstance(record, dict):
            raise RuntimeError("result manifest record is invalid")
        evidence_name = record.get("evidence")
        if not isinstance(evidence_name, str) or PurePosixPath(evidence_name).name != evidence_name:
            raise RuntimeError("unsafe evidence path in result manifest")
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
        verdict = json.loads(verdict_run.stdout)
        if verdict != verdict_expected:
            raise RuntimeError(f"non-canonical validator verdict: {evidence_name}: {verdict}")

    remote_ack = local_root / "ACK.remote"
    remote_ack.write_text(archive_hash + "\n", encoding="utf-8")
    remote_tmp = f"{ack_remote}.tmp-{os.getpid()}"
    run("scp", "-q", str(remote_ack), f"{args.host}:{remote_tmp}")
    quoted_tmp = shlex.quote(remote_tmp)
    ssh(args.host, f"set -e; test ! -e {quoted_ack}; chmod 600 {quoted_tmp}; mv {quoted_tmp} {quoted_ack}")

    while state not in TERMINAL_STATES:
        state = query_state()
        if state not in TERMINAL_STATES:
            time.sleep(args.interval)
    accounting = ssh(args.host, f"sacct -n -X -j {quoted_job} --format=JobIDRaw,State,ExitCode,Elapsed,MaxRSS,ReqMem,AllocCPUS,NodeList -P")
    (local_root / "sacct.txt").write_text(accounting.stdout, encoding="utf-8")
    if state != "COMPLETED" or "|COMPLETED|0:0|" not in accounting.stdout:
        raise RuntimeError(f"Slurm terminal state is not COMPLETED/0:0: {state}\n{accounting.stdout}")
    ssh(args.host, f"set -e; test -f {quoted_acked}; test \"$(cut -d' ' -f1 < {quoted_acked})\" = '{archive_hash}'")

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
        "validated_records": len(records),
        "slurm_state": state,
    }, sort_keys=True, separators=(",", ":")) + "\n"
    local_ack = local_root / "CONTROLLER_ACK.json"
    local_ack.write_text(ack_text, encoding="utf-8")
    print(ack_text, end="")


if __name__ == "__main__":
    main()
