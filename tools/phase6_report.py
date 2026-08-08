#!/usr/bin/env python3
"""Render deterministic Phase 6 summary JSON and SVG figures from accepted private evidence."""

from __future__ import annotations

import argparse
import hashlib
import html
import json
import re
import statistics
import tarfile
from collections import defaultdict
from pathlib import Path
from typing import Any

EXPECTED_HOST_REVISION = "17872d6a1d52c58cfa4b55826c1a5ef43ed19529"
EXPECTED_HOST_TREE = "e8652d8fff31894ca56cbae2a6464992002124fa"
EXPECTED_ARTIFACT_SHA256 = "f00f22ac94a66f2f2e67573da11ef879f8b5e46622eb9379300cc1e6a5b40a30"
EXPECTED_ARTIFACT_MANIFEST_SHA256 = "458a4e4bbec1ad225f0f3c38357738f1937b1e16d5388f76cdf4c460ce6839fa"
EXPECTED_ARTIFACT_SOURCE = "64666a5aaacf8555f65d47f75c77796432e141e8"
EXPECTED_BINARY_SHA256 = "09253ed8b664337863769523707f0a467cd19ec146013ddc435e15fe2749d893"
EXPECTED_VALIDATOR_SHA256 = "b0ea4daec2bd8ab6d18627f80bca4cbb2aa23641f0662672e2d5c2e07efeb359"
EXPECTED_SELECTION_SHA256 = "2c35b00b352bc3b3dfea7ea10b28d776e18ec847e3283b6c978c92b8745e9b0f"
EXPECTED_ARCHIVE_SHA256 = "fe3077a0621b83459b3991b28e9506145080331d7f640039d9123d9be5a5b059"
EXPECTED_JOB_ID = "271717"
EXPECTED_FORMAL_CELLS = (
    "closed-numpy-v1-s64-c1",
    "closed-numpy-v1-s256-c4",
    "closed-numpy-v1-s256-c8",
    "open-numpy-v1-s256-w8-r25",
    "open-numpy-v1-s256-w8-r100",
    "closed-numpy-mixed-v1-s256-c8",
    "open-numpy-mixed-v1-s256-w8-r10",
    "open-numpy-mixed-v1-s256-w8-r40",
)
OUTPUT_FILES = (
    "phase6-summary.json",
    "closed-loop-knee.svg",
    "open-loop-load.svg",
    "mixed-load.svg",
    "SHA256SUMS",
)


def sha256_file(path: Path) -> str:
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def load_unique_json(path: Path, *, maximum_bytes: int) -> Any:
    size = path.stat().st_size
    if size <= 0 or size > maximum_bytes:
        raise ValueError(f"JSON input outside size bound: {path}")

    def unique(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, item in pairs:
            if key in value:
                raise ValueError(f"duplicate JSON key {key!r}: {path}")
            value[key] = item
        return value

    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=unique)


def measured(container: dict[str, Any], name: str) -> int:
    metric = container[name]
    if metric != {"status": "measured", "value": metric.get("value")}:
        raise ValueError(f"metric {name} is not canonical measured telemetry")
    value = metric["value"]
    if not isinstance(value, int) or value < 0:
        raise ValueError(f"metric {name} has invalid value")
    return value


def _stats(values: list[float | int | None]) -> dict[str, Any] | None:
    if all(value is None for value in values):
        return None
    if any(value is None for value in values):
        raise ValueError("metric is missing from only part of a repeat set")
    raw = [float(value) for value in values if value is not None]
    return {
        "raw": raw,
        "median": statistics.median(raw),
        "min": min(raw),
        "max": max(raw),
    }


