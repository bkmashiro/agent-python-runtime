#!/usr/bin/env python3
"""Merge complete, mutually source-bound per-host sweep manifests."""

from __future__ import annotations

import argparse
import json
import pathlib
import re
from typing import Any, Iterable


HOST_IDS = tuple(f"gpu{number}" for number in range(31, 36))
MANIFEST_SCHEMA = "pysolate.linux-evaluation-sweeps.v1"
MERGED_SCHEMA = "pysolate.linux-evaluation-sweeps-merged.v1"
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def load(path: pathlib.Path) -> dict[str, Any]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"missing regular host manifest: {path}")
    try:
        value = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid host manifest JSON: {path}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"host manifest must be an object: {path}")
    return value


def _selected_hosts(value: Iterable[str] | str) -> list[str]:
    if isinstance(value, str):
        values = value.split(",")
    else:
        values = list(value)
    result = [item.strip() for item in values if item.strip()]
    if not result or any(item not in HOST_IDS for item in result) or len(set(result)) != len(result):
        raise ValueError("selected hosts must be a unique non-empty subset of gpu31..gpu35")
    return result


def _source(document: dict[str, Any], label: str) -> dict[str, Any]:
    source = document.get("source")
    if not isinstance(source, dict) or set(source) != {"commit", "tree", "epoch"}:
        raise ValueError(f"{label} source schema drift")
    if not isinstance(source["commit"], str) or not re.fullmatch(r"[0-9a-f]{40}", source["commit"]):
        raise ValueError(f"{label} source commit drift")
    if not isinstance(source["tree"], str) or not re.fullmatch(r"[0-9a-f]{40}", source["tree"]):
        raise ValueError(f"{label} source tree drift")
    if not isinstance(source["epoch"], int) or isinstance(source["epoch"], bool) or source["epoch"] <= 0:
        raise ValueError(f"{label} source epoch drift")
    return source


def _artifacts(document: dict[str, Any], label: str) -> dict[str, Any]:
    artifacts = document.get("artifacts")
    if not isinstance(artifacts, dict) or set(artifacts) != {"base", "numpy_core"}:
        raise ValueError(f"{label} artifact schema drift")
    normalized: dict[str, Any] = {}
    for name in ("base", "numpy_core"):
        item = artifacts[name]
        if not isinstance(item, dict) or set(item) != {"path", "sha256"} or not isinstance(item["path"], str) or DIGEST.fullmatch(str(item["sha256"])) is None:
            raise ValueError(f"{label} artifact schema drift")
        normalized[name] = {"path": item["path"], "sha256": item["sha256"]}
    return normalized


def _schemas(document: dict[str, Any], label: str) -> dict[str, Any]:
    schemas = document.get("schemas")
    expected = {
        "platform": "pysolate.platform.v1",
        "plm_crossover": "pysolate.plm-crossover-economics.v1",
        "cow_fanout": "pysolate.cow-fanout-economics.v1",
    }
    if schemas != expected:
        raise ValueError(f"{label} schema drift")
    return schemas


def _shared_config(document: dict[str, Any], label: str) -> dict[str, Any]:
    config = document.get("config")
    if not isinstance(config, dict):
        raise ValueError(f"{label} config drift")
    required = {"plm_crossover_runs", "cow_fanout_runs", "order_offset"}
    if set(config) != required or any(not isinstance(config[key], int) or isinstance(config[key], bool) for key in required):
        raise ValueError(f"{label} config drift")
    if config["plm_crossover_runs"] < 3 or config["cow_fanout_runs"] < 3 or config["order_offset"] < 0:
        raise ValueError(f"{label} config drift")
    return {"plm_crossover_runs": config["plm_crossover_runs"], "cow_fanout_runs": config["cow_fanout_runs"]}


def _host(document: dict[str, Any], label: str) -> dict[str, Any]:
    host = document.get("host")
    if not isinstance(host, dict) or not isinstance(host.get("id"), str) or host["id"] not in HOST_IDS or not isinstance(host.get("hostname"), str) or not host["hostname"]:
        raise ValueError(f"{label} host schema drift")
    return host


