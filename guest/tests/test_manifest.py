import importlib.util
import json
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
WRITER_PATH = ROOT / "guest" / "build" / "write-manifest.py"


def load_writer():
    spec = importlib.util.spec_from_file_location("write_manifest", WRITER_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load manifest writer")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ManifestWriterTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.writer = load_writer()

    def test_manifest_binds_artifact_sources_and_wasm_contract(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            artifact = root / "guest.wasm"
            artifact.write_bytes(b"\x00asm\x01\x00\x00\x00")
            wat = root / "guest.wat"
            wat.write_text(
                '(module\n  (import "wasi_snapshot_preview1" "fd_write" (func))\n'
                '  (export "memory" (memory 0))\n'
                '  (export "runtime_init" (func 1))\n)\n'
            )
            lock = root / "sources.lock.json"
            lock.write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "target": "wasm32-wasip1",
                        "sources": [{"id": "source", "sha256": "a" * 64}],
                    }
                )
            )

            manifest = self.writer.build_manifest(
                artifact=artifact,
                wat=wat,
                source_lock=lock,
                commit="abc123",
                source_date_epoch="1234567890",
            )

            self.assertEqual(1, manifest["schema_version"])
            self.assertEqual("v1", manifest["abi_version"])
            self.assertEqual(8, manifest["artifact"]["size"])
            self.assertEqual(
                "93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476",
                manifest["artifact"]["sha256"],
            )
            self.assertEqual("abc123", manifest["build"]["repository_commit"])
            self.assertEqual("1234567890", manifest["build"]["source_date_epoch"])
            self.assertEqual("wasm32-wasip1", manifest["target"])
            self.assertEqual([{"id": "source", "sha256": "a" * 64}], manifest["sources"])
            self.assertEqual(
                [{"module": "wasi_snapshot_preview1", "name": "fd_write"}],
                manifest["wasm"]["imports"],
            )
            self.assertEqual(["memory", "runtime_init"], manifest["wasm"]["exports"])
            limitations = "\n".join(manifest["limitations"])
            self.assertNotIn("capabilities are not implemented", limitations)
            self.assertIn("fetch_many", limitations)


if __name__ == "__main__":
    unittest.main()
