import importlib.util
import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
TOOL_PATH = ROOT / "guest" / "build" / "package_profile.py"
REGISTRY_PATH = ROOT / "guest" / "build" / "profiles" / "registry.v1.json"
BUILD_SCRIPT = ROOT / "guest" / "build" / "build-guest.sh"


def load_tool():
    spec = importlib.util.spec_from_file_location("package_profile", TOOL_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load package profile tool")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class PackageProfileTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tool = load_tool()

    def test_registry_resolves_base_and_package_profiles_as_closed_profiles(self):
        registry = self.tool.load_registry(REGISTRY_PATH)
        self.assertEqual(("base", "attrs-770", "numpy-core"), self.tool.profile_ids(registry))
        base = self.tool.resolve_profile(registry, "base")
        self.assertEqual("base", base["kind"])
        self.assertIsNone(base["lock"])
        self.assertIsNone(base["recipe"])
        attrs = self.tool.resolve_profile(registry, "attrs-770")
        self.assertEqual("pure-python-package", attrs["kind"])
        self.assertEqual("attrs-770.lock.json", attrs["lock"])
        self.assertEqual("attrs-770-v1", attrs["recipe"])
        self.assertEqual(["extension_patch"], attrs["private_inputs"])
        numpy = self.tool.resolve_profile(registry, "numpy-core")
        self.assertEqual("static-native-package", numpy["kind"])
        self.assertEqual("numpy-core.lock.json", numpy["lock"])
        self.assertEqual("numpy-static-v1", numpy["recipe"])
        self.assertEqual(["agent_runtime", "json", "numpy", "sys"], numpy["required_import_roots"])
        with self.assertRaisesRegex(ValueError, "unsupported package profile"):
            self.tool.resolve_profile(registry, "pandas")

    def test_registry_is_strict_and_rejects_paths_or_duplicate_profiles(self):
        registry = json.loads(REGISTRY_PATH.read_text())
        forged = json.loads(json.dumps(registry))
        forged["profiles"][1]["lock"] = "../escape.json"
        with self.assertRaisesRegex(ValueError, "profile path"):
            self.tool.validate_registry(forged)
        forged = json.loads(json.dumps(registry))
        forged["profiles"].append(dict(forged["profiles"][1]))
        with self.assertRaisesRegex(ValueError, "sorted unique"):
            self.tool.validate_registry(forged)
        forged = json.loads(json.dumps(registry))
        forged["profiles"][0]["runtime_install"] = True
        with self.assertRaisesRegex(ValueError, "fields"):
            self.tool.validate_registry(forged)

    def test_resolved_attrs_contract_preserves_legacy_lock_identity(self):
        registry = self.tool.load_registry(REGISTRY_PATH)
        attrs = self.tool.resolve_profile(registry, "attrs-770")
        contract = self.tool.load_package_contract(attrs)
        self.assertEqual("attrs-770", contract["artifact_profile"])
        self.assertEqual("selected-pure-python", contract["package"]["status"])
        self.assertEqual("f1e3b25ec86f639a4ce256f5c1216fd585527142a08a284cc5fd9c9de603229f", contract["package"]["tree_sha256"])
        source_lock = self.tool.source_lock_projection(attrs, contract)
        self.assertEqual(["attrs-source"], [row["id"] for row in source_lock["sources"]])

        numpy = self.tool.resolve_profile(registry, "numpy-core")
        numpy_contract = self.tool.load_package_contract(numpy)
        self.assertEqual("static-native-package", numpy_contract["kind"])
        self.assertEqual(19, len(numpy_contract["native_modules"]))
        numpy_sources = self.tool.source_lock_projection(numpy, numpy_contract)
        self.assertEqual(["cython-source", "numpy-source", "setuptools-wheel"], [row["id"] for row in numpy_sources["sources"]])

    def test_build_script_resolves_registry_before_cache_and_keeps_base_default(self):
        text = BUILD_SCRIPT.read_text()
        resolve = text.index('"${PACKAGE_PROFILE_TOOL}" field')
        cache = text.index("FINAL_CACHE_KEY=")
        self.assertLess(resolve, cache)
        self.assertIn("AGENT_RUNTIME_ARTIFACT_PROFILE:-base", text)
        self.assertNotIn("case \"${ARTIFACT_PROFILE}\" in\n  base)", text)


if __name__ == "__main__":
    unittest.main()
