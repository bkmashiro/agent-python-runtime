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

SCHEMA_VERSION = 2
EVIDENCE_CLASS = "phase7-paired-density"
COW_STRATEGY = "cow-ready-single-use"
NON_COW_STRATEGY = "single-use-preinitialized"
WARMUP_PROFILE = "numpy-ready-v1"
CANONICAL_SLOTS = [1, 2, 4, 8, 16, 32, 64]
MAX_INPUT_BYTES = 32 * 1024 * 1024
MAX_RSS_BYTES = 8 << 30
CHILD_TIMEOUT_NS = 4 * 60 * 1_000_000_000


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


def _external_validate(benchmark: Path, schema: Path, artifact: Path, manifest: Path, evidence: Path) -> None:
    completed = subprocess.run(
        [str(benchmark), "-kind", "validate-lifecycle-density", "-input", str(evidence), "-schema", str(schema),
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
    if verdict.get("valid") is not True or verdict.get("schema_version") != 3:
        raise ValidationError(f"standalone validator returned an invalid verdict for {evidence}")
    if not isinstance(verdict.get("samples"), int) or not isinstance(verdict.get("boundaries"), int):
        raise ValidationError(f"standalone validator omitted outcome counts for {evidence}")


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


def _lower_hex(value: Any, length: int) -> bool:
    if not isinstance(value, str) or len(value) != length or value.lower() != value:
        return False
    try:
        bytes.fromhex(value)
    except ValueError:
        return False
    return True


def _validate_arm(evidence: dict[str, Any], strategy: str) -> int:
    if evidence.get("schema_version") != 3 or evidence.get("evidence_class") != "lifecycle-density":
        raise ValidationError("arm is not lifecycle-density schema v3")
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
    repeats = plan.get("repeats_per_slot")
    if not isinstance(repeats, int) or isinstance(repeats, bool) or not 1 <= repeats <= 20:
        raise ValidationError("arm repeat count is invalid")
    if (plan.get("fresh_process_per_sample") is not True or plan.get("max_process_rss_bytes") != MAX_RSS_BYTES or
            plan.get("child_timeout_ns") != CHILD_TIMEOUT_NS):
        raise ValidationError("arm process isolation or fixed guard plan drifted")
    samples = evidence.get("samples")
    boundaries = evidence.get("boundaries", [])
    if not isinstance(samples, list) or not isinstance(boundaries, list) or len(samples) + len(boundaries) != repeats * len(CANONICAL_SLOTS):
        raise ValidationError("arm outcome matrix is incomplete")
    summary = _required_object(evidence, "summary")
    if summary.get("sample_count") != len(samples) or summary.get("boundary_count", 0) != len(boundaries):
        raise ValidationError("arm summary outcome counts drifted")
    return repeats


def _cell_identity(outcome: dict[str, Any], repeats: int, kind: str) -> tuple[int, int]:
    slots = outcome.get("requested_slots")
    repeat_index = outcome.get("repeat_index")
    sample_index = outcome.get("sample_index")
    if (not isinstance(slots, int) or isinstance(slots, bool) or slots not in CANONICAL_SLOTS or
            not isinstance(repeat_index, int) or isinstance(repeat_index, bool) or not 0 <= repeat_index < repeats or
            not isinstance(sample_index, int) or isinstance(sample_index, bool) or sample_index < 0):
        raise ValidationError(f"{kind} cell identity is invalid")
    expected_index = CANONICAL_SLOTS.index(slots) * repeats + repeat_index
    if sample_index != expected_index:
        raise ValidationError(f"{kind} sample index drifted")
    return slots, repeat_index + 1


def _outcome_maps(evidence: dict[str, Any], strategy: str) -> tuple[dict[tuple[int, int], dict[str, Any]], dict[tuple[int, int], dict[str, Any]]]:
    repeats = evidence["plan"]["repeats_per_slot"]
    samples: dict[tuple[int, int], dict[str, Any]] = {}
    boundaries: dict[tuple[int, int], dict[str, Any]] = {}
    for sample in evidence["samples"]:
        if not isinstance(sample, dict):
            raise ValidationError("sample must be an object")
        key = _cell_identity(sample, repeats, "sample")
        if key in samples or key in boundaries:
            raise ValidationError(f"duplicate outcome cell: {key}")
        slots = key[0]
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
        samples[key] = sample
    for boundary in evidence.get("boundaries", []):
        if not isinstance(boundary, dict):
            raise ValidationError("boundary must be an object")
        key = _cell_identity(boundary, repeats, "boundary")
        if key in samples or key in boundaries:
            raise ValidationError(f"duplicate outcome cell: {key}")
        if (strategy != NON_COW_STRATEGY or key[0] != 64 or boundary.get("status") != "rss_guard" or
                boundary.get("guard_rss_bytes") != MAX_RSS_BYTES or
                not isinstance(boundary.get("max_observed_rss_bytes"), int) or isinstance(boundary.get("max_observed_rss_bytes"), bool) or
                boundary["max_observed_rss_bytes"] <= MAX_RSS_BYTES or
                not _lower_hex(boundary.get("process_instance_sha256"), 64)):
            raise ValidationError(f"unsupported boundary outcome: {key}")
        boundaries[key] = boundary
    expected = {(slots, repeat) for slots in CANONICAL_SLOTS for repeat in range(1, repeats + 1)}
    if set(samples) | set(boundaries) != expected or set(samples) & set(boundaries):
        raise ValidationError("outcome keys do not cover the canonical matrix exactly once")
    if strategy == COW_STRATEGY and boundaries:
        raise ValidationError("COW arm cannot carry a boundary outcome")
    for slots in CANONICAL_SLOTS[:-1]:
        if any((slots, repeat) not in samples for repeat in range(1, repeats + 1)):
            raise ValidationError("slots 1-32 must be complete successful samples")
    return samples, boundaries


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
    cow_repeats = _validate_arm(cow, COW_STRATEGY)
    non_cow_repeats = _validate_arm(non_cow, NON_COW_STRATEGY)
    if cow_repeats != non_cow_repeats:
        raise ValidationError("cross-arm repeat count drifted")
    for field in ("artifact", "host_source", "backend", "environment", "warmup", "plan", "metric_semantics", "observability"):
        if cow.get(field) != non_cow.get(field):
            raise ValidationError(f"cross-arm {field} drifted")
    cow_samples, cow_boundaries = _outcome_maps(cow, COW_STRATEGY)
    non_cow_samples, non_cow_boundaries = _outcome_maps(non_cow, NON_COW_STRATEGY)
    if cow_boundaries:
        raise ValidationError("COW boundary outcomes are forbidden")
    pairs: list[dict[str, Any]] = []
    for key in sorted(set(cow_samples) & set(non_cow_samples)):
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
    expected_success_pairs = {(slots, repeat) for slots in CANONICAL_SLOTS[:-1] for repeat in range(1, cow_repeats + 1)}
    if not expected_success_pairs.issubset({(pair["slots"], pair["repeat"]) for pair in pairs}):
        raise ValidationError("paired successful curve is incomplete below the boundary")
    by_slots: list[dict[str, Any]] = []
    for slots in CANONICAL_SLOTS:
        slot_pairs = [pair for pair in pairs if pair["slots"] == slots]
        if not slot_pairs:
            continue
        by_slots.append({
            "slots": slots,
            "median_cow_pss_bytes": int(statistics.median(pair["cow"]["pss_bytes"] for pair in slot_pairs)),
            "median_non_cow_pss_bytes": int(statistics.median(pair["non_cow"]["pss_bytes"] for pair in slot_pairs)),
            "median_pss_saved_bytes": int(statistics.median(pair["derived"]["pss_saved_bytes"] for pair in slot_pairs)),
            "median_pss_reduction_ppm": int(statistics.median(pair["derived"]["pss_reduction_ppm"] for pair in slot_pairs)),
            "median_non_cow_to_cow_ready_time_ppm": int(statistics.median(pair["derived"]["non_cow_to_cow_ready_time_ppm"] for pair in slot_pairs)),
        })
    coverage = []
    for slots in CANONICAL_SLOTS:
        coverage.append({
            "slots": slots,
            "cow_measured": sum((slots, repeat) in cow_samples for repeat in range(1, cow_repeats + 1)),
            "cow_rss_guard": 0,
            "non_cow_measured": sum((slots, repeat) in non_cow_samples for repeat in range(1, cow_repeats + 1)),
            "non_cow_rss_guard": sum((slots, repeat) in non_cow_boundaries for repeat in range(1, cow_repeats + 1)),
        })
    boundary_outcomes = [{
        "slots": slots,
        "repeat": repeat,
        "arm": "non_cow",
        "status": boundary["status"],
        "max_observed_rss_bytes": boundary["max_observed_rss_bytes"],
        "guard_rss_bytes": boundary["guard_rss_bytes"],
        "process_instance_sha256": boundary["process_instance_sha256"],
    } for (slots, repeat), boundary in sorted(non_cow_boundaries.items())]
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
        "coverage_by_slots": coverage,
        "boundary_outcomes": boundary_outcomes,
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


def run(args: argparse.Namespace, external_validator: Callable[[Path, Path, Path, Path, Path], None] = _external_validate) -> None:
    benchmark, schema, artifact, manifest = Path(args.benchmark), Path(args.schema), Path(args.artifact), Path(args.manifest)
    cow_path, non_cow_path, output = Path(args.cow), Path(args.non_cow), Path(args.output)
    external_validator(benchmark, schema, artifact, manifest, cow_path)
    external_validator(benchmark, schema, artifact, manifest, non_cow_path)
    cow, cow_raw = strict_load(cow_path)
    non_cow, non_cow_raw = strict_load(non_cow_path)
    paired = pair_evidence(cow, non_cow, cow_raw, non_cow_raw)
    encoded = (json.dumps(paired, sort_keys=True, indent=2, ensure_ascii=False) + "\n").encode()
    atomic_write(output, encoded)


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser()
    value.add_argument("--benchmark", required=True)
    value.add_argument("--schema", required=True)
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
