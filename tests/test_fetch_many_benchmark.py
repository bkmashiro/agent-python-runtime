import importlib.util
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "benchmark_fetch_many.py"
spec = importlib.util.spec_from_file_location("benchmark_fetch_many", MODULE_PATH)
benchmark_fetch_many = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmark_fetch_many)


class FetchManyBenchmarkParserTests(unittest.TestCase):
    def test_parses_canonical_samples_and_medians(self):
        output = """
goos: linux
goarch: amd64
cpu: fixture cpu
BenchmarkFetchManySequential/operations=1-2  10  300 ns/op  1.000 operations/batch  2000000 provider-delay-ns/operation
BenchmarkFetchManySequential/operations=1-2  10  100 ns/op  1.000 operations/batch  2000000 provider-delay-ns/operation
BenchmarkFetchManySequential/operations=1-2  10  200 ns/op  1.000 operations/batch  2000000 provider-delay-ns/operation
BenchmarkFetchManySequential/operations=5-2  10  900 ns/op  5.000 operations/batch  2000000 provider-delay-ns/operation
BenchmarkFetchManySequential/operations=5-2  10  1100 ns/op  5.000 operations/batch  2000000 provider-delay-ns/operation
BenchmarkFetchManySequential/operations=5-2  10  1000 ns/op  5.000 operations/batch  2000000 provider-delay-ns/operation
BenchmarkFetchManySequential/operations=20-2 10  4100 ns/op 20.00 operations/batch 2000000 provider-delay-ns/operation
BenchmarkFetchManySequential/operations=20-2 10  3900 ns/op 20.00 operations/batch 2000000 provider-delay-ns/operation
BenchmarkFetchManySequential/operations=20-2 10  4000 ns/op 20.00 operations/batch 2000000 provider-delay-ns/operation
"""
        evidence = benchmark_fetch_many.parse_benchmark_output(output, "deadbeef")
        self.assertEqual(1, evidence["schema_version"])
        self.assertEqual("sequential", evidence["mode"])
        self.assertEqual("synthetic-provider-delay", evidence["evidence_class"])
        self.assertEqual("deadbeef", evidence["source_commit"])
        self.assertEqual([1, 5, 20], [row["operations"] for row in evidence["results"]])
        self.assertEqual(200, evidence["results"][0]["median_ns_per_batch"])
        self.assertEqual(200, evidence["results"][1]["median_ns_per_operation"])
        self.assertEqual(2_000_000, evidence["fixture"]["provider_delay_ns_per_operation"])

    def test_parses_sequential_parallel_comparison(self):
        lines = ["goos: linux", "goarch: amd64", "cpu: fixture cpu"]
        for operations, sequential, parallel, concurrency in [
            (1, [300, 100, 200], [100, 120, 110], 1),
            (5, [1000, 900, 1100], [220, 200, 210], 5),
            (20, [4000, 3900, 4100], [700, 600, 650], 8),
        ]:
            for value in sequential:
                lines.append(
                    "BenchmarkFetchManySequential/operations=%d-2 10 %d ns/op %.3f operations/batch 2000000 provider-delay-ns/operation"
                    % (operations, value, operations)
                )
            for value in parallel:
                lines.append(
                    "BenchmarkFetchManyParallel/operations=%d-2 10 %d ns/op %.3f max-concurrency %.3f operations/batch 2000000 provider-delay-ns/operation"
                    % (operations, value, concurrency, operations)
                )
        evidence = benchmark_fetch_many.parse_comparison_output("\n".join(lines), "cafebabe")
        self.assertEqual("sequential-vs-bounded-parallel", evidence["mode"])
        self.assertEqual([1, 5, 20], [row["operations"] for row in evidence["results"]])
        self.assertEqual(1000, evidence["results"][1]["sequential_median_ns_per_batch"])
        self.assertEqual(210, evidence["results"][1]["parallel_median_ns_per_batch"])
        self.assertEqual(5, evidence["results"][1]["max_concurrency"])
        self.assertAlmostEqual(1000 / 210, evidence["results"][1]["speedup"])

    def test_rejects_missing_or_drifted_fixture_rows(self):
        incomplete = """
goos: linux
goarch: amd64
cpu: fixture
BenchmarkFetchManySequential/operations=1-2 10 100 ns/op 1 operations/batch 2000000 provider-delay-ns/operation
"""
        with self.assertRaisesRegex(ValueError, "exactly three samples"):
            benchmark_fetch_many.parse_benchmark_output(incomplete, "deadbeef")


if __name__ == "__main__":
    unittest.main()
