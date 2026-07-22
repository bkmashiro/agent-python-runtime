import importlib.util
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "patch_wasi_vfs_storage", ROOT / "tools" / "patch_wasi_vfs_storage.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class WasiVFSStoragePatchTests(unittest.TestCase):
    def test_zero_initializes_all_linked_storage_allocations(self):
        source = "\n".join(
            [
                "malloc(sizeof(struct wasi_vfs_node))",
                "malloc(sizeof(struct wasi_vfs_link))",
                "malloc(sizeof(struct wasi_vfs_dirent))",
                "malloc(sizeof(struct wasi_vfs_embed_linked_storage))",
            ]
        )
        patched = MODULE.patch_source(source)
        self.assertEqual(4, patched.count("calloc(1, sizeof("))
        self.assertNotIn("malloc(sizeof(struct wasi_vfs_", patched)

    def test_fails_closed_on_upstream_shape_drift_or_already_patched_input(self):
        with self.assertRaisesRegex(ValueError, "expected exactly one"):
            MODULE.patch_source("malloc(sizeof(struct wasi_vfs_node))")
        complete = "\n".join(
            [
                "calloc(1, sizeof(struct wasi_vfs_node))",
                "malloc(sizeof(struct wasi_vfs_link))",
                "malloc(sizeof(struct wasi_vfs_dirent))",
                "malloc(sizeof(struct wasi_vfs_embed_linked_storage))",
            ]
        )
        with self.assertRaisesRegex(ValueError, "expected exactly one"):
            MODULE.patch_source(complete)


if __name__ == "__main__":
    unittest.main()
