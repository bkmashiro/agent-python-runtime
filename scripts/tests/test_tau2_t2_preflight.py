import argparse
import importlib.util
import pathlib
import tempfile
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-t2-preflight.py"
SPEC = importlib.util.spec_from_file_location("tau2_t2_preflight", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
preflight = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(preflight)


class Tau2T2PreflightTests(unittest.TestCase):
    def test_relative_paths_are_frozen_to_absolute_paths(self):
        names = ("source_root", "repo_root", "tau2_python", "artifact", "private_manifest", "evidence_root", "public_output")
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(**{name: directory for name in names})
            resolved = preflight.resolve_paths(args)
            for name in names:
                self.assertTrue(pathlib.Path(getattr(resolved, name)).is_absolute())
                expected = pathlib.Path(directory).absolute() if name == "tau2_python" else pathlib.Path(directory).resolve()
                self.assertEqual(pathlib.Path(getattr(resolved, name)), expected)

    def test_source_shapes_are_exact(self):
        source, names = preflight.source_for("search_direct_flight", {"origin": "A", "destination": "B", "date": "C"})
        self.assertEqual(names, ["origin", "destination", "date"])
        self.assertEqual(source, "result = tools.search_direct_flight('A', 'B', 'C')")
        with self.assertRaises(ValueError):
            preflight.source_for("search_direct_flight", {"origin": "A"})

    def test_python_executable_is_not_symlink_resolved(self):
        names = ("source_root", "repo_root", "artifact", "private_manifest", "evidence_root", "public_output")
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            target = root / "system-python"
            target.write_text("")
            link = root / "venv-python"
            link.symlink_to(target)
            args = argparse.Namespace(tau2_python=str(link), **{name: directory for name in names})
            resolved = preflight.resolve_paths(args)
            self.assertEqual(resolved.tau2_python, str(link.absolute()))


if __name__ == "__main__":
    unittest.main()
