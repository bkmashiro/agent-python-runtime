import importlib.util
import json
import pathlib
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "build" / "cache_identity.py"
SPEC = importlib.util.spec_from_file_location("guest_cache_identity", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
cache_identity = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(cache_identity)


class BuildCacheIdentityTests(unittest.TestCase):
    def make_repository(self, root: pathlib.Path) -> None:
        (root / "guest/build").mkdir(parents=True, exist_ok=True)
        (root / "guest/bootstrap/agent_runtime").mkdir(parents=True, exist_ok=True)
        (root / "tools").mkdir(exist_ok=True)
        (root / "guest/build/sources.lock.json").write_text(
            json.dumps({"schema_version": 1, "target": "wasm32-wasip1", "sources": [{"id": "cpython", "sha256": "a" * 64}]})
        )
        (root / "guest/build/build-guest.sh").write_text(
            "before\n# BEGIN CPYTHON CACHE RECIPE\nrecipe-v1\n# END CPYTHON CACHE RECIPE\nafter\n"
        )
        (root / "guest/build/cache_identity.py").write_text("identity-v1\n")
        (root / "guest/build/cache_maintenance.py").write_text("maintenance-v1\n")
        (root / "guest/build/validate_cache_layer.py").write_text("validator-v1\n")
        (root / "tools/patch_cpython_wasi_timer_config.py").write_text("timer-v1\n")
        (root / "tools/patch_cpython_import_gate.py").write_text("import-v1\n")
        (root / "guest/bootstrap/agent_runtime/__init__.py").write_text("bootstrap-v1\n")

    def test_identity_ignores_bootstrap_only_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.make_repository(root)
            first = cache_identity.build_identity(root, "Linux", "x86_64")
            (root / "guest/bootstrap/agent_runtime/__init__.py").write_text("bootstrap-v2\n")
            second = cache_identity.build_identity(root, "Linux", "x86_64")
            self.assertEqual(first, second)

    def test_identity_changes_with_locked_inputs_recipe_patch_or_host(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.make_repository(root)
            baseline = cache_identity.build_identity(root, "Linux", "x86_64")
            lock = root / "guest/build/sources.lock.json"
            lock.write_text(lock.read_text().replace("a" * 64, "b" * 64))
            self.assertNotEqual(baseline, cache_identity.build_identity(root, "Linux", "x86_64"))
            self.make_repository(root)
            recipe = root / "guest/build/build-guest.sh"
            recipe.write_text(recipe.read_text().replace("recipe-v1", "recipe-v2"))
            self.assertNotEqual(baseline, cache_identity.build_identity(root, "Linux", "x86_64"))
            self.make_repository(root)
            patch = root / "tools/patch_cpython_import_gate.py"
            patch.write_text("import-v2\n")
            self.assertNotEqual(baseline, cache_identity.build_identity(root, "Linux", "x86_64"))
            self.make_repository(root)
            identity = root / "guest/build/cache_identity.py"
            identity.write_text("identity-v2\n")
            self.assertNotEqual(baseline, cache_identity.build_identity(root, "Linux", "x86_64"))
            self.make_repository(root)
            self.assertNotEqual(baseline, cache_identity.build_identity(root, "Linux", "aarch64"))

    def test_missing_or_duplicate_recipe_markers_fail_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.make_repository(root)
            script = root / "guest/build/build-guest.sh"
            script.write_text("no markers\n")
            with self.assertRaises(ValueError):
                cache_identity.build_identity(root, "Linux", "x86_64")
            script.write_text(
                "# BEGIN CPYTHON CACHE RECIPE\na\n# END CPYTHON CACHE RECIPE\n"
                "# BEGIN CPYTHON CACHE RECIPE\nb\n# END CPYTHON CACHE RECIPE\n"
            )
            with self.assertRaises(ValueError):
                cache_identity.build_identity(root, "Linux", "x86_64")


if __name__ == "__main__":
    unittest.main()