def build_summary(records: list[dict[str, Any]]) -> dict[str, Any]:
    expected = {(cell, repetition) for cell in EXPECTED_FORMAL_CELLS for repetition in range(1, 4)}
    actual = {(record["cell_id"], record["repetition"]) for record in records}
    if len(records) != 24 or actual != expected:
        raise ValueError("formal matrix identity is not exactly eight cells x three repetitions")
    grouped: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for record in records:
        grouped[record["cell_id"]].append(record)
    points: list[dict[str, Any]] = []
    identity_fields = ("arrival_mode", "arrival_rate_per_second", "slots", "consumers", "workload")
    metric_fields = (
        "throughput_per_second", "latency_p50_ms", "latency_p95_ms", "latency_p99_ms",
        "latency_mean_ms", "replenish_drain_ms", "cpu_core_utilization", "offered_requests",
        "accepted_requests", "rejected_requests", "completed_requests", "failed_requests",
        "timed_out_requests", "validated_results", "ready_after", "max_active_private_dirty_mib",
        "max_active_pss_mib", "active_sample_count", "oom_events", "oom_kill_events",
        "psi_some_total_us", "pool_failures", "ready_alias_virtual_gib", "prepared_allocated_mib",
        "final_process_pss_mib", "cgroup_memory_peak_mib",
    )
    for cell in EXPECTED_FORMAL_CELLS:
        rows = sorted(grouped[cell], key=lambda value: value["repetition"])
        point: dict[str, Any] = {"cell_id": cell, "repetitions": 3}
        for field in identity_fields:
            values = {row[field] for row in rows}
            if len(values) != 1:
                raise ValueError(f"repeat identity drift for {cell}: {field}")
            point[field] = values.pop()
        for field in metric_fields:
            point[field] = _stats([row[field] for row in rows])
        if any(row["replenish_status"] != "complete" for row in rows):
            raise ValueError(f"incomplete recovery in {cell}")
        points.append(point)
    return {
        "schema_version": 1,
        "summary_kind": "phase6-numpy-density-formal-summary",
        "host_revision": EXPECTED_HOST_REVISION,
        "host_tree": EXPECTED_HOST_TREE,
        "artifact_sha256": EXPECTED_ARTIFACT_SHA256,
        "artifact_manifest_sha256": EXPECTED_ARTIFACT_MANIFEST_SHA256,
        "artifact_source_revision": EXPECTED_ARTIFACT_SOURCE,
        "benchmark_binary_sha256": EXPECTED_BINARY_SHA256,
        "validator_sha256": EXPECTED_VALIDATOR_SHA256,
        "formal_selection_sha256": EXPECTED_SELECTION_SHA256,
        "formal_archive_sha256": EXPECTED_ARCHIVE_SHA256,
        "slurm_job_id": EXPECTED_JOB_ID,
        "resource_shape": {"partition": "t4", "gpu": "tesla_t4:1", "cpus": 4, "memory_gib": 16},
        "benchmark_policy": {"max_memory_gib": 8, "reserve_gib": 2, "max_cpu": 4, "greed": 50, "derived_max_active": 10},
        "formal_records": 24,
        "dispersion": "median and full min-max across three exact-source repetitions; not a confidence interval",
        "points": points,
    }


def _record_from_evidence(cell: dict[str, Any], evidence: dict[str, Any]) -> dict[str, Any]:
    load = evidence["load"]
    arrival = load["arrival"]
    samples = load["load_samples"] if "load_samples" in load else evidence["load_samples"]
    active = [sample for sample in samples if sample["phase"] == "load-active"]
    final = [sample for sample in samples if sample["phase"] == "load-final"]
    if len(final) != 1:
        raise ValueError("evidence does not contain exactly one final load sample")
    final_sample = final[0]
    active_dirty = max((measured(sample["process"], "private_dirty_bytes") for sample in active), default=None)
    active_pss = max((measured(sample["process"], "pss_bytes") for sample in active), default=None)
    all_samples = evidence["spawn_snapshots"] + samples
    if evidence["policy"]["max_active"] != 10 or evidence["policy"]["version"] != "production-policy-v1":
        raise ValueError("compiled production policy identity drift")
    if evidence["host_source"]["revision"] != EXPECTED_HOST_REVISION:
        raise ValueError("evidence Host revision drift")
    if evidence["artifact"]["sha256"] != EXPECTED_ARTIFACT_SHA256:
        raise ValueError("evidence artifact identity drift")
    return {
        "cell_id": cell["cell_id"],
        "repetition": cell["repetition"],
        "arrival_mode": arrival["mode"],
        "arrival_rate_per_second": arrival["rate_per_second"],
        "slots": cell["slots"],
        "consumers": cell["consumers"],
        "workload": cell["workload"],
        "throughput_per_second": load["throughput_per_second"],
        "latency_p50_ms": load["latency_p50_ns"] / 1_000_000,
        "latency_p95_ms": load["latency_p95_ns"] / 1_000_000,
        "latency_p99_ms": load["latency_p99_ns"] / 1_000_000,
        "latency_mean_ms": load["latency_mean_ns"] / 1_000_000,
        "replenish_drain_ms": load["replenish_drain_ns"] / 1_000_000,
        "cpu_core_utilization": load["cpu_core_utilization"],
        "offered_requests": arrival["offered_requests"],
        "accepted_requests": arrival["accepted_requests"],
        "rejected_requests": arrival["rejected_requests"],
        "completed_requests": load["completed_requests"],
        "failed_requests": load["failed_requests"],
        "timed_out_requests": load["timed_out_requests"],
        "validated_results": load["validated_results"],
        "ready_after": load["ready_after"],
        "replenish_status": load["replenish_status"],
        "max_active_private_dirty_mib": None if active_dirty is None else active_dirty / 2**20,
        "max_active_pss_mib": None if active_pss is None else active_pss / 2**20,
        "active_sample_count": len(active),
        "oom_events": max(measured(sample["cgroup"], "memory_events_oom_total") for sample in all_samples),
        "oom_kill_events": max(measured(sample["cgroup"], "memory_events_oom_kill_total") for sample in all_samples),
        "psi_some_total_us": max(measured(sample["cgroup"], "pressure_some_total_us") for sample in all_samples),
        "pool_failures": max(sample["pool"]["total_failures"] for sample in all_samples),
        "ready_alias_virtual_gib": measured(final_sample["cow_mappings"], "virtual_bytes") / 2**30,
        "prepared_allocated_mib": final_sample["prepared_image"]["allocated_bytes"] / 2**20,
        "final_process_pss_mib": measured(final_sample["process"], "pss_bytes") / 2**20,
        "cgroup_memory_peak_mib": max(measured(sample["cgroup"], "memory_peak_bytes") for sample in all_samples) / 2**20,
    }


