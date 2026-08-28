import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
PROJECTOR = ROOT / "scripts" / "project-linux-evaluation-sweeps.py"
MERGER = ROOT / "scripts" / "merge-linux-evaluation-sweeps.py"


def load_module(path: pathlib.Path, name: str):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def digest(path: pathlib.Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


class EvaluationSweepManifestTests(unittest.TestCase):
    def fixture(self, root: pathlib.Path, host_id: str = "gpu31"):
        commit, tree, epoch = "a" * 40, "b" * 40, 1_700_000_000
        (root / "artifacts").mkdir(parents=True)
        (root / "artifacts/base.wasm").write_bytes(b"base-artifact")
        (root / "artifacts/numpy-core.wasm").write_bytes(b"numpy-artifact")
        source = {
            "source_commit": commit,
            "source_tree": tree,
            "source_epoch": epoch,
            "host_id": host_id,
            "order_offset": 0,
            "plm_crossover_runs": 20,
            "cow_fanout_runs": 12,
        }
        (root / "platform.json").write_text(
            json.dumps({
                "schema_version": "pysolate.platform.v1",
                "hostname": f"{host_id}.doc.ic.ac.uk",
                **source,
            })
            + "\n"
        )
        (root / "plm-crossover.json").write_text(
            json.dumps({
                "schema_version": "pysolate.plm-crossover-economics.v1",
                "target_commit": commit,
                "source_tree": tree,
                "artifact_source_commit": commit,
                "artifact_sha256": digest(root / "artifacts/base.wasm"),
                "evaluation_host_id": host_id,
                "order_offset": 0,
                "runs_per_arm": 20,
            })
            + "\n"
        )
        (root / "cow-fanout.json").write_text(
            json.dumps({
                "schema_version": "pysolate.cow-fanout-economics.v1",
                "source_commit": commit,
                "source_tree": tree,
                "artifact_sha256": digest(root / "artifacts/numpy-core.wasm"),
                "host_id": host_id,
                "order_offset": 0,
                "runs": 12,
            })
            + "\n"
        )
        return commit, tree, epoch

    def test_project_is_source_and_artifact_bound(self):
        module = load_module(PROJECTOR, "project_linux_evaluation_sweeps")
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            commit, tree, epoch = self.fixture(root)
            manifest = module.project(root, commit, tree, epoch, "gpu31", 0, 20, 12)
            self.assertEqual("pysolate.linux-evaluation-sweeps.v1", manifest["schema_version"])
            self.assertEqual(commit, manifest["source"]["commit"])
            self.assertEqual("gpu31", manifest["host"]["id"])
            self.assertEqual(digest(root / "artifacts/base.wasm"), manifest["artifacts"]["base"]["sha256"])
            self.assertTrue(manifest["complete"])

    def test_project_rejects_lane_source_drift(self):
        module = load_module(PROJECTOR, "project_linux_evaluation_sweeps")
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            commit, tree, epoch = self.fixture(root)
            document = json.loads((root / "cow-fanout.json").read_text())
            document["source_tree"] = "c" * 40
            (root / "cow-fanout.json").write_text(json.dumps(document) + "\n")
            with self.assertRaisesRegex(ValueError, "source tree drift"):
                module.project(root, commit, tree, epoch, "gpu31", 0, 20, 12)

    def manifest(self, root: pathlib.Path, host_id: str, artifact_sha: str = "sha256:" + "a" * 64, config=None):
        config = config or {"plm_crossover_runs": 10, "cow_fanout_runs": 6, "order_offset": 0}
        return {
            "schema_version": "pysolate.linux-evaluation-sweeps.v1",
            "source": {"commit": "c" * 40, "tree": "d" * 40, "epoch": 1_700_000_000},
            "host": {"id": host_id, "hostname": f"{host_id}.doc.ic.ac.uk"},
            "config": config,
            "schemas": {
                "platform": "pysolate.platform.v1",
                "plm_crossover": "pysolate.plm-crossover-economics.v1",
                "cow_fanout": "pysolate.cow-fanout-economics.v1",
            },
            "artifacts": {
                "base": {"path": "artifacts/base.wasm", "sha256": artifact_sha},
                "numpy_core": {"path": "artifacts/numpy-core.wasm", "sha256": "sha256:" + "b" * 64},
            },
            "complete": True,
            "platform": {"schema_version": "pysolate.platform.v1"},
            "evidence": {},
        }

    def test_merge_requires_complete_consistent_selected_manifests_and_keeps_blocks(self):
        module = load_module(MERGER, "merge_linux_evaluation_sweeps")
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            first = self.manifest(root, "gpu31")
            second = self.manifest(root, "gpu32")
            second["config"] = {"plm_crossover_runs": 10, "cow_fanout_runs": 6, "order_offset": 1}
            paths = []
            for host, document in (("gpu31", first), ("gpu32", second)):
                path = root / f"{host}.json"
                path.write_text(json.dumps(document) + "\n")
                paths.append(path)
            merged = module.merge_manifests(paths, ["gpu31", "gpu32"])
            self.assertEqual(["gpu31", "gpu32"], merged["selected_hosts"])
            self.assertEqual(["gpu31", "gpu32"], [row["host"]["id"] for row in merged["host_blocks"]])

            second["artifacts"]["base"]["sha256"] = "sha256:" + "e" * 64
            paths[1].write_text(json.dumps(second) + "\n")
            with self.assertRaisesRegex(ValueError, "artifact drift"):
                module.merge_manifests(paths, ["gpu31", "gpu32"])

    def test_merge_rejects_missing_selected_host(self):
        module = load_module(MERGER, "merge_linux_evaluation_sweeps")
        with tempfile.TemporaryDirectory() as raw:
            path = pathlib.Path(raw) / "gpu31.json"
            path.write_text(json.dumps(self.manifest(path.parent, "gpu31")) + "\n")
            with self.assertRaisesRegex(ValueError, "selected host manifest count"):
                module.merge_manifests([path], ["gpu31", "gpu32"])


if __name__ == "__main__":
    unittest.main()
