import importlib.util
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "patch_wasi_vfs_deterministic_pack_clock.py"


def load_module():
    spec = importlib.util.spec_from_file_location("patch_wasi_vfs_deterministic_pack_clock", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


class WasiVFSDeterministicPackClockPatchTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_module()

    def upstream_source(self):
        return """use std::path::PathBuf;

use anyhow::Result;
""" + """    let mut wasi = wasmtime_wasi::WasiCtxBuilder::new();
    wasi.inherit_stdio();
    wasi.env("__WASI_VFS_PACKING", "1");
"""

    def patch(self, source_text):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "lib.rs"
            output = pathlib.Path(directory) / "lib.rs.patched"
            source.write_text(source_text)
            self.module.patch_source(source, output)
            return output.read_text()

    def test_injects_only_fixed_pack_monotonic_clock(self):
        patched = self.patch(self.upstream_source())
        self.assertEqual(patched.count("DeterministicPackMonotonicClock"), 3)
        self.assertIn("impl wasmtime_wasi::clocks::HostMonotonicClock", patched)
        self.assertIn("fn resolution(&self) -> u64 {\n        1\n", patched)
        self.assertIn("fn now(&self) -> u64 {\n        0\n", patched)
        self.assertIn("wasi.monotonic_clock(DeterministicPackMonotonicClock);", patched)
        self.assertNotIn("wall_clock(", patched)
        self.assertNotIn("secure_random(", patched)

    def test_fails_closed_on_shape_drift_or_repatch(self):
        cases = (
            self.upstream_source().replace("use anyhow::Result;", "use anyhow::{Context, Result};"),
            self.upstream_source().replace("wasi.inherit_stdio();", "wasi.inherit_stdout();"),
            self.patch(self.upstream_source()),
        )
        for source_text in cases:
            with self.subTest(source_text=source_text[:80]):
                with tempfile.TemporaryDirectory() as directory:
                    source = pathlib.Path(directory) / "lib.rs"
                    output = pathlib.Path(directory) / "lib.rs.patched"
                    source.write_text(source_text)
                    with self.assertRaisesRegex(ValueError, "exact unpatched pack clock shapes"):
                        self.module.patch_source(source, output)


if __name__ == "__main__":
    unittest.main()
