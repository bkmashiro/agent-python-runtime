import importlib.util
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "patch_wasi_vfs_pack_file_size.py"


def load_module():
    spec = importlib.util.spec_from_file_location("patch_wasi_vfs_pack_file_size", MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load pack file-size patch module")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class WasiVFSPackFileSizePatchTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_module()

    def upstream_source(self):
        return """            wasi::FILETYPE_REGULAR_FILE => {
                let oflags = 0;
                let child_fd = unsafe {
                    wasi::path_open(
                        fd,
                        wasi::LOOKUPFLAGS_SYMLINK_FOLLOW,
                        &name,
                        oflags,
                        rights,
                        rights,
                        0,
                    )
                }
                .map_err(|e| e.raw())
                .unwrap();
            }

    fn visit_file(
        &mut self,
        path: &str,
        fd: u32,
        preopened_id: (S::NodeId, S::LinkId),
    ) -> Result<(), u16> {
        let stat = unsafe { wasi::fd_filestat_get(fd) }
            .map_err(|e| e.raw())
            .unwrap();
        if stat.size >= u32::MAX as u64 {
            if self.verbose {
                trace::print(format!("too large file: {} (size {})\\n", path, stat.size));
            }
            return Ok(());
        }
        let mut buf = vec![0; stat.size as usize];
        let mut offset = 0;
        loop {
            let read = read(fd, &mut buf[offset..]);
            offset += read;
            if offset == stat.size as usize {
                break;
            }
        }
        self.fs.create_file(preopened_id.0, preopened_id.1, path, buf).unwrap();
        Ok(())
    }
"""

    def patch(self, text):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source = root / "lib.rs"
            output = root / "patched.rs"
            source.write_text(text)
            self.module.patch_source(source, output)
            return output.read_text()

    def test_uses_seek_for_size_and_resets_position(self):
        patched = self.patch(self.upstream_source())
        self.assertNotIn("fd_filestat_get", patched)
        self.assertIn("wasi::fd_seek(fd, 0, wasi::WHENCE_END)", patched)
        self.assertIn("wasi::fd_seek(fd, 0, wasi::WHENCE_SET)", patched)
        self.assertIn("let file_rights = rights | wasi::RIGHTS_FD_SEEK;", patched)
        self.assertEqual(patched.count("file_rights"), 3)
        self.assertEqual(patched.count("size as usize"), 2)
        self.assertNotIn("stat.size", patched)

    def test_fails_closed_on_shape_drift_or_repatch(self):
        cases = (
            self.upstream_source().replace("stat.size >=", "stat.size >"),
            self.upstream_source().replace(
                "                        rights,\n                        rights,",
                "                        rights,\n                        wasi::RIGHTS_FD_TELL,",
            ),
            self.patch(self.upstream_source()),
        )
        for source_text in cases:
            with self.subTest(source=source_text):
                with tempfile.TemporaryDirectory() as temp:
                    root = pathlib.Path(temp)
                    source = root / "lib.rs"
                    output = root / "patched.rs"
                    source.write_text(source_text)
                    with self.assertRaisesRegex(ValueError, "expected exact unpatched pack file-size shape"):
                        self.module.patch_source(source, output)
                    self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
