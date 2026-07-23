#!/usr/bin/env python3
"""Generate deterministic SPDX JSON and truthful notices for one guest bundle."""

from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import pathlib
import re
import sys
from typing import Any


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def spdx_id(value: str) -> str:
    normalized = re.sub(r"[^A-Za-z0-9.-]+", "-", value).strip("-")
    return normalized or "unnamed"


def checksum(value: str) -> dict[str, str]:
    return {"algorithm": "SHA256", "checksumValue": value}


def vfs_files(vfs_root: pathlib.Path) -> list[pathlib.Path]:
    return sorted(
        (path for path in vfs_root.rglob("*") if path.is_file()),
        key=lambda path: f"/usr/lib/python3.14/{path.relative_to(vfs_root).as_posix()}",
    )


def build_notices(
    lock: dict[str, Any],
    *,
    artifact_name: str,
    artifact_digest: str,
    commit: str,
    epoch_text: str,
    file_count: int,
) -> str:
    included = [
        source
        for source in lock["sources"]
        if source["artifact_relation"] != "build-only"
    ]
    build_only = [
        source
        for source in lock["sources"]
        if source["artifact_relation"] == "build-only"
    ]
    lines = [
        "# Third-party notices and locked provenance",
        "",
        "This notice was generated from the exact source lock and VFS staging tree.",
        "It describes the named private guest artifact; no package or binary release is claimed.",
        "",
        f"- Artifact: `{artifact_name}`",
        f"- SHA-256: `{artifact_digest}`",
        f"- Repository commit: `{commit}`",
        f"- Target: `{lock['target']}`",
        f"- SOURCE_DATE_EPOCH: `{epoch_text}`",
        f"- Packaged VFS files: `{file_count}`",
        "",
        "## Packaged or linked inputs",
        "",
    ]
    for source in included:
        lines.append(
            f"- `{source['id']}` {source['version']} — {source['license']} — "
            f"{source['artifact_relation']} — SHA-256 `{source['sha256']}` — {source['url']}"
        )
    lines.extend(["", "## Build-only inputs", ""])
    for source in build_only:
        lines.append(
            f"- `{source['id']}` {source['version']} — {source['license']} — "
            f"build-only — SHA-256 `{source['sha256']}` — {source['url']}"
        )
    lines.extend(
        [
            "",
            "License identifiers and upstream source locations are locked metadata.",
            "The SPDX JSON contains the per-file VFS inventory and checksums.",
            "",
        ]
    )
    return "\n".join(lines)


