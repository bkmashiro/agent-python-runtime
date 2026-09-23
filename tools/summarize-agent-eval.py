#!/usr/bin/env python3
"""Account for every episode; describe tiny paired samples without ranking claims."""
import argparse
from collections import Counter, defaultdict
from fractions import Fraction
import json
from pathlib import Path
import statistics

EXPECTED = {
    'lookup': {'price_cents': 3199},
    'paginated_sum': {'record_count': 20, 'sum_cents': 46050},
    'join': {'category_totals': {'alpha': 43240, 'beta': 22825, 'gamma': 43290}},
    'transient': {'price_cents': 2075},
}


DEPENDENCIES = {"cursor_sum": {"record_count": 20, "sum_cents": 46050}, "dependent_due": {"kind": "payment_due", "amount_cents": 5825}, "dependent_credit": {"kind": "credit_available", "amount_cents": 4375}}

def summarize(rows, expected_count):
    if len(rows) != expected_count:
        raise ValueError(f'expected {expected_count} rows, got {len(rows)}')
    if any(r.get('replayed') for r in rows):
        raise ValueError('offline replay rows are not live observations')
    identities = [(r['task'], r['arm'], r['repeat']) for r in rows]
    if len(set(identities)) != len(identities):
        raise ValueError('duplicate episode')
    if expected_count in (12, 16):
        cohort = EXPECTED if expected_count == 16 else DEPENDENCIES
        planned = {(task, arm, repeat) for task in cohort for arm in ('direct', 'code') for repeat in (1, 2)}
        if set(identities) != planned:
            raise ValueError('cohort differs from the frozen matrix')
    groups = defaultdict(list)
    for row in rows:
        if row['completion_status'] in ('completed', 'wrong_answer'):
            # Independent grade from the persisted submission, not its success flag.
            answer = json.loads(json.dumps(row['answer']), parse_float=Fraction)
            submissions = [event for event in row.get('trace', []) if event['tool'] == 'submit_answer']
            if not submissions or json.loads(submissions[-1]['arguments'], parse_float=Fraction) != answer:
                raise ValueError('saved answer differs from submitted tool arguments')
            correct = answer == (EXPECTED | DEPENDENCIES)[row['task']]
            if correct != row['correctness'] or correct != (row['completion_status'] == 'completed'):
                raise ValueError(f'oracle disagreement: {row["task"]} {row["arm"]}')
        if row['usage_complete'] and row['model_requests'] and row['provider_usage'] is None:
            raise ValueError('usage marked complete but missing')
        groups[(row['task'], row['arm'])].append(row)
    report = {'episodes': len(rows), 'model_requests': sum(r['model_requests'] for r in rows),
              'statuses': dict(Counter(r['completion_status'] for r in rows)), 'groups': []}
    for (task, arm), samples in sorted(groups.items()):
        usages = [r['provider_usage'] for r in samples]
        attempts = [r for r in samples if r['model_requests']]
        report['groups'].append({
            'task': task, 'arm': arm, 'n': len(samples),
            'correct': sum(r['correctness'] is True for r in samples),
            'statuses': dict(Counter(r['completion_status'] for r in samples)),
            'model_turns': [r['model_requests'] for r in samples],
            'domain_calls': [r['domain_tool_calls'] for r in samples],
            'total_tokens': [u['total_tokens'] if u is not None else None for u in usages],
            'median_tokens_per_attempt': statistics.median(u['total_tokens'] for u in usages) if all(u is not None for u in usages) else None,
            'wall_seconds': [r['total_ns'] / 1e9 for r in samples],
            'median_seconds_per_attempt': statistics.median(r['total_ns'] / 1e9 for r in attempts) if attempts else None,
            'returned_models': sorted({m for r in samples for m in r.get('returned_models', [])}),
        })
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('rows', type=Path)
    parser.add_argument('--expected', type=int, default=16)
    args = parser.parse_args()
    data = [json.loads(line) for line in args.rows.read_text().splitlines()]
    result = summarize(data, args.expected)
    print(json.dumps(result, indent=2))