def load_formal_records(job_root: Path) -> list[dict[str, Any]]:
    ack = load_unique_json(job_root / "CONTROLLER_ACK.json", maximum_bytes=16 << 10)
    expected_ack = {
        "archive_sha256": EXPECTED_ARCHIVE_SHA256,
        "artifact_manifest_sha256": EXPECTED_ARTIFACT_MANIFEST_SHA256,
        "artifact_sha256": EXPECTED_ARTIFACT_SHA256,
        "artifact_source_revision": EXPECTED_ARTIFACT_SOURCE,
        "benchmark_binary_sha256": EXPECTED_BINARY_SHA256,
        "formal_selection_sha256": EXPECTED_SELECTION_SHA256,
        "host_revision": EXPECTED_HOST_REVISION,
        "host_tree": EXPECTED_HOST_TREE,
        "job_id": EXPECTED_JOB_ID,
        "sacct_verified": False,
        "scontrol_verified": True,
        "slurm_state": "COMPLETED",
        "tier": "formal",
        "validated_records": 24,
        "validator_sha256": EXPECTED_VALIDATOR_SHA256,
    }
    if ack != expected_ack:
        raise ValueError("formal controller ACK identity drift")

    archive_path = job_root / "archive.tar.gz"
    if archive_path.stat().st_size <= 0 or archive_path.stat().st_size > 64 << 20:
        raise ValueError("formal archive is outside the report size bound")
    if sha256_file(archive_path) != EXPECTED_ARCHIVE_SHA256:
        raise ValueError("formal archive identity drift")
    result = job_root / "extracted" / "result"

    archive_json: dict[str, bytes] = {}
    with tarfile.open(archive_path, mode="r:gz") as archive:
        members = archive.getmembers()
        names = [member.name for member in members]
        if len(names) != len(set(names)):
            raise ValueError("formal archive contains duplicate members")
        total_json_bytes = 0
        for member in members:
            if not re.fullmatch(r"result/(?:manifest|[a-z0-9-]+-r[123])\.json", member.name):
                continue
            relative_name = member.name.removeprefix("result/")
            maximum_bytes = 1 << 20 if relative_name == "manifest.json" else 16 << 20
            if not member.isfile() or member.size <= 0 or member.size > maximum_bytes:
                raise ValueError(f"formal archive member is outside bounds: {relative_name}")
            total_json_bytes += member.size
            if total_json_bytes > 64 << 20:
                raise ValueError("formal archive JSON members exceed the report size bound")
            source = archive.extractfile(member)
            if source is None:
                raise ValueError(f"formal archive member cannot be read: {relative_name}")
            value = source.read(maximum_bytes + 1)
            if len(value) != member.size:
                raise ValueError(f"formal archive member length drift: {relative_name}")
            archive_json[relative_name] = value

    if archive_json.get("manifest.json") != (result / "manifest.json").read_bytes():
        raise ValueError("extracted formal manifest does not match archive")
    manifest = load_unique_json(result / "manifest.json", maximum_bytes=1 << 20)
    manifest_identity = {
        "host_revision": EXPECTED_HOST_REVISION,
        "artifact_sha256": EXPECTED_ARTIFACT_SHA256,
        "artifact_manifest_sha256": EXPECTED_ARTIFACT_MANIFEST_SHA256,
        "artifact_source_revision": EXPECTED_ARTIFACT_SOURCE,
        "binary_sha256": EXPECTED_BINARY_SHA256,
        "memory_budget_bytes": 8 << 30,
        "memory_reserve_bytes": 2 << 30,
        "max_cpu": 4,
        "greed": 50,
    }
    for key, value in manifest_identity.items():
        if manifest.get(key) != value:
            raise ValueError(f"formal manifest identity drift: {key}")
    entries = manifest.get("records")
    if not isinstance(entries, list) or len(entries) != 24:
        raise ValueError("formal manifest record count drift")
    records: list[dict[str, Any]] = []
    expected_files: set[str] = set()
    for entry in entries:
        if entry["exit_code"] != 0:
            raise ValueError("formal record has nonzero process exit")
        name = entry["evidence"]
        if not re.fullmatch(r"[a-z0-9-]+-r[123]\.json", name) or name in expected_files:
            raise ValueError("formal evidence filename is invalid or duplicated")
        expected_files.add(name)
        path = result / name
        if archive_json.get(name) != path.read_bytes():
            raise ValueError(f"extracted formal evidence does not match archive: {name}")
        if sha256_file(path) != entry["evidence_sha256"]:
            raise ValueError(f"formal evidence checksum drift: {name}")
        evidence = load_unique_json(path, maximum_bytes=16 << 20)
        records.append(_record_from_evidence(entry["cell"], evidence))
    archived_json = set(archive_json) - {"manifest.json"}
    actual_json = {path.name for path in result.glob("*.json")} - {"manifest.json"}
    if archived_json != expected_files or actual_json != expected_files:
        raise ValueError("formal evidence JSON file set drift")
    return records


