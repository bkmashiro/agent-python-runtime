import copy
import importlib.util
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
VERIFIER_PATH = ROOT / "guest" / "build" / "verify-artifact.py"


def load_verifier():
    spec = importlib.util.spec_from_file_location("verify_artifact", VERIFIER_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load artifact verifier")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ArtifactVerifierTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.verifier = load_verifier()

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.artifact = pathlib.Path(self.directory.name) / "agent-python-runtime.wasm"
        self.artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
        self.manifest = {
            "schema_version": 2,
            "abi_version": "v2",
            "artifact_profile": "base",
            "target": "wasm32-wasip1",
            "artifact": {
                "filename": "agent-python-runtime.wasm",
                "size": 8,
                "sha256": "93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476",
            },
            "build": {
                "repository_commit": "a" * 40,
                "source_date_epoch": "1234567890",
                "compiler_target": "wasm32-wasip1",
                "execution_model": "reactor",
            },
            "sources": [{"id": "test-source"}],
            "wasm": {
                "imports": [
                    {"module": "wasi_snapshot_preview1", "name": "fd_write"},
                    {"module": "agent_runtime_v2", "name": "host_call"},
                    {"module": "agent_runtime_v2", "name": "materialize_value"},
                    {"module": "agent_runtime_v2", "name": "submit_call"},
                    {"module": "agent_runtime_v2", "name": "materialize_call"},
                    {"module": "agent_runtime_v2", "name": "prepare_plm_call"},
                    {"module": "agent_runtime_v2", "name": "linearize_plm_call"},
                    {"module": "agent_runtime_v2", "name": "materialize_slot"},
                ],
                "exports": [
                    "_initialize",
                    "memory",
                    "runtime_init",
                    "runtime_validate_source",
                    "runtime_validate_source_for_patch",
                    "runtime_analyze_source",
                    "runtime_transform_source_pass",
                    "runtime_select_source_pass_execution",
                    "runtime_emit_prepared_region_patch",
                    "runtime_execute_prepared_region_scratch",
                    "runtime_select_prepared_region_execution",
                    "runtime_prepare",
                    "runtime_prepare_numpy_ndarray",
                    "alloc",
                    "dealloc",
                    "execute",
                    "wasi_vfs_pack_fs",
                    "__wasi_vfs_rt_init",
                ],
            },
            "packages": [{"name": "cpython", "version": "3.14.test", "status": "core"}],
            "extension_profile": None,
            "limitations": [],
        }

    def test_accepts_bound_neutral_contract(self):
        self.verifier.verify(self.artifact, self.manifest)

    def test_rejects_digest_mismatch(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["artifact"]["sha256"] = "0" * 64
        with self.assertRaisesRegex(ValueError, "sha256"):
            self.verifier.verify(self.artifact, manifest)

    def test_rejects_forbidden_import_module(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["wasm"]["imports"].append({"module": "env", "name": "socket"})
        with self.assertRaisesRegex(ValueError, "import module"):
            self.verifier.verify(self.artifact, manifest)

    def test_rejects_forbidden_import_name_in_capability_module(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["wasm"]["imports"].append({"module": "agent_runtime_v2", "name": "raw_socket"})
        with self.assertRaisesRegex(ValueError, "forbidden import"):
            self.verifier.verify(self.artifact, manifest)

    def test_rejects_missing_prepared_region_import(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["wasm"]["imports"] = [row for row in manifest["wasm"]["imports"] if row["name"] != "materialize_value"]
        with self.assertRaisesRegex(ValueError, "missing required import"):
            self.verifier.verify(self.artifact, manifest)

    def test_rejects_missing_required_export(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["wasm"]["exports"].remove("execute")
        with self.assertRaisesRegex(ValueError, "missing exports"):
            self.verifier.verify(self.artifact, manifest)

    def test_rejects_command_start_export(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["wasm"]["exports"].append("_start")
        with self.assertRaisesRegex(ValueError, "_start"):
            self.verifier.verify(self.artifact, manifest)

    def test_rejects_unknown_or_filename_mismatched_profile(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["artifact_profile"] = "unknown"
        with self.assertRaisesRegex(ValueError, "artifact profile"):
            self.verifier.verify(self.artifact, manifest)

        manifest = copy.deepcopy(self.manifest)
        manifest["artifact"]["filename"] = "agent-python-runtime-numpy-core.wasm"
        with self.assertRaisesRegex(ValueError, "filename"):
            self.verifier.verify(self.artifact, manifest)

    def test_schema_v3_binds_guest_import_inventory(self):
        inventory = pathlib.Path(self.directory.name) / "import-inventory.json"
        payload = {
            "schema_version": 1,
            "artifact_profile": "base",
            "probe": "guest-importlib-find-spec-v1",
            "implementation": "cpython",
            "python_version": "3.14.test",
            "discoverable_roots": ["agent_runtime", "json", "sys"],
            "failures": [],
        }
        inventory.write_text(json.dumps(payload, sort_keys=True))
        manifest = copy.deepcopy(self.manifest)
        manifest["schema_version"] = 3
        manifest["python_import_inventory"] = {
            **{key: value for key, value in payload.items() if key != "artifact_profile"},
            "filename": inventory.name,
            "sha256": self.verifier.sha256(inventory),
        }
        self.verifier.verify(self.artifact, manifest, None, inventory)

        manifest["python_import_inventory"]["discoverable_roots"] = ["agent_runtime", "sys"]
        with self.assertRaisesRegex(ValueError, "does not match"):
            self.verifier.verify(self.artifact, manifest, None, inventory)

    def test_rejects_unknown_manifest_fields_and_duplicate_json_keys(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["operator_note"] = "ignored by old verifier"
        with self.assertRaisesRegex(ValueError, "manifest fields"):
            self.verifier.verify(self.artifact, manifest)
        with tempfile.TemporaryDirectory() as directory:
            duplicate = pathlib.Path(directory) / "duplicate.json"
            duplicate.write_text('{"schema_version":4,"schema_version":4}')
            with self.assertRaisesRegex(ValueError, "duplicate JSON key"):
                self.verifier.load_json_strict(duplicate)
            nonstandard = pathlib.Path(directory) / "nonstandard.json"
            nonstandard.write_text('{"value":NaN}')
            with self.assertRaisesRegex(ValueError, "non-standard JSON constant"):
                self.verifier.load_json_strict(nonstandard)

    def test_rejects_unknown_fields_in_go_typed_nested_objects(self):
        mutations = {
            "artifact": lambda value: value["artifact"].update(operator_note="x"),
            "build": lambda value: value["build"].update(operator_note="x"),
            "package": lambda value: value["packages"][0].update(operator_note="x"),
        }
        for label, mutate in mutations.items():
            with self.subTest(label=label):
                manifest = copy.deepcopy(self.manifest)
                mutate(manifest)
                with self.assertRaisesRegex(ValueError, "fields are invalid"):
                    self.verifier.verify(self.artifact, manifest)

    def test_schema_v4_binds_guest_import_qualification(self):
        tool = self.verifier.IMPORT_QUALIFICATION
        probes = tool.probe_specs("base")
        roots = [probe["name"] for probe in probes]
        inventory_payload = {
            "schema_version": 1,
            "artifact_profile": "base",
            "probe": "guest-importlib-find-spec-v1",
            "implementation": "cpython",
            "python_version": "3.14.test",
            "discoverable_roots": roots,
            "failures": [],
        }
        inventory = pathlib.Path(self.directory.name) / "import-inventory.json"
        inventory.write_text(json.dumps(inventory_payload, sort_keys=True))
        qualification_payload = {
            "schema_version": 1,
            "artifact_profile": "base",
            "probe": "guest-import-exec-v1",
            "implementation": "cpython",
            "python_version": "3.14.test",
            "qualified_roots": roots,
            "results": [
                {"name": probe["name"], "operation": probe["operation"], "status": "qualified", "error": ""}
                for probe in probes
            ],
        }
        qualification = pathlib.Path(self.directory.name) / "import-qualification.json"
        qualification.write_text(json.dumps(qualification_payload, sort_keys=True))
        manifest = copy.deepcopy(self.manifest)
        manifest["schema_version"] = 4
        manifest["python_import_inventory"] = {
            **{key: value for key, value in inventory_payload.items() if key != "artifact_profile"},
            "filename": inventory.name,
            "sha256": self.verifier.sha256(inventory),
        }
        manifest["python_import_qualification"] = {
            **{key: value for key, value in qualification_payload.items() if key != "artifact_profile"},
            "filename": qualification.name,
            "sha256": self.verifier.sha256(qualification),
        }
        self.verifier.verify(self.artifact, manifest, None, inventory, qualification)

        result_extra = copy.deepcopy(manifest)
        result_extra["python_import_qualification"]["results"][0]["operator_note"] = "x"
        with self.assertRaisesRegex(ValueError, "result fields are invalid"):
            self.verifier.verify(self.artifact, result_extra, None, inventory, qualification)

        failure_extra = copy.deepcopy(manifest)
        failure_extra["python_import_inventory"]["failures"] = [
            {"name": "missing", "error": "not_found", "operator_note": "x"}
        ]
        with self.assertRaisesRegex(ValueError, "failure fields are invalid"):
            self.verifier.verify(self.artifact, failure_extra, None, inventory, qualification)

        manifest["python_import_qualification"]["qualified_roots"] = ["agent_runtime", "json", "sys"]
        with self.assertRaisesRegex(ValueError, "does not match"):
            self.verifier.verify(self.artifact, manifest, None, inventory, qualification)

    def test_attrs_profile_binds_embedded_package_source_and_qualification(self):
        self.artifact = pathlib.Path(self.directory.name) / "agent-python-runtime-attrs-770.wasm"
        self.artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
        profile = "attrs-770"
        probes = self.verifier.IMPORT_QUALIFICATION.probe_specs(profile)
        roots = [probe["name"] for probe in probes]
        inventory_payload = {
            "schema_version": 1, "artifact_profile": profile, "probe": "guest-importlib-find-spec-v1",
            "implementation": "cpython", "python_version": "3.14.test",
            "discoverable_roots": roots, "failures": [],
        }
        inventory = pathlib.Path(self.directory.name) / "import-inventory.json"
        inventory.write_text(json.dumps(inventory_payload, sort_keys=True))
        qualification_payload = {
            "schema_version": 1, "artifact_profile": profile, "probe": "guest-import-exec-v1",
            "implementation": "cpython", "python_version": "3.14.test", "qualified_roots": roots,
            "results": [
                {"name": probe["name"], "operation": probe["operation"], "status": "qualified", "error": ""}
                for probe in probes
            ],
        }
        qualification = pathlib.Path(self.directory.name) / "import-qualification.json"
        qualification.write_text(json.dumps(qualification_payload, sort_keys=True))
        extension = {
            "schema_version": 1, "kind": "pure-python-package", "profile": profile,
            "package": self.verifier.EXTENSION_PROFILE._expected_selection_package(self.verifier.ATTRS_LOCK),
        }
        manifest = copy.deepcopy(self.manifest)
        manifest.update({
            "schema_version": 4, "artifact_profile": profile,
            "artifact": {
                "filename": self.artifact.name, "size": 8,
                "sha256": "93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476",
            },
            "sources": copy.deepcopy(self.verifier.ATTRS_SOURCES),
            "packages": [
                {"name": "cpython", "version": "3.14.test", "status": "core"},
                {"name": "attrs", "version": "20.3.0-39-g58d2adc", "status": "selected-pure-python"},
            ],
            "extension_profile": extension,
            "python_import_inventory": {
                **{key: value for key, value in inventory_payload.items() if key != "artifact_profile"},
                "filename": inventory.name, "sha256": self.verifier.sha256(inventory),
            },
            "python_import_qualification": {
                **{key: value for key, value in qualification_payload.items() if key != "artifact_profile"},
                "filename": qualification.name, "sha256": self.verifier.sha256(qualification),
            },
        })
        self.verifier.verify(self.artifact, manifest, None, inventory, qualification)
        manifest_path = pathlib.Path(self.directory.name) / "manifest.json"
        extension_path = pathlib.Path(self.directory.name) / "extension-profile.json"
        manifest_path.write_text(json.dumps(manifest, sort_keys=True))
        extension_path.write_text(json.dumps(extension, sort_keys=True))
        result = subprocess.run(
            [
                sys.executable,
                str(pathlib.Path(__file__).parents[1] / "build" / "verify-artifact.py"),
                str(self.artifact),
                str(manifest_path),
                "--extension-selection",
                str(extension_path),
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(0, result.returncode, result.stderr)
        manifest["sources"][0]["sha256"] = "d" * 64
        with self.assertRaisesRegex(ValueError, "source set"):
            self.verifier.verify(self.artifact, manifest, None, inventory, qualification)



if __name__ == "__main__":
    unittest.main()
