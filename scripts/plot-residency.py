#!/usr/bin/env python3
"""Plot the recorded residency summaries (matplotlib is a plotting-only dependency)."""
import argparse
import json
from pathlib import Path

import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--results', type=Path, default=Path('research/residency'))
    args = parser.parse_args()
    summaries = [json.loads((args.results/'summary'/c/'summary.json').read_text())
                 for c in ('roomy', 'bounded')]
    metrics = [
        ('wait_mean_pss_bytes', 'Wait mean process PSS (MiB)', 2**20),
        ('wait_mean_cgroup_memory_bytes', 'Wait mean cgroup memory (MiB)', 2**20),
        ('wake_to_result_ns', 'Wake to result (ms)', 1e6),
    ]
    arms = ['natural', 'fixed', 'pressure']
    colours = ['#687987', '#cf8540', '#347d80']
    plt.rcParams.update({'font.size': 10, 'svg.fonttype': 'none', 'axes.spines.top': False,
                         'axes.spines.right': False})
    fig, axes = plt.subplots(2, 3, figsize=(12, 7))
    for col, (metric, label, scale) in enumerate(metrics):
        upper = max(r['maximum']/scale for d in summaries for r in d['summary']
                    if r['metric'] == metric) * 1.22
        for row, data in enumerate(summaries):
            points = [next(r for r in data['summary'] if r['metric'] == metric and r['arm'] == a)
                      for a in arms]
            assert all(p['available'] == p['successful'] == p['scheduled'] == 5 for p in points)
            medians = [p['median']/scale for p in points]
            errors = [[(p['median']-p['minimum'])/scale for p in points],
                      [(p['maximum']-p['median'])/scale for p in points]]
            ax = axes[row, col]
            ax.bar(range(3), medians, color=colours, width=.58, yerr=errors,
                   capsize=3, error_kw={'elinewidth': 1})
            ax.set_xticks(range(3), ['Natural', 'Fixed', 'Pressure'])
            ax.set_xlabel('Policy')
            ax.set_ylabel(label)
            ax.set_ylim(0, upper)
            ax.set_axisbelow(True)
            ax.grid(axis='y', alpha=.18)
            for i, point in enumerate(points):
                ax.text(i, point['maximum']/scale + upper*.025, f'{medians[i]:.1f}', ha='center')
    fig.suptitle('Wait-time residency and continuation cost', fontsize=17, x=.06, ha='left', y=.995)
    fig.text(.06, .932, 'Roomy: 8 GiB limit, no pressure companion', fontsize=12, weight='bold')
    fig.text(.06, .492, 'Bounded: 1.25 GiB limit, 224 MiB companion', fontsize=12, weight='bold')
    fig.subplots_adjust(left=.075, right=.985, top=.895, bottom=.115, hspace=.7, wspace=.34)
    fig.text(.06, .025, 'Median and observed min–max; 5 independent trials per policy per condition.\n'
             'Memory: time-weighted mean during the Host wait. Wake-to-result includes continuation work and Run cleanup.', fontsize=9)
    fig.savefig(args.results/'residency.svg')
    fig.savefig(args.results/'residency.png', dpi=150)
    plt.close(fig)


if __name__ == '__main__':
    main()
