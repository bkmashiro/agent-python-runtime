import hashlib
import json
import re
import tempfile
import unittest
from pathlib import Path

import tools.phase6_report as report


class Phase6ReportTests(unittest.TestCase):
    def synthetic_records(self):
        records = []
        for cell in report.EXPECTED_FORMAL_CELLS:
            for repetition, delta in enumerate((-1.0, 0.0, 1.0), 1):
                records.append({
                    "cell_id": cell,
                    "repetition": repetition,
                    "arrival_mode": "closed-loop",
                    "arrival_rate_per_second": 0,
                    "slots": 256,
                    "consumers": 4,
                    "workload": "numpy-v1",
                    "throughput_per_second": 50.0 + delta,
                    "latency_p50_ms": 10.0 + delta,
                    "latency_p95_ms": 20.0 + delta,
                    "latency_p99_ms": 30.0 + delta,
                    "latency_mean_ms": 12.0 + delta,
                    "replenish_drain_ms": 100.0 + delta,
                    "cpu_core_utilization": 2.0 + delta / 10,
                    "offered_requests": 100,
                    "accepted_requests": 100,
                    "rejected_requests": 0,
                    "completed_requests": 100,
                    "failed_requests": 0,
                    "timed_out_requests": 0,
                    "validated_results": 100,
                    "ready_after": 256,
                    "replenish_status": "complete",
                    "max_active_private_dirty_mib": 64.0 + delta,
                    "max_active_pss_mib": 70.0 + delta,
                    "active_sample_count": 3,
                    "oom_events": 0,
                    "oom_kill_events": 0,
                    "psi_some_total_us": 0,
                    "pool_failures": 0,
                    "ready_alias_virtual_gib": 32.0,
                    "prepared_allocated_mib": 63.3,
                    "final_process_pss_mib": 420.0 + delta,
                    "cgroup_memory_peak_mib": 1024.0,
                })
        return records

    def test_summary_requires_exact_three_repeats_and_recomputes_dispersion(self):
        summary = report.build_summary(self.synthetic_records())
        self.assertEqual(summary["formal_records"], 24)
        point = summary["points"][0]
        self.assertEqual(point["throughput_per_second"], {
            "raw": [49.0, 50.0, 51.0], "median": 50.0, "min": 49.0, "max": 51.0,
        })
        with self.assertRaisesRegex(ValueError, "formal matrix identity"):
            report.build_summary(self.synthetic_records()[:-1])

    def test_render_is_deterministic_and_has_exact_output_set(self):
        summary = {
            "schema_version": 1,
            "host_revision": report.EXPECTED_HOST_REVISION,
            "formal_records": 24,
            "points": report.build_summary(self.synthetic_records())["points"],
        }
        with tempfile.TemporaryDirectory() as first, tempfile.TemporaryDirectory() as second:
            report.render_assets(summary, Path(first))
            report.render_assets(summary, Path(second))
            expected = set(report.OUTPUT_FILES)
            self.assertEqual({p.name for p in Path(first).iterdir()}, expected)
            self.assertEqual({p.name for p in Path(second).iterdir()}, expected)
            for name in report.OUTPUT_FILES:
                self.assertEqual((Path(first) / name).read_bytes(), (Path(second) / name).read_bytes())
            sums = (Path(first) / "SHA256SUMS").read_text(encoding="ascii").splitlines()
            self.assertEqual(len(sums), len(expected) - 1)
            for line in sums:
                digest, name = line.split("  ")
                self.assertEqual(digest, hashlib.sha256((Path(first) / name).read_bytes()).hexdigest())

    def test_checked_in_assets_and_report_links_are_bound(self):
        repo = Path(__file__).resolve().parents[1]
        assets = repo / "docs/reports/phase6-numpy-density-assets"
        self.assertEqual({path.name for path in assets.iterdir()}, set(report.OUTPUT_FILES))
        manifest = (assets / "SHA256SUMS").read_text(encoding="ascii").splitlines()
        self.assertEqual(len(manifest), 4)
        for line, name in zip(manifest, report.OUTPUT_FILES[:-1], strict=True):
            digest, listed_name = line.split("  ", 1)
            self.assertEqual(listed_name, name)
            self.assertEqual(digest, hashlib.sha256((assets / name).read_bytes()).hexdigest())
        summary = report.load_unique_json(assets / "phase6-summary.json", maximum_bytes=1 << 20)
        self.assertEqual(summary["host_revision"], report.EXPECTED_HOST_REVISION)
        self.assertEqual(summary["formal_archive_sha256"], report.EXPECTED_ARCHIVE_SHA256)
        report_path = repo / "docs/reports/phase6-numpy-density-results.md"
        body = report_path.read_text(encoding="utf-8")
        for target in re.findall(r"!?\[[^]]*\]\(([^)#]+)", body):
            self.assertTrue((report_path.parent / target).resolve().is_file(), target)

    def test_unique_json_rejects_duplicate_keys(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "duplicate.json"
            path.write_text('{"a":1,"a":2}', encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "duplicate JSON key"):
                report.load_unique_json(path, maximum_bytes=1024)


if __name__ == "__main__":
    unittest.main()