def build_outputs(
    *,
    artifact: pathlib.Path,
    manifest_path: pathlib.Path,
    source_lock: pathlib.Path,
    vfs_root: pathlib.Path,
) -> tuple[dict[str, Any], str]:
    manifest = json.loads(manifest_path.read_text())
    lock = json.loads(source_lock.read_text())
    artifact_digest = sha256(artifact)
    artifact_entry = manifest.get("artifact", {})
    if artifact_entry.get("sha256") != artifact_digest:
        raise ValueError("manifest artifact digest does not match artifact")
    if artifact_entry.get("size") != artifact.stat().st_size:
        raise ValueError("manifest artifact size does not match artifact")
    if manifest.get("target") != lock.get("target"):
        raise ValueError("manifest and source-lock targets differ")
    build = manifest.get("build", {})
    commit = build.get("repository_commit")
    epoch_text = build.get("source_date_epoch")
    if not isinstance(commit, str) or not commit:
        raise ValueError("manifest repository commit is missing")
    if not isinstance(epoch_text, str) or not epoch_text.isdigit():
        raise ValueError("manifest SOURCE_DATE_EPOCH is invalid")
    created = datetime.datetime.fromtimestamp(
        int(epoch_text), tz=datetime.timezone.utc
    ).strftime("%Y-%m-%dT%H:%M:%SZ")

    artifact_spdx = "SPDXRef-Package-agent-python-runtime"
    packages = [
        {
            "SPDXID": artifact_spdx,
            "name": artifact.name,
            "versionInfo": commit,
            "downloadLocation": "NOASSERTION",
            "filesAnalyzed": True,
            "checksums": [checksum(artifact_digest)],
            "licenseConcluded": "NOASSERTION",
            "licenseDeclared": "NOASSERTION",
            "copyrightText": "NOASSERTION",
            "primaryPackagePurpose": "APPLICATION",
        }
    ]
    relationships = [
        {
            "spdxElementId": "SPDXRef-DOCUMENT",
            "relationshipType": "DESCRIBES",
            "relatedSpdxElement": artifact_spdx,
        }
    ]
    for source in lock["sources"]:
        source_spdx = f"SPDXRef-Package-{spdx_id(source['id'])}"
        relation = source["artifact_relation"]
        purpose = "LIBRARY" if relation in {"packaged", "linked"} else "OTHER"
        packages.append(
            {
                "SPDXID": source_spdx,
                "name": source["id"],
                "versionInfo": source["version"],
                "downloadLocation": source["url"],
                "filesAnalyzed": False,
                "checksums": [checksum(source["sha256"])],
                "licenseConcluded": "NOASSERTION",
                "licenseDeclared": source["license"],
                "copyrightText": "NOASSERTION",
                "primaryPackagePurpose": purpose,
                "comment": f"role={source['role']}; artifact_relation={relation}",
            }
        )
        if relation == "packaged":
            relationship_type = "CONTAINS"
            element, related = artifact_spdx, source_spdx
        elif relation == "linked":
            relationship_type = "STATIC_LINK"
            element, related = artifact_spdx, source_spdx
        else:
            relationship_type = "BUILD_TOOL_OF"
            element, related = source_spdx, artifact_spdx
        relationships.append(
            {
                "spdxElementId": element,
                "relationshipType": relationship_type,
                "relatedSpdxElement": related,
            }
        )

    files = []
    for path in vfs_files(vfs_root):
        relative = path.relative_to(vfs_root).as_posix()
        file_digest = sha256(path)
        path_digest = hashlib.sha256(relative.encode("utf-8")).hexdigest()[:16]
        file_spdx = f"SPDXRef-File-{path_digest}-{file_digest}"
        files.append(
            {
                "SPDXID": file_spdx,
                "fileName": f"/usr/lib/python3.14/{relative}",
                "checksums": [checksum(file_digest)],
                "licenseConcluded": "NOASSERTION",
                "copyrightText": "NOASSERTION",
            }
        )
        relationships.append(
            {
                "spdxElementId": artifact_spdx,
                "relationshipType": "CONTAINS",
                "relatedSpdxElement": file_spdx,
            }
        )

    sbom = {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"agent-python-runtime-{commit}",
        "documentNamespace": (
            "https://github.com/bkmashiro/agent-python-runtime/spdx/"
            f"{commit}/{artifact_digest}"
        ),
        "creationInfo": {
            "created": created,
            "creators": ["Tool: agent-python-runtime/write-supply-chain.py-v1"],
        },
        "documentDescribes": [artifact_spdx],
        "packages": packages,
        "files": files,
        "relationships": relationships,
        "annotations": [
            {
                "annotationDate": created,
                "annotationType": "OTHER",
                "annotator": "Tool: agent-python-runtime/write-supply-chain.py-v1",
                "comment": (
                    f"target={lock['target']}; source_date_epoch={epoch_text}; "
                    f"manifest_sha256={sha256(manifest_path)}"
                ),
            }
        ],
    }

    notices = build_notices(
        lock,
        artifact_name=artifact.name,
        artifact_digest=artifact_digest,
        commit=commit,
        epoch_text=epoch_text,
        file_count=len(files),
    )
    return sbom, notices


