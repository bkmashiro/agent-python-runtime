import importlib.util
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "patch_wasi_vfs_dirent_padding.py"


def load_module():
    spec = importlib.util.spec_from_file_location("patch_wasi_vfs_dirent_padding", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


class WasiVFSDirentPaddingPatchTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_module()

    def upstream_source(self):
        return """        let data = &buffer[offset..capacity];
        let dirent_size = core::mem::size_of::<wasi::Dirent>();

        // when dirent is truncated, re-read it
        if data.len() < dirent_size {
            offset = capacity;
            continue;
        }

        let (dirent, data) = data.split_at(dirent_size);
        let dirent = unsafe { core::ptr::read_unaligned(dirent.as_ptr() as *const wasi::Dirent) };
"""

    def patch(self, source_text):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "lib.rs"
            output = pathlib.Path(directory) / "lib.rs.patched"
            source.write_text(source_text)
            self.module.patch_source(source, output)
            return output.read_text()

    def test_zeroes_only_dirent_tail_padding_with_volatile_writes(self):
        patched = self.patch(self.upstream_source())
        self.assertIn("const DIRENT_FIELD_BYTES: usize", patched)
        self.assertIn("let data = &mut buffer[offset..capacity];", patched)
        self.assertIn("for byte in &mut data[DIRENT_FIELD_BYTES..dirent_size]", patched)
        self.assertIn("core::ptr::write_volatile(byte, 0)", patched)
        self.assertIn("assert_eq!(DIRENT_FIELD_BYTES, 21);", patched)
        self.assertIn("assert_eq!(dirent_size, 24);", patched)
        self.assertIn("core::ptr::read_unaligned", patched)
        self.assertNotIn("capacity - offset", patched)
        self.assertNotIn("as_mut_ptr", patched)
        self.assertNotIn("sort", patched)
        self.assertNotIn("d_ino =", patched)

    def test_fails_closed_on_shape_drift_or_repatch(self):
        cases = (
            self.upstream_source().replace("data.len() < dirent_size", "data.len() <= dirent_size"),
            self.upstream_source().replace("read_unaligned", "read"),
            self.patch(self.upstream_source()),
        )
        for source_text in cases:
            with self.subTest(source_text=source_text[:60]):
                with tempfile.TemporaryDirectory() as directory:
                    source = pathlib.Path(directory) / "lib.rs"
                    output = pathlib.Path(directory) / "lib.rs.patched"
                    source.write_text(source_text)
                    with self.assertRaisesRegex(ValueError, "exact unpatched dirent parsing shape"):
                        self.module.patch_source(source, output)


if __name__ == "__main__":
    unittest.main()
