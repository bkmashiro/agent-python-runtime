import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "project-linux-evaluation-manifest.py"


def load_module():
    spec = importlib.util.spec_from_file_location("project_linux_evaluation_manifest", SCRIPT)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def digest(path):
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


class LinuxEvaluationManifestTests(unittest.TestCase):
    def fixture(self, root: pathlib.Path):
        commit, tree = "a" * 40, "b" * 40
        for directory in ("artifacts", "plm", "prepared-family", "producer/private"):
            (root / directory).mkdir(parents=True, exist_ok=True)
        (root / "artifacts/base.wasm").write_bytes(b"base")
        (root / "artifacts/numpy-core.wasm").write_bytes(b"numpy")
        base_sha = digest(root / "artifacts/base.wasm")
        numpy_sha = digest(root / "artifacts/numpy-core.wasm")
        profile = [{"name": "cold_end_to_end", "baseline_median_nanos": 10, "plm_median_nanos": 9, "delta_percent": -10.0}]
        documents = {
            "plm/one-read.json": {"schema_version": "pysolate.plm-economics.v1", "target_commit": commit, "artifact_sha256": base_sha, "profiles": profile},
            "plm/four-read.json": {"schema_version": "pysolate.plm-multiread-economics.v1", "target_commit": commit, "artifact_source_commit": commit, "artifact_sha256": base_sha, "profiles": profile},
            "prepared-family/economics.json": {
                "schema_version": "pysolate.prepared-family-economics.v1",
                "source_commit": commit,
                "source_tree": tree,
                "artifact_sha256": numpy_sha,
                "runs_per_arm": 5,
                "fanout": 4,
                "expected_result": 1_048_577,
                "input_element_value": 1,
                "process_memory_source": "/proc/self/smaps_rollup",
                "treatments": [
                    {
                        "mode": mode,
                        "samples": [
                            {
                                "fanout": 4,
                                "result": 1_048_577,
                                "runner_create_nanos": [1, 1, 1, 1],
                                "run_nanos": [1, 1, 1, 1],
                                "runner_close_nanos": [1, 1, 1, 1],
                            }
                            for _ in range(5)
                        ],
                    }
                    for mode in ("private_copy", "private_cow")
                ],
            },
            "producer/public.json": {"schema_version": "pysolate.transparent-campaign-public-projection.v1", "source": {"campaign_source_commit": commit, "artifact_source_commit": commit, "artifact_sha256": base_sha}, "baseline": {"physical_executions": {"median": 19}}, "qualified": {"physical_executions": {"median": 17}}},
            "producer/private/summary.json": {"schema_version": "private"},
            "platform.json": {"schema_version": "pysolate.platform.v1", "hostname": "gpu31"},
        }
        for relative, document in documents.items():
            (root / relative).write_text(json.dumps(document) + "\n")
        return commit, tree

    def test_project_binds_all_lane_identities(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            commit, tree = self.fixture(root)
            result = module.project(root, commit, tree, 1, 5, 4)
            self.assertEqual(17, result["metrics"]["producer_physical_executions"]["qualified"])
            self.assertEqual(-10.0, result["metrics"]["plm"]["four_read"]["cold_end_to_end"]["delta_percent"])

    def test_project_rejects_artifact_drift(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            commit, tree = self.fixture(root)
            document = json.loads((root / "plm/four-read.json").read_text())
            document["artifact_sha256"] = "sha256:" + "0" * 64
            (root / "plm/four-read.json").write_text(json.dumps(document) + "\n")
            with self.assertRaisesRegex(ValueError, "PLM artifact identity drift"):
                module.project(root, commit, tree, 1, 5, 4)

    def test_project_rejects_copy_on_write_semantic_drift(self):
        module = load_module()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            commit, tree = self.fixture(root)
            document = json.loads((root / "prepared-family/economics.json").read_text())
            document["treatments"][0]["samples"][0]["result"] = 1
            (root / "prepared-family/economics.json").write_text(json.dumps(document) + "\n")
            with self.assertRaisesRegex(ValueError, "copy-on-write evidence contract drift"):
                module.project(root, commit, tree, 1, 5, 4)


if __name__ == "__main__":
    unittest.main()
