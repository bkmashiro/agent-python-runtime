import importlib.util
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "patch_wasi_vfs_dirent_buffer_cleanup.py"


def load_module():
    spec = importlib.util.spec_from_file_location("patch_wasi_vfs_dirent_buffer_cleanup", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


class WasiVFSDirentBufferCleanupPatchTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_module()

    def upstream_source(self):
        return """            _ => {}
        }
    }
    Ok(())
}
"""

    def patch(self, source_text):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "lib.rs"
            output = pathlib.Path(directory) / "lib.rs.patched"
            source.write_text(source_text)
            self.module.patch_source(source, output)
            return output.read_text()

    def test_zeroes_entire_scratch_buffer_before_drop(self):
        patched = self.patch(self.upstream_source())
        self.assertIn("for byte in &mut buffer", patched)
        self.assertIn("core::ptr::write_volatile(byte, 0)", patched)
        self.assertLess(patched.index("for byte in &mut buffer"), patched.index("Ok(())"))
        self.assertNotIn("buffer.fill", patched)
        self.assertNotIn("sort", patched)

    def test_fails_closed_on_shape_drift_or_repatch(self):
        cases = (
            self.upstream_source().replace("_ => {}", "_ => continue"),
            self.upstream_source().replace("Ok(())", "return Ok(())"),
            self.patch(self.upstream_source()),
        )
        for source_text in cases:
            with self.subTest(source_text=source_text[:60]):
                with tempfile.TemporaryDirectory() as directory:
                    source = pathlib.Path(directory) / "lib.rs"
                    output = pathlib.Path(directory) / "lib.rs.patched"
                    source.write_text(source_text)
                    with self.assertRaisesRegex(ValueError, "exact unpatched walk_dir tail"):
                        self.module.patch_source(source, output)


if __name__ == "__main__":
    unittest.main()
