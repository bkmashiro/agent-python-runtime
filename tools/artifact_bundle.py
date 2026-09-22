#!/usr/bin/env python3
"""Verify, bundle, and install the pinned Pysolate guest artifact."""

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
from typing import Any, Dict
from urllib.parse import urlparse

ARTIFACT_NAME = "pysolate.wasm"
MANIFEST_NAME = "pysolate.manifest.json"
EXPECTED_PROFILE = "agent-core"
EXPECTED_TARGET = "wasm32-wasip1"
MAX_ARTIFACT_BYTES = 128 * 1024 * 1024
MAX_MANIFEST_BYTES = 1024 * 1024


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _load_manifest(path: Path) -> Dict[str, Any]:
    if not path.is_file():
        raise ValueError("manifest does not exist: {}".format(path))
    if path.stat().st_size > MAX_MANIFEST_BYTES:
        raise ValueError("manifest is too large")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError("invalid artifact manifest: {}".format(exc)) from exc
    if not isinstance(value, dict):
        raise ValueError("artifact manifest must be an object")
    return value


def verify_artifact(artifact: Path, manifest: Path) -> Dict[str, Any]:
    artifact = Path(artifact)
    manifest = Path(manifest)
    data = _load_manifest(manifest)
    if data.get("schema_version") != 1:
        raise ValueError("unsupported artifact manifest schema")
    if data.get("profile") != EXPECTED_PROFILE:
        raise ValueError("artifact profile must be {}".format(EXPECTED_PROFILE))
    if data.get("target") != EXPECTED_TARGET:
        raise ValueError("artifact target must be {}".format(EXPECTED_TARGET))

    identity = data.get("artifact")
    if not isinstance(identity, dict):
        raise ValueError("artifact identity is missing")
    if identity.get("filename") != ARTIFACT_NAME:
        raise ValueError("artifact filename must be {}".format(ARTIFACT_NAME))
    if not artifact.is_file():
        raise ValueError("artifact does not exist: {}".format(artifact))
    size = artifact.stat().st_size
    if size > MAX_ARTIFACT_BYTES:
        raise ValueError("artifact is too large")
    if identity.get("bytes") != size:
        raise ValueError("artifact size does not match manifest")
    expected_digest = identity.get("sha256")
    if not isinstance(expected_digest, str) or len(expected_digest) != 64:
        raise ValueError("artifact digest is invalid")
    if file_sha256(artifact) != expected_digest.lower():
        raise ValueError("artifact digest does not match manifest")
    with artifact.open("rb") as stream:
        if stream.read(4) != b"\x00asm":
            raise ValueError("artifact is not a WebAssembly module")
    return data


def _tar_info(path: Path, archive_name: str) -> tarfile.TarInfo:
    info = tarfile.TarInfo(archive_name)
    info.size = path.stat().st_size
    info.mode = 0o644
    info.uid = 0
    info.gid = 0
    info.uname = ""
    info.gname = ""
    info.mtime = 0
    return info


def create_bundle(artifact: Path, manifest: Path, output: Path) -> None:
    artifact = Path(artifact)
    manifest = Path(manifest)
    output = Path(output)
    verify_artifact(artifact, manifest)
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_name(output.name + ".tmp")
    try:
        with temporary.open("wb") as raw:
            with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
                with tarfile.open(fileobj=compressed, mode="w") as archive:
                    for path, name in ((artifact, ARTIFACT_NAME), (manifest, MANIFEST_NAME)):
                        with path.open("rb") as stream:
                            archive.addfile(_tar_info(path, name), stream)
        os.replace(str(temporary), str(output))
    finally:
        if temporary.exists():
            temporary.unlink()


def _download_or_copy(source: str, destination: Path) -> None:
    parsed = urlparse(source)
    if parsed.scheme:
        if parsed.scheme != "https" or not parsed.netloc:
            raise ValueError("bundle URL must use HTTPS")
        subprocess.run(
            ["curl", "-fL", "--retry", "2", "--connect-timeout", "20", "-o", str(destination), source],
            check=True,
        )
        return
    source_path = Path(source).expanduser()
    if not source_path.is_file():
        raise ValueError("bundle does not exist: {}".format(source_path))
    shutil.copyfile(str(source_path), str(destination))


def _extract_bundle(bundle: Path, destination: Path) -> None:
    expected = {ARTIFACT_NAME: MAX_ARTIFACT_BYTES, MANIFEST_NAME: MAX_MANIFEST_BYTES}
    try:
        with tarfile.open(str(bundle), mode="r:gz") as archive:
            members = archive.getmembers()
            names = {member.name for member in members}
            if names != set(expected) or len(members) != len(expected):
                raise ValueError("bundle entries must be exactly {}".format(", ".join(sorted(expected))))
            for member in members:
                if not member.isfile() or member.size < 0 or member.size > expected[member.name]:
                    raise ValueError("bundle entries must be bounded regular files")
                stream = archive.extractfile(member)
                if stream is None:
                    raise ValueError("bundle entry cannot be read: {}".format(member.name))
                target = destination / member.name
                with target.open("wb") as output:
                    shutil.copyfileobj(stream, output, length=1024 * 1024)
                os.chmod(str(target), 0o644)
    except (tarfile.TarError, OSError) as exc:
        raise ValueError("invalid artifact bundle: {}".format(exc)) from exc


def install_bundle(source: str, destination: Path) -> None:
    destination = Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="pysolate-artifact-", dir=str(destination.parent)) as temporary:
        root = Path(temporary)
        bundle = root / "bundle.tar.gz"
        unpacked = root / "unpacked"
        unpacked.mkdir()
        _download_or_copy(source, bundle)
        _extract_bundle(bundle, unpacked)
        verify_artifact(unpacked / ARTIFACT_NAME, unpacked / MANIFEST_NAME)
        destination.mkdir(parents=True, exist_ok=True)
        for name in (ARTIFACT_NAME, MANIFEST_NAME):
            os.replace(str(unpacked / name), str(destination / name))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)

    verify = commands.add_parser("verify", help="verify artifact identity")
    verify.add_argument("--artifact", type=Path, required=True)
    verify.add_argument("--manifest", type=Path, required=True)

    bundle = commands.add_parser("bundle", help="create a deterministic artifact bundle")
    bundle.add_argument("--artifact", type=Path, required=True)
    bundle.add_argument("--manifest", type=Path, required=True)
    bundle.add_argument("--output", type=Path, required=True)

    install = commands.add_parser("install", help="install and verify a local or HTTPS bundle")
    install.add_argument("--source", required=True)
    install.add_argument("--dist", type=Path, required=True)

    args = parser.parse_args()
    if args.command == "verify":
        data = verify_artifact(args.artifact, args.manifest)
        print("verified {} {} {}".format(data["profile"], data["target"], data["artifact"]["sha256"]))
    elif args.command == "bundle":
        create_bundle(args.artifact, args.manifest, args.output)
        print(args.output)
    else:
        install_bundle(args.source, args.dist)
        print(args.dist / ARTIFACT_NAME)


if __name__ == "__main__":
    main()
