import importlib.util
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "patch_wasi_vfs_deterministic_hasher.py"


def load_module():
    spec = importlib.util.spec_from_file_location("patch_wasi_vfs_deterministic_hasher", MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load deterministic hasher patch module")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class WasiVFSDeterministicHasherPatchTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_module()

    def upstream_source(self):
        return """use crate::Vfd;
use std::{collections::HashMap, path::Path};

pub(crate) struct EmbeddedFs<S: Storage> {
    opens: HashMap<Vfd, FdEntry<S>>,
}

impl<S: Storage> EmbeddedFs<S> {
    pub(crate) fn new(storage: S) -> Self {
        Self {
            opens: HashMap::new(),
        }
    }
}
"""

    def test_replaces_only_embedded_fs_hasher(self):
        patched = self.cls_patch(self.upstream_source())
        self.assertIn("hash::{BuildHasherDefault, DefaultHasher}", patched)
        self.assertIn(
            "opens: HashMap<Vfd, FdEntry<S>, BuildHasherDefault<DefaultHasher>>",
            patched,
        )
        self.assertIn(
            "opens: HashMap::with_hasher(BuildHasherDefault::default())",
            patched,
        )
        self.assertNotIn("HashMap::new()", patched)

    def cls_patch(self, text):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source = root / "mod.rs"
            output = root / "patched.rs"
            source.write_text(text)
            self.module.patch_source(source, output)
            return output.read_text()

    def test_fails_closed_on_shape_drift_or_repatch(self):
        for source_text in (
            self.upstream_source().replace("HashMap::new()", "HashMap::default()"),
            self.cls_patch(self.upstream_source()),
        ):
            with self.subTest(source=source_text):
                with tempfile.TemporaryDirectory() as temp:
                    root = pathlib.Path(temp)
                    source = root / "mod.rs"
                    output = root / "patched.rs"
                    source.write_text(source_text)
                    with self.assertRaisesRegex(ValueError, "expected exactly one unpatched"):
                        self.module.patch_source(source, output)
                    self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
