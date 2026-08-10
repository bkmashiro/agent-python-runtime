import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest
from unittest import mock

ROOT = pathlib.Path(__file__).resolve().parents[2]
WRITER_PATH = ROOT / "guest" / "build" / "write-manifest.py"


def load_writer():
    spec = importlib.util.spec_from_file_location("write_manifest", WRITER_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load manifest writer")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def write_inventory(root: pathlib.Path, profile: str) -> pathlib.Path:
    roots = ["agent_runtime", "json", "sys"]
    if profile == "numpy-core":
        roots.insert(2, "numpy")
    path = root / "import-inventory.json"
    path.write_text(json.dumps({
        "schema_version": 1,
        "artifact_profile": profile,
        "probe": "guest-importlib-find-spec-v1",
        "implementation": "cpython",
        "python_version": "3.14.test",
        "discoverable_roots": roots,
        "failures": [],
    }))
    return path


class ManifestWriterTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.writer = load_writer()

    def test_manifest_binds_artifact_sources_and_wasm_contract(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact = root / "agent-python-runtime.wasm"
            artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
            wat = root / "guest.wat"
            wat.write_text(
                '(module\n  (import "wasi_snapshot_preview1" "fd_write" (func))\n'
                '  (export "memory" (memory 0))\n'
                '  (export "runtime_init" (func 1))\n)\n'
            )
            lock = root / "sources.lock.json"
            lock.write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "target": "wasm32-wasip1",
                        "sources": [
                            {
                                "id": "cpython-source",
                                "version": "3.14.test",
                                "sha256": "a" * 64,
                            }
                        ],
                    }
                )
            )

            inventory = write_inventory(root, "base")
            manifest = self.writer.build_manifest(
                artifact=artifact,
                wat=wat,
                source_lock=lock,
                commit="abc123",
                source_date_epoch="1234567890",
                artifact_profile="base",
                extension_selection=None,
                import_inventory=inventory,
                memory_initial_pages=2048,
                memory_maximum_pages=2048,
            )

            self.assertEqual(3, manifest["schema_version"])
            self.assertEqual("base", manifest["artifact_profile"])
            self.assertEqual(["agent_runtime", "json", "sys"], manifest["python_import_inventory"]["discoverable_roots"])
            self.assertEqual("v1", manifest["abi_version"])
            self.assertEqual(8, manifest["artifact"]["size"])
            self.assertEqual(
                "93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476",
                manifest["artifact"]["sha256"],
            )
            self.assertEqual("abc123", manifest["build"]["repository_commit"])
            self.assertEqual("1234567890", manifest["build"]["source_date_epoch"])
            self.assertEqual("wasm32-wasip1", manifest["target"])
            self.assertEqual(
                [
                    {
                        "id": "cpython-source",
                        "version": "3.14.test",
                        "sha256": "a" * 64,
                    }
                ],
                manifest["sources"],
            )
            self.assertEqual(
                [{"module": "wasi_snapshot_preview1", "name": "fd_write"}],
                manifest["wasm"]["imports"],
            )
            self.assertEqual(["memory", "runtime_init"], manifest["wasm"]["exports"])
            self.assertEqual(
                {"initial_pages": 2048, "maximum_pages": 2048, "fixed": True},
                manifest["wasm"]["memory"],
            )
            limitations = "\n".join(manifest["limitations"])
            self.assertNotIn("capabilities are not implemented", limitations)
            self.assertIn("fetch_many", limitations)
            self.assertIn("built-in capability", limitations)
            self.assertIn("NumPy is not included", limitations)
            self.assertEqual(
                [{"name": "cpython", "version": "3.14.test", "status": "core"}],
                manifest["packages"],
            )

    def test_infers_memory_bounds_from_canonical_wat(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact = root / "agent-python-runtime.wasm"
            artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
            wat = root / "guest.wat"
            wat.write_text('(module\n  (memory (;0;) 3072 32768)\n  (export "memory" (memory 0))\n)\n')
            lock = root / "sources.lock.json"
            lock.write_text(json.dumps({
                "target": "wasm32-wasip1",
                "sources": [{"id": "cpython-source", "version": "3.14.test"}],
                "host_tools": [{"name": "wasi-sdk", "version": "25.0"}],
            }))
            manifest = self.writer.build_manifest(
                artifact=artifact,
                wat=wat,
                source_lock=lock,
                commit="1" * 40,
                source_date_epoch="1",
                artifact_profile="base",
                extension_selection=None,
                import_inventory=write_inventory(root, "base"),
            )
            self.assertEqual(
                {"initial_pages": 3072, "maximum_pages": 32768, "fixed": False},
                manifest["wasm"]["memory"],
            )

    def test_main_uses_bound_environment_commit_without_git(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact = root / "agent-python-runtime.wasm"
            artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
            wat = root / "guest.wat"
            wat.write_text('(module (export "memory" (memory 0)))')
            lock = root / "sources.lock.json"
            lock.write_text(json.dumps({
                "target": "wasm32-wasip1",
                "sources": [{"id": "cpython-source", "version": "3.14.test"}],
            }))
            inventory = write_inventory(root, "base")
            output = root / "manifest.json"
            argv = [
                "write-manifest.py",
                "--artifact", str(artifact),
                "--wat", str(wat),
                "--source-lock", str(lock),
                "--artifact-profile", "base",
                "--import-inventory", str(inventory),
                "--memory-initial-pages", "2048",
                "--memory-maximum-pages", "2048",
                "--output", str(output),
            ]
            with (
                mock.patch.object(self.writer, "git_commit", side_effect=AssertionError("git called")),
                mock.patch.object(sys, "argv", argv),
                mock.patch.dict(self.writer.os.environ, {"GITHUB_SHA": "a" * 40}),
            ):
                self.assertEqual(0, self.writer.main())
            self.assertEqual("a" * 40, json.loads(output.read_text())["build"]["repository_commit"])

    def test_numpy_core_profile_requires_and_binds_selection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact = root / "agent-python-runtime-numpy-core.wasm"
            artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
            wat = root / "guest.wat"
            wat.write_text('(module (export "memory" (memory 0)))')
            lock = root / "sources.lock.json"
            lock.write_text(json.dumps({
                "target": "wasm32-wasip1",
                "sources": [
                    {"id": "cpython-source", "version": "3.14.test"},
                    {"id": "numpy-source", "version": "2.5.test"},
                ],
            }))
            selection = root / "selection-report.json"
            selection.write_text(json.dumps({
                "schema_version": 1,
                "package": "numpy",
                "profile": "core",
                "modules": [
                    {"module": "numpy._core._multiarray_umath"},
                    {"module": "numpy.linalg._umath_linalg"},
                ],
                "link_inputs": [],
            }))

            inventory = write_inventory(root, "numpy-core")
            manifest = self.writer.build_manifest(
                artifact=artifact,
                wat=wat,
                source_lock=lock,
                commit="abc123",
                source_date_epoch="1234567890",
                artifact_profile="numpy-core",
                extension_selection=selection,
                import_inventory=inventory,
                memory_initial_pages=2048,
                memory_maximum_pages=32768,
            )
            self.assertEqual("numpy-core", manifest["artifact_profile"])
            self.assertEqual("core", manifest["extension_profile"]["profile"])
            self.assertEqual(2, len(manifest["extension_profile"]["modules"]))
            self.assertEqual(
                [
                    {"name": "cpython", "version": "3.14.test", "status": "core"},
                    {"name": "numpy", "version": "2.5.test", "status": "selected-core"},
                ],
                manifest["packages"],
            )
            self.assertNotIn("NumPy is not included", "\n".join(manifest["limitations"]))
            self.assertIn("NumPy random and FFT are not included", "\n".join(manifest["limitations"]))

            duplicated = json.loads(selection.read_text())
            duplicated["modules"].append(duplicated["modules"][0])
            selection.write_text(json.dumps(duplicated))
            with self.assertRaisesRegex(ValueError, "exact core extension selection"):
                self.writer.build_manifest(
                    artifact=artifact,
                    wat=wat,
                    source_lock=lock,
                    commit="abc123",
                    source_date_epoch="1234567890",
                    artifact_profile="numpy-core",
                    extension_selection=selection,
                    import_inventory=inventory,
                    memory_initial_pages=2048,
                    memory_maximum_pages=32768,
                )

            with self.assertRaisesRegex(ValueError, "artifact profile"):
                self.writer.build_manifest(
                    artifact=artifact,
                    wat=wat,
                    source_lock=lock,
                    commit="abc123",
                    source_date_epoch="1234567890",
                    artifact_profile="unknown",
                    extension_selection=None,
                    import_inventory=inventory,
                )

    def test_locked_source_version_requires_one_versioned_source(self):
        cases = {
            "missing": {"sources": []},
            "duplicate": {
                "sources": [
                    {"id": "cpython-source", "version": "3.14.0"},
                    {"id": "cpython-source", "version": "3.14.0"},
                ]
            },
            "versionless": {"sources": [{"id": "cpython-source"}]},
            "empty-version": {
                "sources": [{"id": "cpython-source", "version": ""}]
            },
            "whitespace-version": {
                "sources": [{"id": "cpython-source", "version": "   "}]
            },
        }
        for name, lock in cases.items():
            with self.subTest(name=name):
                with self.assertRaisesRegex(ValueError, "exactly one versioned cpython-source"):
                    self.writer.locked_source_version(lock, "cpython-source")

        self.assertEqual(
            "3.14.test",
            self.writer.locked_source_version(
                {
                    "sources": [
                        {"id": "other", "version": "1"},
                        {"id": "cpython-source", "version": "3.14.test"},
                    ]
                },
                "cpython-source",
            ),
        )


if __name__ == "__main__":
    unittest.main()
