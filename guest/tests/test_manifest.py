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


def load_qualification_tool():
    tool_path = ROOT / "guest" / "build" / "import_qualification.py"
    spec = importlib.util.spec_from_file_location("manifest_import_qualification", tool_path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load import qualification tool")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def write_inventory(root: pathlib.Path, profile: str) -> pathlib.Path:
    roots = [probe["name"] for probe in load_qualification_tool().probe_specs(profile)]
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


def write_qualification(root: pathlib.Path, profile: str) -> pathlib.Path:
    tool = load_qualification_tool()
    probes = tool.probe_specs(profile)
    roots = [probe["name"] for probe in probes]
    path = root / "import-qualification.json"
    path.write_text(json.dumps({
        "schema_version": 1,
        "artifact_profile": profile,
        "probe": "guest-import-exec-v1",
        "implementation": "cpython",
        "python_version": "3.14.test",
        "qualified_roots": roots,
        "results": [
            {"name": probe["name"], "operation": probe["operation"], "status": "qualified", "error": ""}
            for probe in probes
        ],
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
                import_qualification=write_qualification(root, "base"),
                memory_initial_pages=2048,
                memory_maximum_pages=2048,
            )

            self.assertEqual(4, manifest["schema_version"])
            self.assertEqual("base", manifest["artifact_profile"])
            discoverable = json.loads(inventory.read_text())["discoverable_roots"]
            self.assertEqual(discoverable, manifest["python_import_inventory"]["discoverable_roots"])
            qualified = json.loads((root / "import-qualification.json").read_text())["qualified_roots"]
            self.assertEqual(qualified, manifest["python_import_qualification"]["qualified_roots"])
            self.assertEqual("import-qualification.json", manifest["python_import_qualification"]["filename"])
            self.assertEqual("v2", manifest["abi_version"])
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
            self.assertIn("Host tools are explicitly registered", limitations)
            self.assertIn("does not provide package installation", limitations)
            self.assertEqual(
                [{"name": "cpython", "version": "3.14.test", "status": "core"}],
                manifest["packages"],
            )

    def test_attrs_profile_binds_pinned_package_selection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact = root / "agent-python-runtime-attrs-770.wasm"
            artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
            wat = root / "guest.wat"
            wat.write_text('(module\n  (export "memory" (memory 0))\n)\n')
            lock = root / "sources.lock.json"
            lock.write_text(json.dumps({
                "schema_version": 1,
                "target": "wasm32-wasip1",
                "sources": [
                    {"id": "cpython-source", "version": "3.14.test"},
                    {"id": "attrs-source", "version": "20.3.0-39-g58d2adc"},
                ],
            }))
            selection = root / "extension-profile.json"
            selection.write_text(json.dumps({
                "schema_version": 1,
                "kind": "pure-python-package",
                "profile": "attrs-770",
                "package": self.writer.EXTENSION_PROFILE._expected_selection_package(
                    self.writer.EXTENSION_PROFILE.load_lock(self.writer.EXTENSION_PROFILE.PROFILE_LOCK)
                ),
            }))
            manifest = self.writer.build_manifest(
                artifact=artifact, wat=wat, source_lock=lock, commit="abc123",
                source_date_epoch="1234567890", artifact_profile="attrs-770",
                extension_selection=selection, import_inventory=write_inventory(root, "attrs-770"),
                import_qualification=write_qualification(root, "attrs-770"),
                memory_initial_pages=2048, memory_maximum_pages=2048,
            )
            self.assertEqual("attrs-770", manifest["artifact_profile"])
            self.assertEqual(json.loads(selection.read_text()), manifest["extension_profile"])
            self.assertEqual(
                [
                    {"name": "cpython", "version": "3.14.test", "status": "core"},
                    {"name": "attrs", "version": "20.3.0-39-g58d2adc", "status": "selected-pure-python"},
                ],
                manifest["packages"],
            )
            self.assertIn("one pinned pure-Python package", "\n".join(manifest["limitations"]))

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
                import_qualification=write_qualification(root, "base"),
            )
            self.assertEqual(
                {"initial_pages": 3072, "maximum_pages": 32768, "fixed": False},
                manifest["wasm"]["memory"],
            )

    def test_memory_model_is_reflected_in_wasm_memory_contract(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact = root / "agent-python-runtime.wasm"
            artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
            wat = root / "guest.wat"
            wat.write_text('(module (export "memory" (memory 0)))')
            lock = root / "sources.lock.json"
            lock.write_text(
                json.dumps(
                    {"target": "wasm32-wasip1", "sources": [{"id": "cpython-source", "version": "3.14.test"}]}
                )
            )
            inventory = write_inventory(root, "base")
            qualification = write_qualification(root, "base")

            fixed_manifest = self.writer.build_manifest(
                artifact=artifact,
                wat=wat,
                source_lock=lock,
                commit="abc123",
                source_date_epoch="1234567890",
                artifact_profile="base",
                extension_selection=None,
                import_inventory=inventory,
                import_qualification=qualification,
                memory_initial_pages=2048,
                memory_maximum_pages=2048,
            )
            growable_manifest = self.writer.build_manifest(
                artifact=artifact,
                wat=wat,
                source_lock=lock,
                commit="abc123",
                source_date_epoch="1234567890",
                artifact_profile="base",
                extension_selection=None,
                import_inventory=inventory,
                import_qualification=qualification,
                memory_initial_pages=2048,
                memory_maximum_pages=3072,
            )

            self.assertEqual(
                {"initial_pages": 2048, "maximum_pages": 2048, "fixed": True},
                fixed_manifest["wasm"]["memory"],
            )
            self.assertEqual(
                {"initial_pages": 2048, "maximum_pages": 3072, "fixed": False},
                growable_manifest["wasm"]["memory"],
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
                "--import-qualification", str(write_qualification(root, "base")),
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
