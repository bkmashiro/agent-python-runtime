import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]


class EngineBoundaryTests(unittest.TestCase):
    def test_neutral_engine_package_does_not_import_wazero(self):
        neutral_sources = "\n".join(
            path.read_text()
            for path in sorted((ROOT / "runtime" / "engine").glob("*.go"))
        )
        self.assertNotIn("github.com/tetratelabs/wazero", neutral_sources)
        self.assertNotIn("runtime/engine/wazero", neutral_sources)

    def test_wazero_is_a_child_adapter(self):
        adapter = (ROOT / "runtime" / "engine" / "wazero" / "engine.go").read_text()
        self.assertIn("enginecontract.Factory", adapter)
        self.assertIn("enginecontract.Runner", adapter)
        self.assertIn("ResetModeFreshInstance", adapter)

    def test_e2e_depends_on_runner_contract(self):
        e2e = (ROOT / "integration" / "e2e" / "core_guest_test.go").read_text()
        self.assertIn("engine.Runner", e2e)
        self.assertIn("wazeroengine.Factory", e2e)
        self.assertNotIn("*engine.Engine", e2e)


if __name__ == "__main__":
    unittest.main()
