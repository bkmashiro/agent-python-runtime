import runpy
import unittest
from pathlib import Path
from types import SimpleNamespace

path = Path(__file__).resolve().parents[1] / 'scripts/analyze-residency.py'
analysis = SimpleNamespace(**runpy.run_path(str(path)))


class ResidencyAnalysisTests(unittest.TestCase):
    def test_irregular_samples_are_time_weighted(self):
        self.assertAlmostEqual(250/3, analysis.weighted_mean([0, 1, 3], [0, 100, 100]))

    def test_unknown_sample_is_not_zero(self):
        self.assertIsNone(analysis.weighted_mean([0, 1], [None, 100]))
        self.assertIsNone(analysis.weighted_mean([0], [100]))

    def test_nonmonotonic_samples_rejected(self):
        with self.assertRaises(ValueError):
            analysis.weighted_mean([2, 1], [10, 20])

    def test_outermost_smallest_budget_is_used(self):
        sample = {'cgroup_ancestors': [
            {'path': '/job/task', 'memory_max_bytes': None},
            {'path': '/job/step', 'memory_high_bytes': 100, 'memory_max_bytes': 200},
            {'path': '/job', 'memory_max_bytes': 100},
            {'path': '/', 'memory_max_bytes': None},
        ]}
        self.assertEqual('/job', analysis.budget_path(sample))

    def test_pairs_exclude_failed_trials_and_keep_denominator(self):
        records = [{'record_type': 'metadata', 'blocks': 2}]
        for block, base in [(0, 100), (1, 200)]:
            for arm, time in [('natural', base), ('fixed', base/2), ('pressure', base)]:
                records.append({'record_type': 'trial', 'block': block, 'arm': arm,
                                'outcome': 'worker_crash' if (block, arm) == (1, 'fixed') else 'success',
                                'run_ns': time})
        result = analysis.summarize(records)
        pair = next(r for r in result['paired'] if r['arm'] == 'fixed' and r['metric'] == 'run_ns')
        self.assertEqual(1, pair['paired_blocks'])
        self.assertEqual(-50, pair['median_percent_delta'])
        self.assertEqual({'success': 1, 'worker_crash': 1}, result['outcomes']['fixed'])
        row = next(r for r in result['summary'] if r['arm'] == 'fixed' and r['metric'] == 'run_ns')
        self.assertEqual((2, 1, 1), (row['scheduled'], row['successful'], row['available']))

    def test_missing_and_duplicate_rows(self):
        header = {'record_type': 'metadata', 'blocks': 1}
        row = {'record_type': 'trial', 'block': 0, 'arm': 'natural', 'outcome': 'timeout'}
        result = analysis.summarize([header, row])
        self.assertEqual(2, len(result['missing']))
        with self.assertRaises(ValueError):
            analysis.summarize([header, row, row])


if __name__ == '__main__':
    unittest.main()
