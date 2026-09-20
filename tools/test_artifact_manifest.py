import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("write-artifact-manifest.py")
SPEC = importlib.util.spec_from_file_location("write_artifact_manifest", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cannot load write-artifact-manifest.py")
manifest_tool = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(manifest_tool)


class ArtifactManifestTests(unittest.TestCase):
    def test_manifest_and_tree_digest_are_path_independent(self):
        outputs = []
        for index in range(2):
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                profile = root / "profile.json"
                inputs = root / "inputs.json"
                native = root / "native.json"
                raw = root / "raw.wasm"
                tree = root / "tree"
                artifact = root / "pysolate.wasm"
                profile.write_text(json.dumps({
                    "profile": "agent-core",
                    "target": "wasm32-wasip1",
                    "runtime": {"cpython": "3.14.0", "numpy": "1.26.0b1"},
                    "policy": {"guest_network": False},
                }), encoding="utf-8")
                inputs.write_text("{}", encoding="utf-8")
                native.write_text(json.dumps({"native_modules": [{"name": "numpy.core"}]}), encoding="utf-8")
                raw.write_bytes(b"raw")
                artifact.write_bytes(b"packed")
                (tree / "nested").mkdir(parents=True)
                (tree / "nested" / "module.py").write_text("value=42\n", encoding="utf-8")
                outputs.append(manifest_tool.build_manifest(profile, inputs, native, raw, tree, artifact))
        self.assertEqual(outputs[0], outputs[1])
        self.assertEqual(outputs[0]["artifact"]["bytes"], 6)
        self.assertEqual(outputs[0]["native_modules"], ["numpy.core"])

    def test_tree_digest_changes_with_path_or_content(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "a").write_text("one", encoding="utf-8")
            first = manifest_tool.tree_sha256(root)
            (root / "a").write_text("two", encoding="utf-8")
            second = manifest_tool.tree_sha256(root)
            (root / "a").rename(root / "b")
            third = manifest_tool.tree_sha256(root)
        self.assertEqual(len({first, second, third}), 3)


if __name__ == "__main__":
    unittest.main()
