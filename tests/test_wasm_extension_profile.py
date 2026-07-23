import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
TOOL = ROOT / "tools" / "resolve_wasm_extension_profile.py"
CONFIG = ROOT / "experiments" / "numpy-wasi" / "feature-profiles.json"


def load_tool():
    spec = importlib.util.spec_from_file_location("resolve_wasm_extension_profile", TOOL)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load profile resolver")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class WasmExtensionProfileTests(unittest.TestCase):
    def fixture(self, root: Path):
        build = root / "build"
        manifests = root / "manifests"
        (build / "pkg").mkdir(parents=True)
        (manifests / "pkg").mkdir(parents=True)
        for name in ("core.a", "extra.a", "shared.a"):
            (build / "pkg" / name).write_bytes(name.encode())
        (manifests / "pkg" / "core.json").write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "kind": "wasm-static-python-extension",
                    "archive": "pkg/core.a",
                    "static_inputs": ["pkg/shared.a"],
                }
            )
        )
        (manifests / "pkg" / "extra.json").write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "kind": "wasm-static-python-extension",
                    "archive": "pkg/extra.a",
                    "static_inputs": ["pkg/shared.a"],
                }
            )
        )
        config = root / "profiles.json"
        config.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "package": "fixture",
                    "profiles": {
                        "core": {
                            "modules": [
                                {
                                    "module": "pkg._core",
                                    "init_symbol": "PyInit__core",
                                    "manifest": "pkg/core.json",
                                }
                            ]
                        },
                        "extra": {
                            "extends": ["core"],
                            "modules": [
                                {
                                    "module": "pkg._extra",
                                    "init_symbol": "PyInit__extra",
                                    "manifest": "pkg/extra.json",
                                }
                            ],
                        },
                    },
                }
            )
        )
        return config, manifests, build

    def test_resolves_inherited_modules_and_deduplicates_static_inputs(self):
        tool = load_tool()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            config, manifests, build = self.fixture(root)
            result = tool.resolve_profile(config, "extra", manifests, build)
            self.assertEqual(
                [item["module"] for item in result["modules"]],
                ["pkg._core", "pkg._extra"],
            )
            self.assertEqual(
                [Path(value).relative_to(build.resolve()).as_posix() for value in result["extension_archives"]],
                ["pkg/core.a", "pkg/extra.a"],
            )
            self.assertEqual(
                [Path(value).relative_to(build.resolve()).as_posix() for value in result["static_inputs"]],
                ["pkg/shared.a"],
            )
            self.assertEqual(
                [Path(value).relative_to(build.resolve()).as_posix() for value in result["link_inputs"]],
                ["pkg/core.a", "pkg/extra.a", "pkg/shared.a"],
            )
            header = tool.render_registry_header(result)
            self.assertIn("extern PyObject *PyInit__core(void);", header)
            self.assertIn('PyImport_AppendInittab("pkg._extra", PyInit__extra)', header)
            self.assertIn("register_selected_builtins", header)

    def test_rejects_inheritance_cycle(self):
        tool = load_tool()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            config, manifests, build = self.fixture(root)
            payload = json.loads(config.read_text())
            payload["profiles"]["core"]["extends"] = ["extra"]
            config.write_text(json.dumps(payload))
            with self.assertRaisesRegex(ValueError, "profile inheritance cycle"):
                tool.resolve_profile(config, "extra", manifests, build)

    def test_rejects_manifest_escape_and_archive_drift(self):
        tool = load_tool()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            config, manifests, build = self.fixture(root)
            payload = json.loads(config.read_text())
            payload["profiles"]["core"]["modules"][0]["manifest"] = "../escape.json"
            config.write_text(json.dumps(payload))
            with self.assertRaisesRegex(ValueError, "manifest path"):
                tool.resolve_profile(config, "core", manifests, build)

            drift_root = root / "drift"
            config, manifests, build = self.fixture(drift_root)
            manifest = manifests / "pkg" / "core.json"
            data = json.loads(manifest.read_text())
            data["archive"] = "pkg/not-core.a"
            manifest.write_text(json.dumps(data))
            with self.assertRaisesRegex(ValueError, "archive does not match manifest"):
                tool.resolve_profile(config, "core", manifests, build)

    def test_repository_profiles_declare_core_and_random_feature_closures(self):
        payload = json.loads(CONFIG.read_text())
        self.assertEqual(payload["schema_version"], 1)
        self.assertEqual(payload["package"], "numpy")
        self.assertEqual(
            [item["module"] for item in payload["profiles"]["core"]["modules"]],
            ["numpy._core._multiarray_umath", "numpy.linalg._umath_linalg"],
        )
        random_profile = payload["profiles"]["random"]
        self.assertEqual(random_profile["extends"], ["core"])
        self.assertEqual(len(random_profile["modules"]), 9)
        self.assertEqual(
            {item["module"] for item in random_profile["modules"]},
            {
                "numpy.random._bounded_integers",
                "numpy.random._common",
                "numpy.random._generator",
                "numpy.random._mt19937",
                "numpy.random._pcg64",
                "numpy.random._philox",
                "numpy.random._sfc64",
                "numpy.random.bit_generator",
                "numpy.random.mtrand",
            },
        )


if __name__ == "__main__":
    unittest.main()
