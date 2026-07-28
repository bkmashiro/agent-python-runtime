import copy
import importlib.util
import json
import pathlib
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
            "abi_version": "v1",
            "artifact_profile": "base",
            "target": "wasm32-wasip1",
            "artifact": {
                "filename": "agent-python-runtime.wasm",
                "size": 8,
                "sha256": "93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476",
            },
            "wasm": {
                "imports": [
                    {"module": "wasi_snapshot_preview1", "name": "fd_write"},
                    {"module": "agent_runtime_v1", "name": "host_call"},
                ],
                "exports": [
                    "_initialize",
                    "memory",
                    "runtime_init",
                    "runtime_prepare",
                    "runtime_warmup",
                    "alloc",
                    "dealloc",
                    "execute",
                    "wasi_vfs_pack_fs",
                    "__wasi_vfs_rt_init",
                ],
            },
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
        manifest["wasm"]["imports"].append({"module": "agent_runtime_v1", "name": "raw_socket"})
        with self.assertRaisesRegex(ValueError, "forbidden import"):
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

    def test_numpy_core_requires_bound_core_extension_selection(self):
        numpy_artifact = pathlib.Path(self.directory.name) / "agent-python-runtime-numpy-core.wasm"
        numpy_artifact.write_bytes(self.artifact.read_bytes())
        manifest = copy.deepcopy(self.manifest)
        manifest["artifact_profile"] = "numpy-core"
        manifest["artifact"]["filename"] = numpy_artifact.name
        with self.assertRaisesRegex(ValueError, "extension profile"):
            self.verifier.verify(numpy_artifact, manifest)

        selection = pathlib.Path(self.directory.name) / "extension-selection.json"
        selection.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "package": "numpy",
                    "profile": "core",
                    "modules": [
                        {"module": "numpy._core._multiarray_umath"},
                        {"module": "numpy.linalg._umath_linalg"},
                    ],
                    "link_inputs": [{"path": "one.a"}],
                }
            )
        )
        manifest["extension_profile"] = {
            "filename": selection.name,
            "profile": "core",
            "manifest_sha256": self.verifier.sha256(selection),
            "modules": [
                "numpy._core._multiarray_umath",
                "numpy.linalg._umath_linalg",
            ],
            "link_input_count": 1,
        }
        self.verifier.verify(numpy_artifact, manifest, selection)


if __name__ == "__main__":
    unittest.main()
