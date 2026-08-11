#!/usr/bin/env python3
"""Run one bounded trial on an unmodified pinned Cloudflare Computer checkout.

The adapter is Host orchestration only. It does not patch or vendor upstream code,
does not deploy, and talks only to a local Wrangler/workerd endpoint.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re
import stat
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from typing import Any

UPSTREAM_URL = "https://github.com/cloudflare/computer.git"
TAG = "v0.1.1"
COMMIT = "63d363632e558f7e077794988d36ed75017c2a62"
TREE = "6f4bd936f3154f299f50db79fd4a5f519c0e0447"
ARCHIVE_SHA256 = "e2da083db26d39414da165bb96aa51fde044209541a451c223389355898e6aa1"
LOCK_SHA256 = "79ae6e5058855758e510622631615c8a2532cd84ee3276149a5f3c2c6f8ad328"
WRANGLER_VERSION = "4.115.0"
WORKERD_VERSION = "1.20260722.1"
MAX_REQUEST = 1 << 20
MAX_SOURCE = 64 << 10
MAX_FILE = 1 << 20
MAX_FILES = 64
WORKSPACE_ID = re.compile(r"^[a-z0-9][a-z0-9-]{0,62}$")
HARNESS_SOURCE = pathlib.Path(__file__).resolve().parents[1] / "eval" / "computer-worker-harness"
HARNESS_DESTINATION = ".placement-harness"


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def canonical(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n").encode()


def safe_path(value: str) -> str:
    if not isinstance(value, str) or not value or len(value) > 4096 or "\x00" in value:
        raise ValueError("invalid workspace path")
    candidate = pathlib.PurePosixPath(value)
    if candidate.is_absolute() or ".." in candidate.parts or candidate.as_posix() in {".", ""}:
        raise ValueError("workspace path must be relative and contained")
    return candidate.as_posix()


def load_request(path: pathlib.Path) -> dict[str, Any]:
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode) or info.st_size <= 0 or info.st_size > MAX_REQUEST:
        raise ValueError("request must be a bounded regular file")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or set(value) != {"schema_version", "workspace_id", "files", "source", "input", "output_files", "tool_fixture"}:
        raise ValueError("invalid request envelope")
    if value["schema_version"] != "cloudflare-computer-local-trial/v1" or not WORKSPACE_ID.fullmatch(value["workspace_id"]):
        raise ValueError("invalid request identity")
    if not isinstance(value["source"], str) or not value["source"] or len(value["source"].encode()) > MAX_SOURCE:
        raise ValueError("invalid JavaScript source")
    files = value["files"]
    outputs = value["output_files"]
    if not isinstance(files, dict) or not isinstance(outputs, list) or len(files) > MAX_FILES or len(outputs) > MAX_FILES:
        raise ValueError("invalid workspace file set")
    normalized_files: dict[str, str] = {}
    for raw_path, content in files.items():
        normalized = safe_path(raw_path)
        if not isinstance(content, str) or len(content.encode()) > MAX_FILE or normalized in normalized_files:
            raise ValueError("invalid workspace fixture")
        normalized_files[normalized] = content
    normalized_outputs = [safe_path(item) for item in outputs]
    if len(set(normalized_outputs)) != len(normalized_outputs):
        raise ValueError("duplicate output path")
    fixture = value["tool_fixture"]
    if fixture is not None and (
        not isinstance(fixture, dict)
        or set(fixture) != {"schema_version", "calls"}
        or fixture.get("schema_version") != "placement-computer-tool-fixture/v1"
        or not isinstance(fixture.get("calls"), list)
        or len(fixture["calls"]) > 64
    ):
        raise ValueError("invalid trusted tool fixture")
    value["files"] = normalized_files
    value["output_files"] = normalized_outputs
    canonical(value["input"])
    return value


def git(checkout: pathlib.Path, *args: str, capture: bool = True) -> bytes:
    completed = subprocess.run(
        ["git", "-C", str(checkout), *args],
        check=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE,
    )
    return completed.stdout if capture else b""


def harness_digest(root: pathlib.Path) -> str:
    payload = bytearray()
    files = sorted(path for path in root.rglob("*") if path.is_file())
    if not files:
        raise ValueError("placement harness is empty")
    for path in files:
        relative = path.relative_to(root).as_posix().encode()
        data = path.read_bytes()
        payload.extend(len(relative).to_bytes(4, "big"))
        payload.extend(relative)
        payload.extend(len(data).to_bytes(8, "big"))
        payload.extend(data)
    return "sha256:" + sha256(bytes(payload))


def install_harness(checkout: pathlib.Path) -> str:
    expected = harness_digest(HARNESS_SOURCE)
    destination = checkout / HARNESS_DESTINATION
    if destination.exists():
        shutil.rmtree(destination)
    shutil.copytree(HARNESS_SOURCE, destination)
    actual = harness_digest(destination)
    if actual != expected:
        raise ValueError("placement harness copy mismatch")
    return actual


def verify_checkout(checkout: pathlib.Path, *, require_harness: bool = False) -> dict[str, str]:
    info = checkout.lstat()
    if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
        raise ValueError("checkout must be a real directory")
    commit = git(checkout, "rev-parse", "HEAD").decode().strip()
    tree = git(checkout, "rev-parse", "HEAD^{tree}").decode().strip()
    tag = git(checkout, "describe", "--tags", "--exact-match", "HEAD").decode().strip()
    status = git(checkout, "status", "--porcelain", "--untracked-files=no").decode()
    archive = git(checkout, "archive", "--format=tar", "HEAD")
    lock = (checkout / "package-lock.json").read_bytes()
    harness = checkout / HARNESS_DESTINATION
    expected_harness = harness_digest(HARNESS_SOURCE)
    if require_harness and (not harness.is_dir() or harness_digest(harness) != expected_harness):
        raise ValueError("placement harness identity mismatch")
    if (commit, tree, tag, status, sha256(archive), sha256(lock)) != (COMMIT, TREE, TAG, "", ARCHIVE_SHA256, LOCK_SHA256):
        raise ValueError("Cloudflare Computer checkout identity mismatch")
    return {
        "repository": UPSTREAM_URL,
        "tag": TAG,
        "commit": COMMIT,
        "tree": TREE,
        "archive_sha256": "sha256:" + ARCHIVE_SHA256,
        "package_lock_sha256": "sha256:" + LOCK_SHA256,
        "backend": "worker-javascript",
        "transport": "wrangler-local",
        "wrangler": WRANGLER_VERSION,
        "workerd": WORKERD_VERSION,
        "harness_sha256": expected_harness,
    }


def prepare(checkout: pathlib.Path) -> None:
    if checkout.exists():
        verify_checkout(checkout)
    else:
        checkout.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "clone", "--depth", "1", "--branch", TAG, UPSTREAM_URL, str(checkout)], check=True)
        verify_checkout(checkout)
    subprocess.run(["npm", "ci"], cwd=checkout, check=True)
    subprocess.run(["npm", "run", "build"], cwd=checkout, check=True)
    install_harness(checkout)
    version = subprocess.run(["npx", "wrangler", "--version"], cwd=checkout, check=True, capture_output=True, text=True).stdout.strip()
    if version != WRANGLER_VERSION:
        raise ValueError(f"unexpected Wrangler version: {version}")
    verify_checkout(checkout, require_harness=True)


def request_bytes(method: str, url: str, body: bytes | None = None, content_type: str | None = None, timeout: float = 30) -> tuple[int, bytes]:
    headers = {}
    if content_type:
        headers["content-type"] = content_type
    request = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, response.read(MAX_FILE + 1)
    except urllib.error.HTTPError as error:
        return error.code, error.read(MAX_FILE + 1)


def wait_ready(base_url: str, process: subprocess.Popen[bytes], deadline: float) -> None:
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("Wrangler exited before readiness")
        try:
            status, _ = request_bytes("GET", base_url, timeout=1)
            if status == 200:
                return
        except (OSError, urllib.error.URLError, TimeoutError):
            pass
        time.sleep(0.1)
    raise TimeoutError("Wrangler readiness deadline exceeded")


def run_trial(checkout: pathlib.Path, request_path: pathlib.Path, port: int) -> dict[str, Any]:
    identity = verify_checkout(checkout, require_harness=True)
    request = load_request(request_path)
    if not (1024 <= port <= 65535):
        raise ValueError("invalid port")
    base = f"http://127.0.0.1:{port}"
    lifecycle_started = time.monotonic_ns()
    with tempfile.TemporaryDirectory(prefix="cf-computer-local-") as persistent, tempfile.TemporaryFile() as log:
        process = subprocess.Popen(
            ["npx", "wrangler", "dev", "--config", f"{HARNESS_DESTINATION}/wrangler.jsonc", "--port", str(port), "--persist-to", persistent],
            cwd=checkout,
            stdin=subprocess.DEVNULL,
            stdout=log,
            stderr=subprocess.STDOUT,
        )
        try:
            wait_ready(base, process, time.monotonic() + 60)
            ready_ns = time.monotonic_ns()
            workspace = request["workspace_id"]
            for path, content in sorted(request["files"].items()):
                status, payload = request_bytes("PUT", f"{base}/c/{workspace}/file/workspace/{path}", content.encode(), "application/octet-stream")
                if status != 204 or payload:
                    raise RuntimeError(f"fixture write failed: {status}")
            if request["tool_fixture"] is not None:
                status, payload = request_bytes(
                    "PUT",
                    f"{base}/c/{workspace}/fixture",
                    canonical(request["tool_fixture"]),
                    "application/json",
                )
                if status != 204 or payload:
                    raise RuntimeError(f"tool fixture write failed: {status}")
            seeded_ns = time.monotonic_ns()
            exec_payload = canonical({"source": request["source"], "input": request["input"], "cwd": "/workspace"})
            status, payload = request_bytes("POST", f"{base}/c/{workspace}/exec", exec_payload, "application/json", timeout=60)
            if status != 200 or len(payload) > MAX_FILE:
                raise RuntimeError(f"Computer execution failed: {status}")
            execution = json.loads(payload)
            executed_ns = time.monotonic_ns()
            outputs: dict[str, str] = {}
            for path in request["output_files"]:
                status, payload = request_bytes("GET", f"{base}/c/{workspace}/file/workspace/{path}")
                if status != 200 or len(payload) > MAX_FILE:
                    raise RuntimeError(f"output read failed: {path}: {status}")
                outputs[path] = payload.decode("utf-8")
            status, payload = request_bytes("GET", f"{base}/c/{workspace}/trace")
            if status != 200 or len(payload) > MAX_FILE:
                raise RuntimeError(f"tool trace read failed: {status}")
            tool_trace = json.loads(payload)
            observed_ns = time.monotonic_ns()
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            stopped_ns = time.monotonic_ns()
            log.seek(0)
            log_digest = "sha256:" + sha256(log.read())
    return {
        "schema_version": "cloudflare-computer-local-result/v1",
        "identity": identity,
        "request_sha256": "sha256:" + sha256(canonical(request)),
        "execution": execution,
        "output_files": outputs,
        "tool_trace": tool_trace,
        "lifecycle": {
            "startup_ns": ready_ns - lifecycle_started,
            "fixture_ns": seeded_ns - ready_ns,
            "execution_ns": executed_ns - seeded_ns,
            "observation_ns": observed_ns - executed_ns,
            "shutdown_ns": stopped_ns - observed_ns,
            "wall_ns": stopped_ns - lifecycle_started,
        },
        "wrangler_log_sha256": log_digest,
    }


def write_new(path: pathlib.Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    fd = os.open(path, flags, 0o600)
    with os.fdopen(fd, "wb") as handle:
        handle.write(canonical(value))


def main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    prepare_parser = sub.add_parser("prepare")
    prepare_parser.add_argument("--checkout", type=pathlib.Path, required=True)
    run_parser = sub.add_parser("run")
    run_parser.add_argument("--checkout", type=pathlib.Path, required=True)
    run_parser.add_argument("--request", type=pathlib.Path, required=True)
    run_parser.add_argument("--result", type=pathlib.Path, required=True)
    run_parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    if args.command == "prepare":
        prepare(args.checkout.resolve())
        return 0
    result = run_trial(args.checkout.resolve(), args.request.resolve(), args.port)
    write_new(args.result.resolve(), result)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
