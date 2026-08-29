import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "project-cow-fanout-economics.py"
GATE = ROOT / "scripts" / "cow-fanout-economics-gate.sh"
RUNTIME_FIXTURE = ROOT / "runtime" / "engine" / "wazero" / "prepared_family_economics_test.go"


COMMIT = "a" * 40
TREE = "b" * 40
ARTIFACT_SHA = "sha256:" + "c" * 64
INPUT_SHA = "sha256:" + "d" * 64
HOST_ID = "gpu31"
EXPECTED_RESULT = 1_048_577


def load_module():
    spec = importlib.util.spec_from_file_location("project_cow_fanout_economics", SCRIPT)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def sample(mode, iteration, fanout, base):
    return {
        "mode": mode,
        "iteration": iteration,
        "fanout": fanout,
        "family_prepare_nanos": base,
        "runner_create_nanos": [base + 1] * fanout,
        "run_nanos": [base + 2] * fanout,
        "runner_close_nanos": [base + 3] * fanout,
        "family_close_nanos": base + 4,
        "baseline_resources": {"pss_bytes": 10, "private_dirty_bytes": 9},
        "after_create_resources": {"pss_bytes": 20, "private_dirty_bytes": 19},
        "after_run_resources": {"pss_bytes": 30, "private_dirty_bytes": 29},
        "pss_create_delta_bytes": 10,
        "pss_run_delta_bytes": 20,
        "private_dirty_create_delta_bytes": 10,
        "private_dirty_run_delta_bytes": 20,
        "result": EXPECTED_RESULT,
    }


def treatment(mode, fanout, runs):
    return {
        "mode": mode,
        "family_prepare_median_nanos": 1,
        "runner_create_median_nanos": 2,
        "run_median_nanos": 3,
        "pss_create_delta_median_bytes": 4,
        "pss_run_delta_median_bytes": 5,
        "private_dirty_create_delta_median_bytes": 6,
        "private_dirty_run_delta_median_bytes": 7,
        "samples": [sample(mode, iteration, fanout, 10 + iteration) for iteration in range(runs)],
    }


def evidence(fanout, runs=3):
    return {
        "schema_version": "pysolate.prepared-family-economics.v1",
        "source_commit": COMMIT,
        "source_tree": TREE,
        "artifact_sha256": ARTIFACT_SHA,
        "input_sha256": INPUT_SHA,
        "input_bytes": 8 << 20,
        "input_element_value": 1,
        "expected_result": EXPECTED_RESULT,
        "runs_per_arm": runs,
        "fanout": fanout,
        "isolation": "one fresh test subprocess per treatment and repetition; treatment order alternates",
        "process_memory_source": "/proc/self/smaps_rollup",
        "treatments": [
            treatment("private_copy", fanout, runs),
            treatment("private_cow", fanout, runs),
        ],
    }


def write_inputs(directory, fanouts, runs=3):
    paths = []
    for fanout in fanouts:
        path = directory / f"{fanout}.json"
        path.write_text(json.dumps(evidence(fanout, runs)) + "\n")
        paths.append(path)
    return paths


