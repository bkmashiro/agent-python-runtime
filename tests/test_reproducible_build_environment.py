import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "guest" / "build" / "build-guest.sh"


class ReproducibleBuildEnvironmentTests(unittest.TestCase):
    def test_python_hash_seed_is_fixed_before_cpython_build(self):
        source = SCRIPT.read_text()
        export = "export PYTHONHASHSEED=0"
        build = 'python3 Tools/wasm/wasi build --wasi-sdk "${WASI_SDK_PATH}"'
        self.assertIn(export, source)
        self.assertIn(build, source)
        self.assertLess(source.index(export), source.index(build))


if __name__ == "__main__":
    unittest.main()
