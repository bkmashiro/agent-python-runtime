#!/usr/bin/env python3
"""Render scheduler experiment figures from checksum-verified private evidence.

Run from the repository root with:
  uv run --with matplotlib==3.11.1 --with pandas==2.3.3 --with seaborn==0.13.2 \
    python scripts/plot_scheduler_experiments.py
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import pandas as pd
import seaborn as sns

MIB = 1 << 20
CURRENT_SOURCE = "d6f6702f9462ec58705f05786a9ea58ba2baba1c"
CPU_SOURCE = "d6df17be59de626a53c5c374cbb552d0c8d53ca1"
IO_SOURCE = "23384d236f2e84e5da900d2f50d54a7ba7b96ad5"
NATIVE_SOURCE = "84d6f3711e4c0e042faea955c4422e0de9ec33f5"


def load(path: Path) -> dict:
    with path.open(encoding="utf-8") as handle:
        return json.load(handle)


def save(fig: plt.Figure, output: Path) -> None:
    fig.tight_layout()
    fig.savefig(output, dpi=180, bbox_inches="tight", metadata={"Software": "plot_scheduler_experiments.py"})
    plt.close(fig)


def aggregate(raw: pd.DataFrame, group: str, metrics: list[str]) -> pd.DataFrame:
    rows = []
    for key, frame in raw.groupby(group, sort=True):
        row = {group: key}
        for metric in metrics:
            values = frame[metric].dropna()
            if not values.empty:
                row[f"{metric}_median"] = values.median()
                row[f"{metric}_min"] = values.min()
                row[f"{metric}_max"] = values.max()
        rows.append(row)
    return pd.DataFrame(rows)


def error_line(ax: plt.Axes, data: pd.DataFrame, x: str, metric: str, *, color: str, marker: str = "o", label: str | None = None) -> None:
    center = data[f"{metric}_median"]
    lower = center - data[f"{metric}_min"]
    upper = data[f"{metric}_max"] - center
    ax.errorbar(data[x], center, yerr=[lower, upper], color=color, marker=marker, linewidth=2, capsize=4, label=label)


def plot_refill(evidence: Path, output: Path) -> None:
    payload = load(evidence / "refill-repeat/job-267987/SUMMARY.json")
    assert payload["source_commit"] == CURRENT_SOURCE
    raw = pd.DataFrame(payload["raw"])
    summary = aggregate(raw, "workers", ["rps", "p99_ns", "cpu_cores", "drain_ns"])
    summary["p99_ms_median"] = summary["p99_ns_median"] / 1e6
    summary["p99_ms_min"] = summary["p99_ns_min"] / 1e6
    summary["p99_ms_max"] = summary["p99_ns_max"] / 1e6
    summary["drain_s_median"] = summary["drain_ns_median"] / 1e9
    summary["drain_s_min"] = summary["drain_ns_min"] / 1e9
    summary["drain_s_max"] = summary["drain_ns_max"] / 1e9

    fig, axes = plt.subplots(1, 3, figsize=(14, 4.2))
    error_line(axes[0], summary, "workers", "rps", color="#2474B5")
    error_line(axes[1], summary, "workers", "p99_ms", color="#D45B3E")
    error_line(axes[2], summary, "workers", "drain_s", color="#3A8D5D")
    for ax, ylabel in zip(axes, ["Throughput (req/s)", "p99 latency (ms)", "Post-load refill drain (s)"]):
        ax.set(xlabel="Refill workers", ylabel=ylabel, xticks=[1, 2, 4, 8, 12, 16])
        ax.grid(alpha=0.25)
    axes[0].axvline(8, color="0.4", linestyle="--", linewidth=1, label="balanced = 8")
    axes[0].legend(frameon=False)
    fig.suptitle("COW refill scaling — median and min–max, n=3")
    save(fig, output / "refill-scaling.png")


def plot_concurrency(evidence: Path, output: Path) -> None:
    cpu = load(evidence / "cpu-concurrency/job-267926/SUMMARY.json")
    io = load(evidence / "io-concurrency/job-267934/SUMMARY.json")
    assert cpu["source_commit"] == CPU_SOURCE and io["source_commit"] == IO_SOURCE
    rows = []
    for row in cpu["rows"]:
        rows.append({**row, "profile": "CPU runtime overhead"})
    for row in io["rows"]:
        rows.append({**row, "profile": f"Timer {row['wait_ns'] / 1e6:.0f} ms"})
    frame = pd.DataFrame(rows).sort_values(["profile", "consumers"])
    frame["p99_ms"] = frame["p99_ns"] / 1e6
    frame["rps_per_core"] = frame["rps"] / frame["cpu_cores"]

    fig, axes = plt.subplots(1, 3, figsize=(15, 4.4))
    sns.lineplot(data=frame, x="consumers", y="rps", hue="profile", marker="o", ax=axes[0])
    sns.lineplot(data=frame, x="consumers", y="p99_ms", hue="profile", marker="o", legend=False, ax=axes[1])
    sns.lineplot(data=frame, x="consumers", y="rps_per_core", hue="profile", marker="o", legend=False, ax=axes[2])
    for ax, ylabel in zip(axes, ["Throughput (req/s)", "p99 latency (ms)", "Throughput / observed CPU core"]):
        ax.set_xscale("log", base=2)
        ax.set(xlabel="Closed-loop consumers", ylabel=ylabel)
        ax.grid(alpha=0.25)
    axes[0].legend(title=None, frameon=False)
    fig.suptitle("Profile-specific concurrency knees — single-run exploratory sweeps")
    save(fig, output / "profile-concurrency.png")


def plot_mixed_heavy(evidence: Path, output: Path) -> None:
    payload = load(evidence / "paper-repeat/job-267985/SUMMARY.json")
    assert payload["source_commit"] == CURRENT_SOURCE
    rows = []
    for row in payload["raw"]:
        if row["name"].startswith(("mixed-c", "heavy-c")):
            profile, consumers, _repeat = row["name"].split("-")
            rows.append({**row, "profile": profile.capitalize(), "consumers": int(consumers[1:])})
    raw = pd.DataFrame(rows)
    raw["p50_ms"] = raw["p50_ns"] / 1e6
    raw["p99_ms"] = raw["p99_ns"] / 1e6
    raw["rps_per_core"] = raw["rps"] / raw["cpu_cores"]

    fig, axes = plt.subplots(1, 3, figsize=(14.5, 4.3))
    sns.pointplot(data=raw, x="consumers", y="rps", hue="profile", estimator="median", errorbar=("pi", 100), capsize=0.12, ax=axes[0])
    sns.pointplot(data=raw, x="consumers", y="p50_ms", hue="profile", estimator="median", errorbar=("pi", 100), capsize=0.12, legend=False, ax=axes[1])
    sns.pointplot(data=raw, x="consumers", y="rps_per_core", hue="profile", estimator="median", errorbar=("pi", 100), capsize=0.12, legend=False, ax=axes[2])
    for ax, ylabel in zip(axes, ["Throughput (req/s)", "p50 latency (ms)", "Throughput / observed CPU core"]):
        ax.set(xlabel="Consumers", ylabel=ylabel)
        ax.grid(alpha=0.25)
    axes[0].legend(title=None, frameon=False)
    fig.suptitle("Mixed and heavy-tail concurrency — median and min–max, n=3")
    save(fig, output / "mixed-heavy-concurrency.png")


def plot_dirty(evidence: Path, output: Path) -> None:
    payload = load(evidence / "paper-repeat/job-267985/SUMMARY.json")
    rows = []
    for row in payload["raw"]:
        if row["name"].startswith("dirty"):
            dirty_mib = int(row["name"].split("-")[0].replace("dirty", ""))
            rows.append({**row, "dirty_mib": dirty_mib})
    raw = pd.DataFrame(rows)
    raw["anonymous_mib"] = raw["active_anonymous_peak"] / MIB
    raw["pss_mib"] = raw["active_pss_peak"] / MIB
    raw["p99_ms"] = raw["p99_ns"] / 1e6
    memory = raw.melt(id_vars=["dirty_mib"], value_vars=["anonymous_mib", "pss_mib"], var_name="metric", value_name="mib")
    memory["metric"] = memory["metric"].map({"anonymous_mib": "Active Anonymous", "pss_mib": "Process PSS"})

    fig, axes = plt.subplots(1, 2, figsize=(10.5, 4.3))
    sns.pointplot(data=memory, x="dirty_mib", y="mib", hue="metric", estimator="median", errorbar=("pi", 100), capsize=0.12, ax=axes[0])
    sns.pointplot(data=raw, x="dirty_mib", y="p99_ms", estimator="median", errorbar=("pi", 100), capsize=0.12, color="#D45B3E", ax=axes[1])
    axes[0].set(xlabel="Dirty bytes per request (MiB)", ylabel="Active memory (MiB)")
    axes[1].set(xlabel="Dirty bytes per request (MiB)", ylabel="p99 latency (ms)")
    axes[0].legend(title=None, frameon=False)
    for ax in axes:
        ax.grid(alpha=0.25)
    fig.suptitle("Dirty working-set cost at 16 active consumers — median and min–max, n=3")
    save(fig, output / "dirty-working-set.png")


def plot_burst(evidence: Path, output: Path) -> None:
    payload = load(evidence / "paper-repeat/job-267985/SUMMARY.json")
    rows = []
    for row in payload["raw"]:
        if row["name"].startswith("burst-"):
            factor = int(row["name"].split("-")[1].replace("x", ""))
            rows.append({**row, "factor": factor, "peak_consumers": 16 * factor})
    raw = pd.DataFrame(rows)
    raw["p50_ms"] = raw["p50_ns"] / 1e6
    raw["p99_ms"] = raw["p99_ns"] / 1e6
    windows = raw.melt(id_vars=["factor"], value_vars=["pre_rps", "burst_rps"], var_name="window", value_name="window_rps")
    windows["window"] = windows["window"].map({"pre_rps": "Pre-window (16)", "burst_rps": "Burst window"})

    fig, axes = plt.subplots(1, 3, figsize=(14.5, 4.3))
    sns.pointplot(data=windows, x="factor", y="window_rps", hue="window", estimator="median", errorbar=("pi", 100), capsize=0.12, ax=axes[0])
    latency = raw.melt(id_vars=["factor"], value_vars=["p50_ms", "p99_ms"], var_name="quantile", value_name="latency_ms")
    latency["quantile"] = latency["quantile"].str.replace("_ms", "", regex=False)
    sns.pointplot(data=latency, x="factor", y="latency_ms", hue="quantile", estimator="median", errorbar=("pi", 100), capsize=0.12, ax=axes[1])
    sns.pointplot(data=raw, x="factor", y="max_waiting", estimator="median", errorbar=("pi", 100), capsize=0.12, color="#8E5AA7", ax=axes[2])
    axes[0].set(xlabel="Correlated burst factor", ylabel="Window throughput (req/s)")
    axes[1].set(xlabel="Correlated burst factor", ylabel="Latency (ms)")
    axes[2].set(xlabel="Correlated burst factor", ylabel="Peak waiting consumers")
    axes[0].legend(title=None, frameon=False)
    axes[1].legend(title=None, frameon=False)
    for ax in axes:
        ax.grid(alpha=0.25)
    fig.suptitle("Correlated closed-loop burst knee — median and min–max, n=3")
    save(fig, output / "correlated-burst.png")


def plot_native_baseline(evidence: Path, output: Path) -> None:
    payload = load(evidence / "native-numpy/job-268015/result/SUMMARY.json")
    assert payload["host_revision"] == NATIVE_SOURCE
    rows = []
    for key, label in [("basic", "Basic"), ("numpy_import", "NumPy import")]:
        fixture = payload[key]
        rows.extend(
            [
                {"fixture": label, "path": "Native cold", "latency_ms": fixture["native"]["cold_total"] / 1e6},
                {"fixture": label, "path": "Native warm", "latency_ms": fixture["native"]["warm_total"] / 1e6},
                {"fixture": label, "path": "WASI fresh", "latency_ms": fixture["wasi_fresh"]["total"] / 1e6},
                {"fixture": label, "path": "WASI prepared", "latency_ms": fixture["wasi_prepared"]["total"] / 1e6},
            ]
        )
    frame = pd.DataFrame(rows)
    fig, axes = plt.subplots(1, 2, figsize=(11.5, 4.8), sharey=True)
    order = ["Native cold", "Native warm", "WASI fresh", "WASI prepared"]
    for ax, fixture in zip(axes, ["Basic", "NumPy import"]):
        subset = frame[frame["fixture"] == fixture]
        sns.barplot(data=subset, x="path", y="latency_ms", order=order, ax=ax)
        ax.set_yscale("log")
        ax.set(xlabel="Deployment path", ylabel="Median request total (ms)", title=fixture)
        ax.tick_params(axis="x", rotation=20)
        ax.grid(axis="y", alpha=0.25)
        for container in ax.containers:
            ax.bar_label(container, fmt="%.3g", padding=3, fontsize=8)
    axes[1].text(0.02, 0.96, "NumPy is present but not\nimported before WASI readiness", transform=axes[1].transAxes, ha="left", va="top", fontsize=9, bbox={"facecolor": "white", "edgecolor": "0.75", "alpha": 0.9})
    fig.suptitle("Native CPython and CPython-WASI request boundaries — same Linux node")
    save(fig, output / "native-cpython-baseline.png")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--evidence-root", type=Path, default=Path(".artifacts-private"))
    parser.add_argument("--output-dir", type=Path, default=Path("docs/assets/scheduler-experiments"))
    args = parser.parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    sns.set_theme(context="paper", style="whitegrid", palette="colorblind", font_scale=1.05)
    plot_refill(args.evidence_root, args.output_dir)
    plot_concurrency(args.evidence_root, args.output_dir)
    plot_mixed_heavy(args.evidence_root, args.output_dir)
    plot_dirty(args.evidence_root, args.output_dir)
    plot_burst(args.evidence_root, args.output_dir)
    plot_native_baseline(args.evidence_root, args.output_dir)
    figures = sorted(args.output_dir.glob("*.png"))
    checksums = []
    for path in figures:
        checksums.append(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}")
        print(path)
    (args.output_dir / "SHA256SUMS").write_text("\n".join(checksums) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
