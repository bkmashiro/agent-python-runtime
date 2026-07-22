import importlib.util
import json
import os
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "archive_wasm_extension", ROOT / "tools" / "archive_wasm_extension.py"
)
assert SPEC is not None
assert SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ArchiveWasmExtensionTests(unittest.TestCase):
    def test_passes_non_shared_invocations_through(self):
        self.assertIsNone(
            MODULE.plan_shared_archive(
                ["-c", "source.c", "-o", "source.c.o"], Path("/build")
            )
        )

    def test_plans_deterministic_archive_and_manifest_inputs(self):
        plan = MODULE.plan_shared_archive(
            [
                "-o",
                "numpy/_core/_multiarray_umath.cpython-314-wasm32-wasi.so",
                "numpy/_core/core.o",
                "numpy/_core/libnpymath.a",
                "-shared",
                "-fPIC",
                "-Wl,--allow-shlib-undefined",
            ],
            Path("/build"),
        )

        self.assertEqual(
            plan.output,
            Path("/build/numpy/_core/_multiarray_umath.cpython-314-wasm32-wasi.so"),
        )
        self.assertEqual(plan.archive, Path("/build/numpy/_core/_multiarray_umath.a"))
        self.assertEqual(plan.objects, (Path("/build/numpy/_core/core.o"),))
        self.assertEqual(plan.static_inputs, (Path("/build/numpy/_core/libnpymath.a"),))
        self.assertEqual(
            plan.link_args,
            ("-shared", "-fPIC", "-Wl,--allow-shlib-undefined"),
        )

    def test_rejects_shared_extension_without_direct_objects(self):
        with self.assertRaisesRegex(ValueError, "no direct object inputs"):
            MODULE.plan_shared_archive(
                ["-o", "numpy/_core/broken.cpython-314-wasm32-wasi.so", "-shared"],
                Path("/build"),
            )

    def test_executes_archive_and_writes_manifest(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            obj = root / "build/numpy/_core/example.o"
            obj.parent.mkdir(parents=True)
            obj.write_bytes(b"object fixture")
            ar_shim = root / "ar-shim"
            ar_shim.write_text("#!/bin/sh\n[ \"$1\" = rcsD ] || exit 9\nshift\nexec /usr/bin/ar rcs \"$@\"\n")
            os.chmod(ar_shim, 0o755)
            plan = MODULE.plan_shared_archive(
                [
                    "-o",
                    "numpy/_core/example.cpython-314-wasm32-wasi.so",
                    "numpy/_core/example.o",
                    "-shared",
                ],
                root / "build",
            )

            manifest_path = MODULE.execute_archive(
                plan,
                ar_shim,
                root / "manifests",
                root / "build",
                Path("/usr/bin/cc"),
            )

            self.assertEqual(plan.archive.read_bytes()[:8], b"!<arch>\n")
            self.assertEqual(plan.output.read_bytes(), plan.archive.read_bytes())
            manifest = json.loads(manifest_path.read_text())
            self.assertEqual(manifest["archive"], "numpy/_core/example.a")
            self.assertEqual(manifest["objects"], ["numpy/_core/example.o"])

    def test_manifest_path_preserves_target_identity_without_parent_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            plan = MODULE.plan_shared_archive(
                ["-o", "numpy/random/_generator.cpython-314-wasm32-wasi.so", "x.o", "-shared"],
                root,
            )
            manifest = MODULE.manifest_path_for(plan, root / "manifests", root)
            self.assertEqual(
                manifest, (root / "manifests/numpy/random/_generator.json").resolve()
            )

            with self.assertRaisesRegex(ValueError, "outside build root"):
                MODULE.manifest_path_for(plan, root / "manifests", root / "other")


if __name__ == "__main__":
    unittest.main()