def validate_bundle_outputs(
    sbom: dict[str, Any],
    notices: str,
    artifact: pathlib.Path,
    manifest_path: pathlib.Path,
    source_lock: pathlib.Path,
) -> list[str]:
    errors: list[str] = []
    try:
        manifest = json.loads(manifest_path.read_text())
        lock = json.loads(source_lock.read_text())
        artifact_digest = sha256(artifact)
    except (OSError, json.JSONDecodeError) as error:
        return [f"bundle evidence read failed: {error}"]
    artifact_record = manifest.get("artifact", {})
    build = manifest.get("build", {})
    commit = build.get("repository_commit")
    epoch_text = build.get("source_date_epoch")
    if artifact_record.get("sha256") != artifact_digest or artifact_record.get("size") != artifact.stat().st_size:
        errors.append("manifest artifact identity does not match artifact")
    if manifest.get("target") != lock.get("target"):
        errors.append("manifest and source-lock targets differ")
    if manifest.get("sources") != lock.get("sources"):
        errors.append("manifest sources do not match source lock")
    try:
        created = datetime.datetime.fromtimestamp(
            int(epoch_text), tz=datetime.timezone.utc
        ).strftime("%Y-%m-%dT%H:%M:%SZ")
    except (TypeError, ValueError, OverflowError):
        created = None
        errors.append("manifest SOURCE_DATE_EPOCH is invalid")

    expected_namespace = (
        "https://github.com/bkmashiro/agent-python-runtime/spdx/"
        f"{commit}/{artifact_digest}"
    )
    expected_top = {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"agent-python-runtime-{commit}",
        "documentNamespace": expected_namespace,
        "documentDescribes": ["SPDXRef-Package-agent-python-runtime"],
    }
    for key, value in expected_top.items():
        if sbom.get(key) != value:
            errors.append(f"SPDX {key} does not match artifact identity")
    if sbom.get("creationInfo") != {
        "created": created,
        "creators": ["Tool: agent-python-runtime/write-supply-chain.py-v1"],
    }:
        errors.append("SPDX creationInfo does not match SOURCE_DATE_EPOCH")

    packages = sbom.get("packages")
    package_map = {
        row.get("name"): row for row in packages if isinstance(row, dict)
    } if isinstance(packages, list) else {}
    expected_names = {artifact.name} | {source["id"] for source in lock.get("sources", [])}
    if set(package_map) != expected_names or len(package_map) != len(expected_names):
        errors.append("SPDX package set does not match source lock")
    artifact_package = package_map.get(artifact.name, {})
    if (
        artifact_package.get("SPDXID") != "SPDXRef-Package-agent-python-runtime"
        or artifact_package.get("versionInfo") != commit
        or artifact_package.get("checksums") != [checksum(artifact_digest)]
    ):
        errors.append("SPDX artifact package does not match artifact")
    for source in lock.get("sources", []):
        row = package_map.get(source["id"], {})
        purpose = "LIBRARY" if source["artifact_relation"] in {"packaged", "linked"} else "OTHER"
        if (
            row.get("SPDXID") != f"SPDXRef-Package-{spdx_id(source['id'])}"
            or row.get("versionInfo") != source["version"]
            or row.get("downloadLocation") != source["url"]
            or row.get("checksums") != [checksum(source["sha256"])]
            or row.get("licenseDeclared") != source["license"]
            or row.get("primaryPackagePurpose") != purpose
            or row.get("comment") != f"role={source['role']}; artifact_relation={source['artifact_relation']}"
        ):
            errors.append(f"SPDX package does not match locked source: {source['id']}")

    files = sbom.get("files")
    if not isinstance(files, list):
        files = []
        errors.append("SPDX files must be an array")
    names = [row.get("fileName") for row in files if isinstance(row, dict)]
    ids = [row.get("SPDXID") for row in files if isinstance(row, dict)]
    if len(names) != len(files) or names != sorted(names) or len(set(names)) != len(names):
        errors.append("SPDX VFS file names must be sorted and unique")
    if len(ids) != len(files) or len(set(ids)) != len(ids):
        errors.append("SPDX VFS file IDs must be unique")
    for row in files:
        checksums = row.get("checksums") if isinstance(row, dict) else None
        name = row.get("fileName") if isinstance(row, dict) else None
        if (
            not isinstance(name, str)
            or not name.startswith("/usr/lib/python3.14/")
            or not isinstance(checksums, list)
            or len(checksums) != 1
            or checksums[0].get("algorithm") != "SHA256"
            or not re.fullmatch(r"[0-9a-f]{64}", str(checksums[0].get("checksumValue")))
        ):
            errors.append("SPDX VFS file record is invalid")
            break

    expected_relationships = {
        (
            "SPDXRef-DOCUMENT",
            "DESCRIBES",
            "SPDXRef-Package-agent-python-runtime",
        )
    }
    for source in lock.get("sources", []):
        source_spdx = f"SPDXRef-Package-{spdx_id(source['id'])}"
        relation = source["artifact_relation"]
        if relation == "packaged":
            expected_relationships.add(("SPDXRef-Package-agent-python-runtime", "CONTAINS", source_spdx))
        elif relation == "linked":
            expected_relationships.add(("SPDXRef-Package-agent-python-runtime", "STATIC_LINK", source_spdx))
        else:
            expected_relationships.add((source_spdx, "BUILD_TOOL_OF", "SPDXRef-Package-agent-python-runtime"))
    for row in files:
        if isinstance(row, dict):
            expected_relationships.add(("SPDXRef-Package-agent-python-runtime", "CONTAINS", row.get("SPDXID")))
    relationships = sbom.get("relationships")
    actual_relationships = {
        (
            row.get("spdxElementId"),
            row.get("relationshipType"),
            row.get("relatedSpdxElement"),
        )
        for row in relationships
        if isinstance(row, dict)
    } if isinstance(relationships, list) else set()
    if actual_relationships != expected_relationships or len(actual_relationships) != len(relationships or []):
        errors.append("SPDX relationships do not match artifact, source, and VFS inventory")

    expected_annotation = [
        {
            "annotationDate": created,
            "annotationType": "OTHER",
            "annotator": "Tool: agent-python-runtime/write-supply-chain.py-v1",
            "comment": (
                f"target={lock.get('target')}; source_date_epoch={epoch_text}; "
                f"manifest_sha256={sha256(manifest_path)}"
            ),
        }
    ]
    if sbom.get("annotations") != expected_annotation:
        errors.append("SPDX annotation does not bind manifest and target")

    expected_notices = build_notices(
        lock,
        artifact_name=artifact.name,
        artifact_digest=artifact_digest,
        commit=str(commit),
        epoch_text=str(epoch_text),
        file_count=len(files),
    )
    if notices != expected_notices:
        errors.append("third-party notices do not match source lock and SPDX inventory")
    return errors


