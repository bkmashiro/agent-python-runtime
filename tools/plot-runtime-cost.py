#!/usr/bin/env python3
"""Render the README comparison from the archived three-repeat measurements.

uv run --with matplotlib==3.10.8 python tools/plot-runtime-cost.py
Use --summary PATH to refresh the small figure-data snapshot from the full report.
"""
import argparse
import json
from pathlib import Path
from statistics import median

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Patch
from matplotlib.ticker import StrMethodFormatter

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "docs/figures/runtime-cost"
EVIDENCE = "https://github.com/bkmashiro/pysolate-spine/tree/1908514a55f524289146bad5c9b3e7a24ef5c688/experiments/runtime-cost/compare/evidence/linux-arm64-20261012"
ARMS = ("fork-freeze", "cow")
COLORS = {"fork-freeze": "#718096", "cow": "#087F8C"}
MEMORY = (("idle", "Idle"), ("json", "JSON"), ("agent-stream", "Stream"))
PERFORMANCE = (("short", "Short Python"), ("compute", "Compute"), ("io", "I/O"), ("jsonio", "JSON + I/O"), ("mixed", "Mixed"))


def extract(summary):
    assert summary["complete"] and not summary["errors"]
    assert summary["accounting"] == dict(planned=288, accepted=288, failed=0, missing=0, unexpected=0)
    data = {"source": EVIDENCE, "source_commit": "039421dc8484900856894b9717d1a6c2401d44b4", "environment": "Linux arm64; 2 vCPU; ~2 GiB; no swap activity", "memory": {}, "throughput": {}}
    for work, _ in MEMORY:
        data["memory"][work] = {}
        for arm in ARMS:
            group = next(g for g in summary["memory"] if g["work"] == work and g["arm"] == arm)
            samples = sorted(group["samples"], key=lambda r: r["rep"])
            assert [r["rep"] for r in samples] == [0, 1, 2]
            data["memory"][work][arm] = [r["pss"]["slope"] for r in samples]
    for work, _ in PERFORMANCE:
        data["throughput"][work] = {}
        for arm in ARMS:
            samples = sorted((r for r in summary["performance"] if r["phase"] == "closed" and r["concurrency"] == 32 and r["work"] == work and r["arm"] == arm), key=lambda r: r["rep"])
            assert [r["rep"] for r in samples] == [0, 1, 2]
            data["throughput"][work][arm] = [r["rps"] for r in samples]
    return data


def panel(ax, data, categories, limit, ticks, xlabel):
    for i, (work, _) in enumerate(categories):
        for j, arm in enumerate(ARMS):
            values = data[work][arm]
            assert len(values) == 3 and all(v > 0 for v in values)
            value = median(values)
            y = i + (j - .5) * .32
            ax.barh(y, value, height=.25, color=COLORS[arm], edgecolor="white", linewidth=.5,
                    hatch="///" if arm == "fork-freeze" else None, zorder=3)
            ax.errorbar(value, y, xerr=[[value-min(values)], [max(values)-value]],
                        fmt="none", ecolor="#283645", elinewidth=1.05, capsize=3, zorder=4)
            ax.text(max(values) + limit * .023, y, f"{value:,.0f}", va="center", fontsize=11.5, color="#273849")
    ax.set(yticks=range(len(categories)), yticklabels=[label for _, label in categories],
           xlim=(0, limit), xticks=ticks, ylim=(len(categories)-.45, -.65))
    ax.set_xlabel(xlabel, labelpad=11, fontsize=12)
    ax.set_ylabel("Workload", fontsize=11, labelpad=12, color="#536276")
    ax.xaxis.set_major_formatter(StrMethodFormatter("{x:,.0f}"))
    ax.tick_params(axis="both", length=0, pad=9, labelsize=11.5)
    ax.grid(axis="x", color="#E2E7EC", linewidth=.7, zorder=0)
    for side in ("top", "right", "left"):
        ax.spines[side].set_visible(False)
    ax.spines["bottom"].set_color("#C4CDD7")


def render(data):
    plt.rcParams.update({"font.family": "DejaVu Sans", "text.color": "#172B3A", "axes.labelcolor": "#34475A", "xtick.color": "#536276", "ytick.color": "#34475A", "svg.hashsalt": "pysolate-runtime-cost-v1", "hatch.linewidth": .45})
    fig, axes = plt.subplots(1, 2, figsize=(12, 6.1), gridspec_kw={"width_ratios": [1, 1.16]})
    fig.subplots_adjust(left=.115, right=.975, bottom=.25, top=.65, wspace=.48)
    fig.text(.055, .944, "Memory density & execution throughput", fontsize=21, fontweight="bold")
    fig.text(.055, .895, "Linux arm64  /  2 vCPUs  /  ~2 GiB RAM  /  no swap activity", fontsize=11.5, color="#536276")
    fig.legend(handles=[Patch(facecolor=COLORS["fork-freeze"], edgecolor="white", hatch="///", label="Native CPython · prewarmed fork"), Patch(facecolor=COLORS["cow"], label="Pysolate · experimental Prepared/COW")],
               loc="upper left", bbox_to_anchor=(.045, .856), frameon=False, ncol=2, fontsize=11.5,
               handlelength=1.6, handleheight=1.1, columnspacing=2.7)
    panel(axes[0], data["memory"], MEMORY, 4750, [0, 1000, 2000, 3000, 4000], "Marginal resident PSS (KiB / instance)")
    panel(axes[1], data["throughput"], PERFORMANCE, 6550, [0, 2000, 4000, 6000], "Completed requests / second")
    for ax, title, subtitle in zip(axes, ["a   Memory growth", "b   Throughput"], ["OLS slope at N = 10, 50, 100 · lower is better", "32 concurrent executions · higher is better"]):
        ax.text(0, 1.19, title, transform=ax.transAxes, fontsize=14, fontweight="bold")
        ax.text(0, 1.10, subtitle, transform=ax.transAxes, fontsize=10.5, color="#536276")
    fig.text(.055, .095, "Bars: median of 3 independent runs   ·   Whiskers: observed min–max", fontsize=10.5, color="#536276")
    fig.text(.055, .055, "Memory excludes fixed costs and page tables; throughput excludes setup and warm-up.", fontsize=10.5, color="#536276")
    fig.savefig(OUT / "overview.svg", facecolor="white", metadata={"Date": None, "Title": "Native CPython and experimental Pysolate: memory growth and throughput", "Description": "Three independent repetitions; medians and observed ranges. Source: " + EVIDENCE})
    # Matplotlib emits trailing spaces inside multiline SVG path attributes.
    svg = OUT / "overview.svg"
    svg.write_text("\n".join(line.rstrip() for line in svg.read_text().splitlines()) + "\n")
    fig.savefig(OUT / "overview.png", dpi=180, facecolor="white", metadata={"Software": "matplotlib"})
    plt.close(fig)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", type=Path)
    args = parser.parse_args()
    OUT.mkdir(parents=True, exist_ok=True)
    data_path = OUT / "data.json"
    if args.summary:
        data = extract(json.loads(args.summary.read_text()))
        data_path.write_text(json.dumps(data, indent=2) + "\n")
    else:
        data = json.loads(data_path.read_text())
    render(data)
    print(OUT / "overview.svg")
    print(OUT / "overview.png")


if __name__ == "__main__":
    main()