class CowFanoutEconomicsTests(unittest.TestCase):
    def call_project(self, module, paths, fanouts=(1, 2, 4, 8), runs=3):
        return module.project(
            paths,
            fanouts=list(fanouts),
            runs=runs,
            host_id=HOST_ID,
            source_commit=COMMIT,
            source_tree=TREE,
            artifact_sha256=ARTIFACT_SHA,
        )

    def test_project_preserves_raw_cells_and_pairs_full_lifecycle_delta(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            paths = write_inputs(root, [1, 2, 4, 8])
            result = self.call_project(module, paths)

        self.assertEqual("pysolate.cow-fanout-economics.v1", result["schema_version"])
        self.assertEqual(HOST_ID, result["host_id"])
        self.assertEqual(0, result["order_offset"])
        self.assertEqual([1, 2, 4, 8], [cell["fanout"] for cell in result["cells"]])
        cell = result["cells"][0]
        self.assertEqual(2, len(cell["treatments"]))
        self.assertEqual(3, len(cell["treatments"][0]["samples"]))
        self.assertEqual(3, len(cell["paired_full_lifecycle_total_deltas"]))
        pair = cell["paired_full_lifecycle_total_deltas"][0]
        copy_total = 10 + 11 + 12 + 13 + 14
        cow_total = copy_total
        self.assertEqual(
            {
                "iteration": 0,
                "private_copy_full_lifecycle_total_nanos": copy_total,
                "private_cow_full_lifecycle_total_nanos": cow_total,
                "full_lifecycle_total_delta_nanos": 0,
            },
            pair,
        )
        self.assertEqual(evidence(1)["treatments"], cell["treatments"])

    def test_project_rejects_missing_fanout(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            paths = write_inputs(pathlib.Path(raw), [1, 4, 8])
            with self.assertRaisesRegex(ValueError, "missing fanout"):
                self.call_project(module, paths)

    def test_project_rejects_duplicate_fanout(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            paths = write_inputs(pathlib.Path(raw), [1, 2, 4, 4])
            with self.assertRaisesRegex(ValueError, "duplicate fanout"):
                self.call_project(module, paths, fanouts=(1, 2, 4, 4))

    def test_project_rejects_unordered_fanout(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            paths = write_inputs(pathlib.Path(raw), [1, 4, 2, 8])
            with self.assertRaisesRegex(ValueError, "ordered fanout"):
                self.call_project(module, paths, fanouts=(1, 4, 2, 8))

    def test_project_rejects_wrong_schema_identity_or_oracle(self):
        module = load_module()
        for field, value, message in (
            ("schema_version", "wrong.v1", "schema"),
            ("source_tree", "z" * 40, "identity"),
            ("expected_result", 1, "oracle"),
        ):
            with self.subTest(field=field):
                with tempfile.TemporaryDirectory() as raw:
                    root = pathlib.Path(raw)
                    paths = write_inputs(root, [1, 2, 4, 8])
                    document = json.loads(paths[0].read_text())
                    document[field] = value
                    paths[0].write_text(json.dumps(document) + "\n")
                    with self.assertRaisesRegex(ValueError, message):
                        self.call_project(module, paths)

    def test_project_rejects_sample_count_drift(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            paths = write_inputs(root, [1, 2, 4, 8])
            document = json.loads(paths[0].read_text())
            document["treatments"][0]["samples"].pop()
            paths[0].write_text(json.dumps(document) + "\n")
            with self.assertRaisesRegex(ValueError, "sample count"):
                self.call_project(module, paths)

    def test_project_rejects_sample_array_length_drift(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            paths = write_inputs(root, [1, 2, 4, 8])
            document = json.loads(paths[0].read_text())
            document["treatments"][1]["samples"][0]["run_nanos"].pop()
            paths[0].write_text(json.dumps(document) + "\n")
            with self.assertRaisesRegex(ValueError, "array length"):
                self.call_project(module, paths)

    def test_project_rejects_process_memory_drift(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            paths = write_inputs(root, [1, 2, 4, 8])
            document = json.loads(paths[0].read_text())
            document["treatments"][1]["samples"][0]["pss_run_delta_bytes"] = "invalid"
            paths[0].write_text(json.dumps(document) + "\n")
            with self.assertRaisesRegex(ValueError, "process memory"):
                self.call_project(module, paths)

    def test_gate_and_fixture_expose_sweep_contract(self):
        gate = GATE.read_text()
        fixture = RUNTIME_FIXTURE.read_text()
        for marker in (
            "COW_FANOUTS",
            "COW_FANOUT_RUNS",
            "COW_ORDER_OFFSET",
            "EVALUATION_HOST_ID",
            "prepared-family-economics-gate.sh",
            "project-cow-fanout-economics.py",
        ):
            self.assertIn(marker, gate)
        self.assertIn("PYSOLATE_PREPARED_FAMILY_ECONOMICS_ORDER_OFFSET", fixture)
        self.assertIn("COW_FANOUT_GO_TEST_TIMEOUT", gate)
        self.assertIn("PYSOLATE_PREPARED_FAMILY_GO_TEST_TIMEOUT", gate)
        self.assertIn("pysolate.cow-fanout-economics.v1", SCRIPT.read_text())


if __name__ == "__main__":
    unittest.main()