def _fmt(value: float) -> str:
    return f"{value:.3f}".rstrip("0").rstrip(".")


def _svg_chart(title: str, subtitle: str, categories: list[str], panels: list[tuple[str, str, list[dict[str, Any]], str]]) -> str:
    width, height = 1000, 580
    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}" role="img" aria-labelledby="title desc">',
        f'<title id="title">{html.escape(title)}</title><desc id="desc">{html.escape(subtitle)}</desc>',
        '<rect width="1000" height="580" fill="#ffffff"/>',
        '<style>text{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;fill:#17202a}.title{font-size:25px;font-weight:700}.sub{font-size:14px;fill:#566573}.axis{stroke:#9aa5b1;stroke-width:1}.grid{stroke:#e8ecef;stroke-width:1}.label{font-size:13px}.tick{font-size:12px;fill:#566573}.legend{font-size:12px}.range{stroke-width:3}.point{stroke:#fff;stroke-width:2}</style>',
        f'<text class="title" x="50" y="42">{html.escape(title)}</text>',
        f'<text class="sub" x="50" y="67">{html.escape(subtitle)}</text>',
    ]
    colors = ["#0072B2", "#D55E00", "#009E73"]
    for panel_index, (panel_title, unit, series, note) in enumerate(panels):
        x0 = 60 + panel_index * 490
        y0, chart_w, chart_h = 115, 410, 350
        maximum = max(item["max"] for line in series for item in line["values"])
        maximum = maximum * 1.12 if maximum > 0 else 1.0
        parts.append(f'<text class="label" x="{x0}" y="100" font-weight="700">{html.escape(panel_title)} ({html.escape(unit)})</text>')
        for tick in range(5):
            value = maximum * tick / 4
            y = y0 + chart_h - chart_h * tick / 4
            parts.append(f'<line class="grid" x1="{x0}" y1="{_fmt(y)}" x2="{x0+chart_w}" y2="{_fmt(y)}"/>')
            parts.append(f'<text class="tick" x="{x0-8}" y="{_fmt(y+4)}" text-anchor="end">{_fmt(value)}</text>')
        parts.append(f'<line class="axis" x1="{x0}" y1="{y0+chart_h}" x2="{x0+chart_w}" y2="{y0+chart_h}"/>')
        for category_index, category in enumerate(categories):
            x = x0 + chart_w * (category_index + 0.5) / len(categories)
            parts.append(f'<text class="tick" x="{_fmt(x)}" y="490" text-anchor="middle">{html.escape(category)}</text>')
            for series_index, line in enumerate(series):
                item = line["values"][category_index]
                offset = (series_index - (len(series)-1)/2) * 14
                px = x + offset
                low_y = y0 + chart_h * (1 - item["min"] / maximum)
                high_y = y0 + chart_h * (1 - item["max"] / maximum)
                median_y = y0 + chart_h * (1 - item["median"] / maximum)
                color = colors[series_index]
                parts.append(f'<line class="range" stroke="{color}" x1="{_fmt(px)}" y1="{_fmt(low_y)}" x2="{_fmt(px)}" y2="{_fmt(high_y)}"/>')
                parts.append(f'<circle class="point" fill="{color}" cx="{_fmt(px)}" cy="{_fmt(median_y)}" r="6"/>')
        for series_index, line in enumerate(series):
            lx = x0 + series_index * 125
            parts.append(f'<circle fill="{colors[series_index]}" cx="{lx+7}" cy="525" r="5"/><text class="legend" x="{lx+18}" y="529">{html.escape(line["name"])}</text>')
        parts.append(f'<text class="tick" x="{x0}" y="557">{html.escape(note)}</text>')
    parts.append("</svg>\n")
    return "".join(parts)