def merge_manifests(
    manifest_paths: Iterable[pathlib.Path | str],
    selected_hosts: Iterable[str] | str,
    source_commit: str | None = None,
    source_tree: str | None = None,
    source_epoch: int | None = None,
) -> dict[str, Any]:
    """Merge selected manifests while retaining each complete host block verbatim."""

    selected = _selected_hosts(selected_hosts)
    paths = [pathlib.Path(path) for path in manifest_paths]
    if len(paths) != len(selected):
        raise ValueError("selected host manifest count does not match selected hosts")
    documents = [load(path) for path in paths]
    seen: set[str] = set()
    source_ref: dict[str, Any] | None = None
    artifact_ref: dict[str, Any] | None = None
    schema_ref: dict[str, Any] | None = None
    config_ref: dict[str, Any] | None = None
    host_blocks: list[dict[str, Any]] = []
    order_offsets: dict[str, int] = {}

    for index, document in enumerate(documents):
        label = f"manifest {paths[index]}"
        if document.get("schema_version") != MANIFEST_SCHEMA or document.get("complete") is not True:
            raise ValueError(f"{label} incomplete or schema drift")
        source = _source(document, label)
        host = _host(document, label)
        host_id = host["id"]
        if host_id in seen or host_id != selected[index]:
            raise ValueError("selected host manifest identity mismatch")
        seen.add(host_id)
        artifacts = _artifacts(document, label)
        schemas = _schemas(document, label)
        shared_config = _shared_config(document, label)
        config = document["config"]
        order_offsets[host_id] = config["order_offset"]
        for name, expected, drift in (
            ("source", source, "source drift"),
            ("artifacts", artifacts, "artifact drift"),
            ("schemas", schemas, "schema drift"),
            ("shared config", shared_config, "config drift"),
        ):
            if name == "source":
                if source_ref is None:
                    source_ref = expected
                elif source_ref != expected:
                    raise ValueError(drift)
            elif name == "artifacts":
                if artifact_ref is None:
                    artifact_ref = expected
                elif artifact_ref != expected:
                    raise ValueError(drift)
            elif name == "schemas":
                if schema_ref is None:
                    schema_ref = expected
                elif schema_ref != expected:
                    raise ValueError(drift)
            else:
                if config_ref is None:
                    config_ref = expected
                elif config_ref != expected:
                    raise ValueError(drift)
        host_blocks.append(document)

    if seen != set(selected):
        raise ValueError("selected host manifest identity mismatch")
    assert source_ref is not None and artifact_ref is not None and schema_ref is not None and config_ref is not None
    if source_commit is not None and source_ref["commit"] != source_commit:
        raise ValueError("source commit drift")
    if source_tree is not None and source_ref["tree"] != source_tree:
        raise ValueError("source tree drift")
    if source_epoch is not None and source_ref["epoch"] != source_epoch:
        raise ValueError("source epoch drift")

    return {
        "schema_version": MERGED_SCHEMA,
        "complete": True,
        "source": source_ref,
        "selected_hosts": selected,
        "schemas": schema_ref,
        "config": {**config_ref, "order_offsets": order_offsets},
        "artifacts": artifact_ref,
        "host_blocks": host_blocks,
    }


# A descriptive alias keeps callers independent of the CLI name.
merge = merge_manifests


def _manifest_paths(args: argparse.Namespace, selected: list[str]) -> list[pathlib.Path]:
    supplied: list[str] = []
    supplied.extend(args.manifest or [])
    supplied.extend(args.manifests or [])
    if supplied:
        return [pathlib.Path(path) for path in supplied]
    if args.root is None:
        raise ValueError("provide --manifest paths or --root")
    root = args.root
    paths: list[pathlib.Path] = []
    for host in selected:
        candidates = (root / host / "manifest.json", root / host / "evaluation-sweeps/manifest.json")
        for candidate in candidates:
            if candidate.is_file() and not candidate.is_symlink():
                paths.append(candidate)
                break
        else:
            raise ValueError(f"missing selected host manifest: {host}")
    return paths


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--selected-hosts", required=True)
    parser.add_argument("--manifest", action="append")
    parser.add_argument("--manifests", nargs="+")
    parser.add_argument("--root", type=pathlib.Path)
    parser.add_argument("--source-commit")
    parser.add_argument("--source-tree")
    parser.add_argument("--source-epoch", type=int)
    args = parser.parse_args()
    selected = _selected_hosts(args.selected_hosts)
    result = merge_manifests(_manifest_paths(args, selected), selected, args.source_commit, args.source_tree, args.source_epoch)
    if args.output.exists() or args.output.is_symlink():
        raise ValueError("merge output already exists")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
