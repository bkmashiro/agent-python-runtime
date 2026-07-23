import copy
import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
GENERATOR_PATH = ROOT / "guest" / "build" / "write-supply-chain.py"


def load_generator():
    spec = importlib.util.spec_from_file_location("write_supply_chain", GENERATOR_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load supply-chain writer")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def digest(data):
    return hashlib.sha256(data).hexdigest()


class SupplyChainWriterTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.writer = load_generator()

    def fixture(self, root):
        artifact = root / "guest.wasm"
        artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
        manifest = {
            "artifact": {"filename": "guest.wasm", "size": 8, "sha256": digest(artifact.read_bytes())},
            "build": {"repository_commit": "a" * 40, "source_date_epoch": "1784729528"},
            "target": "wasm32-wasip1",
        }
        manifest_path = root / "manifest.json"
        lock = {
            "target": "wasm32-wasip1",
            "sources": [
                {
                    "id": "cpython-source",
                    "version": "3.14.0",
                    "url": "https://example.invalid/cpython.tgz",
                    "sha256": "b" * 64,
                    "license": "PSF-2.0",
                    "role": "runtime-source",
                    "artifact_relation": "packaged",
                },
                {
                    "id": "builder",
                    "version": "1.0",
                    "url": "https://example.invalid/builder.tgz",
                    "sha256": "c" * 64,
                    "license": "Apache-2.0",
                    "role": "build-tool",
                    "artifact_relation": "build-only",
                },
            ],
        }
        manifest["sources"] = lock["sources"]
        manifest_path.write_text(json.dumps(manifest))
        lock_path = root / "lock.json"
        lock_path.write_text(json.dumps(lock))
        vfs = root / "vfs"
        (vfs / "site-packages" / "agent_runtime").mkdir(parents=True)
        (vfs / "site-packages" / "agent_runtime" / "__init__.py").write_text("VERSION = 1\n")
        (vfs / "json.py").write_text("# stdlib\n")
        (vfs / "a_test.py").write_text("# sibling\n")
        (vfs / "a_test").mkdir()
        (vfs / "a_test" / "empty").write_text("")
        return artifact, manifest_path, lock_path, vfs

    def test_outputs_are_deterministic_and_bind_actual_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact, manifest, lock, vfs = self.fixture(root)
            first_sbom, first_notices = self.writer.build_outputs(
                artifact=artifact, manifest_path=manifest, source_lock=lock, vfs_root=vfs
            )
            second_sbom, second_notices = self.writer.build_outputs(
                artifact=artifact, manifest_path=manifest, source_lock=lock, vfs_root=vfs
            )
            validation_errors = self.writer.validate_outputs(
                first_sbom, first_notices, artifact, manifest, lock, vfs
            )
            bundle_validation_errors = self.writer.validate_bundle_outputs(
                first_sbom, first_notices, artifact, manifest, lock
            )
            tampered = copy.deepcopy(first_sbom)
            tampered["packages"][1]["versionInfo"] = "tampered"
            tamper_errors = self.writer.validate_bundle_outputs(
                tampered, first_notices, artifact, manifest, lock
            )
        self.assertEqual(first_sbom, second_sbom)
        self.assertEqual(first_notices, second_notices)
        self.assertEqual("SPDX-2.3", first_sbom["spdxVersion"])
        self.assertEqual("CC0-1.0", first_sbom["dataLicense"])
        self.assertEqual(4, len(first_sbom["files"]))
        self.assertEqual(sorted(row["fileName"] for row in first_sbom["files"]), [row["fileName"] for row in first_sbom["files"]])
        package_names = {row["name"] for row in first_sbom["packages"]}
        self.assertEqual({"guest.wasm", "builder", "cpython-source"}, package_names)
        self.assertNotIn("NumPy", first_notices)
        self.assertNotIn("wazero", first_notices)
        self.assertIn("Packaged or linked inputs", first_notices)
        self.assertIn("Build-only inputs", first_notices)
        self.assertEqual([], validation_errors)
        self.assertEqual([], bundle_validation_errors)
        self.assertTrue(any("locked source" in error for error in tamper_errors))

    def test_bundle_validation_rejects_manifest_source_lock_drift(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact, manifest, lock, vfs = self.fixture(root)
            sbom, notices = self.writer.build_outputs(
                artifact=artifact, manifest_path=manifest, source_lock=lock, vfs_root=vfs
            )
            manifest_data = json.loads(manifest.read_text())
            manifest_data["sources"][0]["sha256"] = "d" * 64
            manifest.write_text(json.dumps(manifest_data))
            errors = self.writer.validate_bundle_outputs(
                sbom, notices, artifact, manifest, lock
            )
            self.assertTrue(any("manifest sources" in error for error in errors), errors)

    def test_validation_rejects_artifact_or_vfs_tamper(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact, manifest, lock, vfs = self.fixture(root)
            sbom, notices = self.writer.build_outputs(
                artifact=artifact, manifest_path=manifest, source_lock=lock, vfs_root=vfs
            )
            artifact.write_bytes(b"changed")
            errors = self.writer.validate_outputs(sbom, notices, artifact, manifest, lock, vfs)
            self.assertTrue(any("artifact" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
