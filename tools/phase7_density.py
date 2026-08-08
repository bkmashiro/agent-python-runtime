#!/usr/bin/env python3
"""Fail-closed Phase 7 COW/non-COW NumPy density pairing."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import statistics
import subprocess
import tempfile
from typing import Any, Callable

SCHEMA_VERSION = 1
EVIDENCE_CLASS = "phase7-paired-density"
COW_STRATEGY = "cow-ready-single-use"
NON_COW_STRATEGY = "single-use-preinitialized"
WARMUP_PROFILE = "numpy-ready-v1"
CANONICAL_SLOTS = [1, 2, 4, 8, 16, 32, 64]
MAX_INPUT_BYTES = 32 * 1024 * 1024


class ValidationError(ValueError):
    pass


def _unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValidationError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def strict_load(path: Path) -> tuple[dict[str, Any], bytes]:
    stat = path.lstat()
    if not path.is_file() or path.is_symlink() or stat.st_size <= 0 or stat.st_size > MAX_INPUT_BYTES:
        raise ValidationError(f"input is not a bounded regular file: {path}")
    raw = path.read_bytes()
    try:
        value = json.loads(raw, object_pairs_hook=_unique_object)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise ValidationError(f"invalid JSON {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValidationError(f"top-level JSON is not an object: {path}")
    return value, raw


def sha256_bytes(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def _external_validate(benchmark: Path, artifact: Path, manifest: Path, evidence: Path) -> None:
    completed = subprocess.run(
        [str(benchmark), "-kind", "validate-lifecycle-density", "-input", str(evidence),
         "-artifact", str(artifact), "-manifest", str(manifest)],
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, timeout=120, check=False,
    )
    if completed.returncode != 0:
        raise ValidationError(f"standalone validator rejected {evidence}: {completed.stderr.strip()}")
    try:
        verdict = json.loads(completed.stdout, object_pairs_hook=_unique_object)
    except json.JSONDecodeError as exc:
        raise ValidationError(f"standalone validator returned invalid JSON for {evidence}") from exc
    if verdict.get("valid") is not True or verdict.get("schema_version") != 2:
        raise ValidationError(f"standalone validator returned an invalid verdict for {evidence}")


def _required_object(value: dict[str, Any], key: str) -> dict[str, Any]:
    candidate = value.get(key)
    if not isinstance(candidate, dict):
        raise ValidationError(f"{key} must be an object")
    return candidate


def _metric_value(metric: Any, path: str) -> int:
    if not isinstance(metric, dict) or metric.get("status") != "measured":
        raise ValidationError(f"{path} must be measured")
    value = metric.get("value")
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
        raise ValidationError(f"{path}.value must be a non-negative integer")
    return value


def _validate_arm(evidence: dict[str, Any], strategy: str) -> None:
    if evidence.get("schema_version") != 2 or evidence.get("evidence_class") != "lifecycle-density":
        raise ValidationError("arm is not lifecycle-density schema v2")
    identity = _required_object(evidence, "strategy")
    if identity != {"active": strategy, "fallback": False, "requested": strategy}:
        raise ValidationError(f"arm strategy identity drifted for {strategy}")
    artifact = _required_object(evidence, "artifact")
    if artifact.get("artifact_profile") != "numpy-core":
        raise ValidationError("arm does not bind the numpy-core artifact profile")
    warmup = _required_object(evidence, "warmup")
    if warmup.get("profile") != WARMUP_PROFILE or not _lower_hex(warmup.get("generation_sha256"), 64):
        raise ValidationError("arm warmup identity is invalid")
    plan = _required_object(evidence, "plan")
    if plan.get("workload") != "numpy-ready-idle" or plan.get("slot_counts") != CANONICAL_SLOTS:
        raise ValidationError("arm plan is not the canonical NumPy density sweep")
    repeats = plan.get("repeats")
    if not isinstance(repeats, int) or isinstance(repeats, bool) or not 1 <= repeats <= 20:
        raise ValidationError("arm repeat count is invalid")
    samples = evidence.get("samples")
    if not isinstance(samples, list) or len(samples) != repeats * len(CANONICAL_SLOTS):
        raise ValidationError("arm sample matrix is incomplete")


def _lower_hex(value: Any, length: int) -> bool:
    if not isinstance(value, str) or len(value) != length or value.lower() != value:
        return False
    try:
        bytes.fromhex(value)
    except ValueError:
        return False
    return True


def _sample_map(evidence: dict[str, Any], strategy: str) -> dict[tuple[int, int], dict[str, Any]]:
    result: dict[tuple[int, int], dict[str, Any]] = {}
    for sample in evidence["samples"]:
        if not isinstance(sample, dict):
            raise ValidationError("sample must be an object")
        slots, repeat = sample.get("requested_slots"), sample.get("repeat")
        if not isinstance(slots, int) or isinstance(slots, bool) or slots not in CANONICAL_SLOTS:
            raise ValidationError("sample slot count is invalid")
        if not isinstance(repeat, int) or isinstance(repeat, bool) or repeat < 1:
            raise ValidationError("sample repeat is invalid")
        key = (slots, repeat)
        if key in result:
            raise ValidationError(f"duplicate sample cell: {key}")
        pool = _required_object(sample, "pool")
        if pool.get("target_capacity") != slots or pool.get("ready") != slots or pool.get("accounted_slots") != slots:
            raise ValidationError(f"sample pool is not fully ready: {key}")
        shards = sample.get("runtime_shards")
        expected_shards = 1 if strategy == COW_STRATEGY else (slots + 3) // 4
        if shards != expected_shards:
            raise ValidationError(f"runtime shard topology drifted: {key}")
        if strategy == COW_STRATEGY:
            mappings = _required_object(sample, "cow_mappings")
            if mappings.get("mapping_count") != slots:
                raise ValidationError(f"COW mapping count drifted: {key}")
        elif sample.get("cow_mappings") is not None:
            raise ValidationError(f"non-COW sample carries COW mapping attribution: {key}")
        result[key] = sample
    repeats = evidence["plan"]["repeats"]
    expected = {(slots, repeat) for slots in CANONICAL_SLOTS for repeat in range(1, repeats + 1)}
    if set(result) != expected:
        raise ValidationError("sample keys do not cover the canonical matrix")
    return result


def _arm_metrics(sample: dict[str, Any], cow: bool) -> dict[str, int]:
    process = _required_object(sample, "process")
    phases = _required_object(sample, "phases")
    result = {
        "runtime_shards": sample["runtime_shards"],
        "ready_total_ns": _metric_value(phases.get("total_ns"), "phases.total_ns"),
        "warmup_ns": _metric_value(phases.get("warmup_ns"), "phases.warmup_ns"),
        "rss_bytes": _metric_value(process.get("rss_bytes"), "process.rss_bytes"),
        "pss_bytes": _metric_value(process.get("pss_bytes"), "process.pss_bytes"),
        "private_dirty_bytes": _metric_value(process.get("private_dirty_bytes"), "process.private_dirty_bytes"),
    }
    if result["pss_bytes"] <= 0 or result["ready_total_ns"] <= 0:
        raise ValidationError("PSS and ready time must be positive")
    if cow:
        mappings = _required_object(sample, "cow_mappings")
        result["cow_mapping_pss_bytes"] = _metric_value(mappings.get("pss_bytes"), "cow_mappings.pss_bytes")
    return result


def pair_evidence(cow: dict[str, Any], non_cow: dict[str, Any], cow_raw: bytes, non_cow_raw: bytes) -> dict[str, Any]:
    _validate_arm(cow, COW_STRATEGY)
    _validate_arm(non_cow, NON_COW_STRATEGY)
    for field in ("artifact", "host_source", "backend", "environment", "warmup", "plan", "metric_semantics", "observability"):
        if cow.get(field) != non_cow.get(field):
            raise ValidationError(f"cross-arm {field} drifted")
    cow_samples = _sample_map(cow, COW_STRATEGY)
    non_cow_samples = _sample_map(non_cow, NON_COW_STRATEGY)
    pairs: list[dict[str, Any]] = []
    for key in sorted(cow_samples):
        slots, repeat = key
        cow_metrics = _arm_metrics(cow_samples[key], True)
        non_cow_metrics = _arm_metrics(non_cow_samples[key], False)
        pss_saved = non_cow_metrics["pss_bytes"] - cow_metrics["pss_bytes"]
        pairs.append({
            "slots": slots,
            "repeat": repeat,
            "cow": cow_metrics,
            "non_cow": non_cow_metrics,
            "derived": {
                "pss_saved_bytes": pss_saved,
                "pss_reduction_ppm": pss_saved * 1_000_000 // non_cow_metrics["pss_bytes"],
                "non_cow_to_cow_ready_time_ppm": non_cow_metrics["ready_total_ns"] * 1_000_000 // cow_metrics["ready_total_ns"],
            },
        })
    by_slots: list[dict[str, Any]] = []
    for slots in CANONICAL_SLOTS:
        slot_pairs = [pair for pair in pairs if pair["slots"] == slots]
        by_slots.append({
            "slots": slots,
            "median_cow_pss_bytes": int(statistics.median(pair["cow"]["pss_bytes"] for pair in slot_pairs)),
            "median_non_cow_pss_bytes": int(statistics.median(pair["non_cow"]["pss_bytes"] for pair in slot_pairs)),
            "median_pss_saved_bytes": int(statistics.median(pair["derived"]["pss_saved_bytes"] for pair in slot_pairs)),
            "median_pss_reduction_ppm": int(statistics.median(pair["derived"]["pss_reduction_ppm"] for pair in slot_pairs)),
            "median_non_cow_to_cow_ready_time_ppm": int(statistics.median(pair["derived"]["non_cow_to_cow_ready_time_ppm"] for pair in slot_pairs)),
        })
    return {
        "schema_version": SCHEMA_VERSION,
        "evidence_class": EVIDENCE_CLASS,
        "artifact": cow["artifact"],
        "warmup": cow["warmup"],
        "host_source": cow["host_source"],
        "backend": cow["backend"],
        "environment": cow["environment"],
        "plan": cow["plan"],
        "source_evidence": {
            "cow_sha256": sha256_bytes(cow_raw),
            "non_cow_sha256": sha256_bytes(non_cow_raw),
        },
        "pairs": pairs,
        "summary_by_slots": by_slots,
    }


def atomic_write(path: Path, raw: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(raw)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def run(args: argparse.Namespace, external_validator: Callable[[Path, Path, Path, Path], None] = _external_validate) -> None:
    benchmark, artifact, manifest = Path(args.benchmark), Path(args.artifact), Path(args.manifest)
    cow_path, non_cow_path, output = Path(args.cow), Path(args.non_cow), Path(args.output)
    external_validator(benchmark, artifact, manifest, cow_path)
    external_validator(benchmark, artifact, manifest, non_cow_path)
    cow, cow_raw = strict_load(cow_path)
    non_cow, non_cow_raw = strict_load(non_cow_path)
    paired = pair_evidence(cow, non_cow, cow_raw, non_cow_raw)
    encoded = (json.dumps(paired, sort_keys=True, indent=2, ensure_ascii=False) + "\n").encode()
    atomic_write(output, encoded)


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser()
    value.add_argument("--benchmark", required=True)
    value.add_argument("--artifact", required=True)
    value.add_argument("--manifest", required=True)
    value.add_argument("--cow", required=True)
    value.add_argument("--non-cow", required=True)
    value.add_argument("--output", required=True)
    return value


def main() -> int:
    try:
        run(parser().parse_args())
    except (ValidationError, OSError, subprocess.SubprocessError) as exc:
        print(f"phase7-density: {exc}", file=os.sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
