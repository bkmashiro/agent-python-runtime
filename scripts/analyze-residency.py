#!/usr/bin/env python3
"""Summarize residency trials without treating missing data as zero."""
import argparse
import csv
import json
import statistics
from collections import Counter
from pathlib import Path

ARMS = ('natural', 'fixed', 'pressure')
TIMINGS = ('setup_ns', 'run_ns', 'wake_to_result_ns', 'cleanup_ns', 'end_to_end_ns')


def budget_path(sample):
    """Use the outermost cgroup with the smallest finite budget."""
    selected = None
    for group in sample.get('cgroup_ancestors', []):
        limits = [group.get(k) for k in ('memory_high_bytes', 'memory_max_bytes')]
        limits = [n for n in limits if n is not None]
        if limits and (selected is None or min(limits) <= selected[0]):
            selected = (min(limits), group['path'])
    return selected[1] if selected else None


def group_value(sample, path, key):
    return next((g.get(key) for g in sample.get('cgroup_ancestors', []) if g['path'] == path), None)


def weighted_mean(times, values):
    if len(times) < 2 or any(v is None for v in values):
        return None
    if any(b < a for a, b in zip(times, times[1:])):
        raise ValueError('non-monotonic wait samples')
    duration = times[-1] - times[0]
    if duration <= 0:
        return None
    area = sum((b-a)*(x+y)/2 for a, b, x, y in zip(times, times[1:], values, values[1:]))
    return area / duration


def trial_metrics(row):
    metrics = {key: row.get(key) for key in TIMINGS}
    samples = row.get('wait_samples') or []
    times = [s['at_mono_ns'] for s in samples]
    path = budget_path(samples[0]) if samples else None
    series = {
        'pss': [s.get('pss_bytes') for s in samples],
        'rss': [s.get('rss_bytes') for s in samples],
        'swap_pss': [s.get('swap_pss_bytes') for s in samples],
        'cgroup_memory': [group_value(s, path, 'memory_current_bytes') for s in samples],
        'cgroup_swap': [group_value(s, path, 'swap_current_bytes') for s in samples],
    }
    for name, values in series.items():
        metrics[f'wait_mean_{name}_bytes'] = weighted_mean(times, values)
        metrics[f'sampled_wait_peak_{name}_bytes'] = (
            max(values) if values and all(v is not None for v in values) else None
        )
    return metrics


def summarize(records):
    metadata = [r for r in records if r.get('record_type') == 'metadata']
    if len(metadata) != 1:
        raise ValueError('expected one metadata record')
    metadata = metadata[0]
    rows = [r for r in records if r.get('record_type') == 'trial']
    expected = {(b, arm) for b in range(metadata['blocks']) for arm in ARMS}
    indexed = {}
    for row in rows:
        key = (row['block'], row['arm'])
        if key not in expected or key in indexed:
            raise ValueError(f'unexpected or duplicate trial: {key}')
        indexed[key] = row
    missing = sorted(expected - indexed.keys())
    outcomes = {arm: dict(Counter(r['outcome'] for r in rows if r['arm'] == arm)) for arm in ARMS}
    good = {key: trial_metrics(r) for key, r in indexed.items() if r['outcome'] == 'success'}
    metric_names = sorted({name for values in good.values() for name in values})
    summary = []
    pairs = []
    for arm in ARMS:
        for metric in metric_names:
            values = [m[metric] for (b, a), m in good.items() if a == arm and m[metric] is not None]
            summary.append({
                'arm': arm, 'metric': metric, 'scheduled': metadata['blocks'],
                'successful': outcomes[arm].get('success', 0), 'available': len(values),
                'median': statistics.median(values) if values else None,
                'minimum': min(values) if values else None,
                'maximum': max(values) if values else None,
            })
            if arm == 'natural':
                continue
            matched = [(good[(b, 'natural')][metric], good[(b, arm)][metric])
                       for b in range(metadata['blocks'])
                       if (b, 'natural') in good and (b, arm) in good
                       and good[(b, 'natural')][metric] is not None and good[(b, arm)][metric] is not None]
            differences = [t-b for b, t in matched]
            percentages = [100*(t-b)/b for b, t in matched if b != 0]
            pairs.append({
                'arm': arm, 'metric': metric, 'paired_blocks': len(matched),
                'median_delta': statistics.median(differences) if differences else None,
                'percent_blocks': len(percentages),
                'median_percent_delta': statistics.median(percentages) if percentages else None,
            })
    return {'metadata': metadata, 'outcomes': outcomes, 'missing': missing,
            'summary': summary, 'paired': pairs}


def write_csv(path, rows):
    with path.open('w', newline='') as handle:
        if rows:
            writer = csv.DictWriter(handle, fieldnames=list(rows[0]))
            writer.writeheader()
            writer.writerows(rows)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('input', type=Path)
    parser.add_argument('--output-dir', type=Path, required=True)
    args = parser.parse_args()
    records = [json.loads(line) for line in args.input.read_text().splitlines() if line.strip()]
    result = summarize(records)
    args.output_dir.mkdir(parents=True, exist_ok=True)
    write_csv(args.output_dir/'summary.csv', result['summary'])
    write_csv(args.output_dir/'paired.csv', result['paired'])
    (args.output_dir/'summary.json').write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps({'outcomes': result['outcomes'], 'missing': result['missing']}, indent=2))
    return int(bool(result['missing']) or any(r.get('outcome') != 'success'
               for r in records if r.get('record_type') == 'trial'))


if __name__ == '__main__':
    raise SystemExit(main())
