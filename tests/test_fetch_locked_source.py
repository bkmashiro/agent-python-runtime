import importlib.util
import json
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
FETCHER_PATH = ROOT / "tools" / "fetch_locked_source.py"
LOCK_PATH = ROOT / "guest" / "build" / "sources.lock.json"


def load_fetcher():
    spec = importlib.util.spec_from_file_location("fetch_locked_source", FETCHER_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load locked source fetcher")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FetchLockedSourceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.fetcher = load_fetcher()

    def test_finds_source_by_exact_id(self):
        lock = json.loads(LOCK_PATH.read_text())
        source = self.fetcher.find_source(lock, "cpython-source")
        self.assertEqual("3.14.0", source["version"])

    def test_rejects_unknown_source_id(self):
        lock = json.loads(LOCK_PATH.read_text())
        with self.assertRaisesRegex(KeyError, "unknown source"):
            self.fetcher.find_source(lock, "missing")

    def test_verifies_matching_file_digest(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "source.bin"
            path.write_bytes(b"locked source")
            digest = "ef26046653e56534e53272e82991cef362dbeaa46153d6aaca81ee3f47de7bc1"
            self.fetcher.verify_digest(path, digest)

    def test_rejects_mismatched_file_digest(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "source.bin"
            path.write_bytes(b"tampered")
            with self.assertRaisesRegex(ValueError, "digest mismatch"):
                self.fetcher.verify_digest(path, "0" * 64)


if __name__ == "__main__":
    unittest.main()
