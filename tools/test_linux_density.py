import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("density_summary", Path(__file__).with_name("summarize-linux-density.py"))
assert spec is not None and spec.loader is not None
summary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(summary)


def row(external=False):
    return {"repeat": 0, "resident": 8, "delay": "50ms", "external_io": external,
            "exit_code": 0, "measurement": {
                "tasks": 32, "completed": 32, "errors": 0, "result_count": 32,
                "tool_dispatches": 32, "batch_ns": 500_000_000 if external else 1_000_000_000,
                "request_ns": [i * 1_000_000 for i in range(1, 33)],
                "sampled_peak_pss_kib": 1024, "sampled_peak_rss_kib": 2048,
                "sampled_peak_private_dirty_kib": 512, "peak_executor_resident": 8}}


class SummaryTests(unittest.TestCase):
    def test_units_percentile_and_paired_ratio(self):
        cell = summary.summarize([row(), row(True)], 1)[0]
        self.assertEqual(cell["inline"]["throughput_tasks_s"]["median"], 32)
        self.assertEqual(cell["external_io"]["throughput_tasks_s"]["median"], 64)
        self.assertEqual(cell["inline"]["request_p95_ms"]["median"], 31)
        self.assertEqual(cell["inline"]["sampled_peak_pss_mib"]["median"], 1)
        self.assertEqual(cell["paired_throughput_ratio"]["median"], 2)

    def test_missing_duplicate_and_failed_runs_are_rejected(self):
        for rows in ([row()], [row(), row(), row(True)], [{**row(), "exit_code": 1}, row(True)]):
            with self.assertRaises(ValueError):
                summary.summarize(rows, 1)

    def test_accounting_errors_are_rejected(self):
        bad = row()
        bad["measurement"]["errors"] = 1
        with self.assertRaises(ValueError):
            summary.summarize([bad, row(True)], 1)


if __name__ == "__main__":
    unittest.main()