def validate_outputs(
    sbom: dict[str, Any],
    notices: str,
    artifact: pathlib.Path,
    manifest_path: pathlib.Path,
    source_lock: pathlib.Path,
    vfs_root: pathlib.Path,
) -> list[str]:
    errors: list[str] = []
    try:
        expected_sbom, expected_notices = build_outputs(
            artifact=artifact,
            manifest_path=manifest_path,
            source_lock=source_lock,
            vfs_root=vfs_root,
        )
    except (OSError, ValueError, KeyError, json.JSONDecodeError) as error:
        return [f"artifact/source validation failed: {error}"]
    if sbom != expected_sbom:
        errors.append("SPDX JSON does not match canonical artifact/source/VFS inputs")
    if notices != expected_notices:
        errors.append("third-party notices do not match canonical source relations")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifact", required=True, type=pathlib.Path)
    parser.add_argument("--manifest", required=True, type=pathlib.Path)
    parser.add_argument("--source-lock", required=True, type=pathlib.Path)
    parser.add_argument("--vfs-root", type=pathlib.Path)
    parser.add_argument("--sbom", required=True, type=pathlib.Path)
    parser.add_argument("--notices", required=True, type=pathlib.Path)
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    if args.verify:
        try:
            sbom = json.loads(args.sbom.read_text())
            notices = args.notices.read_text()
        except (OSError, json.JSONDecodeError) as error:
            print(f"supply-chain evidence read failed: {error}", file=sys.stderr)
            return 2
        if args.vfs_root is not None:
            errors = validate_outputs(
                sbom,
                notices,
                args.artifact,
                args.manifest,
                args.source_lock,
                args.vfs_root,
            )
        else:
            errors = validate_bundle_outputs(
                sbom,
                notices,
                args.artifact,
                args.manifest,
                args.source_lock,
            )
        for error in errors:
            print(error, file=sys.stderr)
        return 1 if errors else 0

    if args.vfs_root is None:
        print("--vfs-root is required when generating supply-chain evidence", file=sys.stderr)
        return 2
    sbom, notices = build_outputs(
        artifact=args.artifact,
        manifest_path=args.manifest,
        source_lock=args.source_lock,
        vfs_root=args.vfs_root,
    )
    args.sbom.parent.mkdir(parents=True, exist_ok=True)
    args.notices.parent.mkdir(parents=True, exist_ok=True)
    args.sbom.write_text(json.dumps(sbom, indent=2, sort_keys=True) + "\n")
    args.notices.write_text(notices)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