def render_assets(summary: dict[str, Any], output_dir: Path) -> None:
    output_dir.mkdir(parents=True, exist_ok=True)
    unexpected = {path.name for path in output_dir.iterdir()} - set(OUTPUT_FILES)
    if unexpected:
        raise ValueError(f"output directory contains undeclared files: {sorted(unexpected)}")
    (output_dir / "phase6-summary.json").write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    by_id = {point["cell_id"]: point for point in summary["points"]}

    def values(cells: list[str], metric: str) -> list[dict[str, Any]]:
        result = [by_id[cell][metric] for cell in cells]
        if any(item is None for item in result):
            raise ValueError(f"figure metric {metric} is unavailable")
        return result

    closed = ["closed-numpy-v1-s64-c1", "closed-numpy-v1-s256-c4", "closed-numpy-v1-s256-c8"]
    closed_svg = _svg_chart(
        "Closed-loop NumPy knee", "Median with full min–max across n=3 exact-source repetitions.",
        ["64 slots / c1", "256 / c4", "256 / c8"],
        [
            ("Completed throughput", "requests/s", [{"name": "throughput", "values": values(closed, "throughput_per_second")}], "c8 adds latency without throughput gain over c4."),
            ("Request latency", "ms", [{"name": "p50", "values": values(closed, "latency_p50_ms")}, {"name": "p95", "values": values(closed, "latency_p95_ms")}], "Latency includes single-use refill and drain accounting."),
        ],
    )
    (output_dir / "closed-loop-knee.svg").write_text(closed_svg, encoding="utf-8")
    opened = ["open-numpy-v1-s256-w8-r25", "open-numpy-v1-s256-w8-r100"]
    open_svg = _svg_chart(
        "Fixed open-loop NumPy load", "All deterministic tape arrivals were accepted, completed and numerically validated.",
        ["25 offered/s", "100 offered/s"],
        [
            ("Completed throughput", "requests/s", [{"name": "throughput", "values": values(opened, "throughput_per_second")}], "Throughput denominator includes completion/drain time."),
            ("Request latency", "ms", [{"name": "p50", "values": values(opened, "latency_p50_ms")}, {"name": "p95", "values": values(opened, "latency_p95_ms")}], "No rejected, failed or timed-out accepted requests."),
        ],
    )
    (output_dir / "open-loop-load.svg").write_text(open_svg, encoding="utf-8")
    mixed = ["open-numpy-mixed-v1-s256-w8-r10", "open-numpy-mixed-v1-s256-w8-r40", "closed-numpy-mixed-v1-s256-c8"]
    mixed_svg = _svg_chart(
        "Controlled mixed NumPy load", "Mixed cycle includes tiny, CPU, 4 MiB/500 ms and 16 MiB/2 s request classes.",
        ["open 10/s", "open 40/s", "closed c8"],
        [
            ("Completed throughput", "requests/s", [{"name": "throughput", "values": values(mixed, "throughput_per_second")}], "Open and closed modes are distinct demand mechanisms."),
            ("Active process memory", "MiB", [{"name": "private dirty", "values": values(mixed, "max_active_private_dirty_mib")}, {"name": "PSS", "values": values(mixed, "max_active_pss_mib")}], "Max of three fixed-offset active snapshots per repetition."),
        ],
    )
    (output_dir / "mixed-load.svg").write_text(mixed_svg, encoding="utf-8")
    lines = []
    for name in OUTPUT_FILES[:-1]:
        lines.append(f"{sha256_file(output_dir / name)}  {name}")
    (output_dir / "SHA256SUMS").write_text("\n".join(lines) + "\n", encoding="ascii")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--formal-root", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    summary = build_summary(load_formal_records(args.formal_root.resolve()))
    render_assets(summary, args.output_dir.resolve())
    print(json.dumps({"rendered": list(OUTPUT_FILES), "formal_records": summary["formal_records"]}, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
