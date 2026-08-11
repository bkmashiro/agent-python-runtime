import importlib.util
import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
VERIFIER_PATH = ROOT / "tools" / "verify_sources_lock.py"
LOCK_PATH = ROOT / "guest" / "build" / "sources.lock.json"



def load_verifier():
    spec = importlib.util.spec_from_file_location("verify_sources_lock", VERIFIER_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load source lock verifier")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class SourcesLockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.verifier = load_verifier()

    def valid_lock(self):
        return {
            "schema_version": 1,
            "target": "wasm32-wasip1",
            "sources": [
                {
                    "id": "example",
                    "version": "v1.2.3",
                    "url": "https://example.com/releases/download/v1.2.3/source.tar.gz",
                    "sha256": "a" * 64,
                    "license": "Apache-2.0",
                    "role": "build-tool",
                    "artifact_relation": "build-only",
                }
            ],
        }

    def assert_invalid(self, lock, expected_fragment):
        errors = self.verifier.validate_lock(lock)
        self.assertTrue(errors)
        self.assertTrue(
            any(expected_fragment in error for error in errors),
            f"expected {expected_fragment!r} in {errors!r}",
        )

    def test_repository_lock_is_valid(self):
        lock = json.loads(LOCK_PATH.read_text())
        self.assertEqual([], self.verifier.validate_lock(lock))
        self.assertNotIn("numpy-source", {row["id"] for row in lock["sources"]})


    def test_rejects_missing_sha256(self):
        lock = self.valid_lock()
        del lock["sources"][0]["sha256"]
        self.assert_invalid(lock, "sha256")

    def test_rejects_mutable_latest_url(self):
        lock = self.valid_lock()
        lock["sources"][0]["url"] = "https://example.com/releases/download/latest/source.tar.gz"
        self.assert_invalid(lock, "mutable")

    def test_rejects_missing_license(self):
        lock = self.valid_lock()
        lock["sources"][0]["license"] = ""
        self.assert_invalid(lock, "license")

    def test_rejects_missing_or_unknown_artifact_relation(self):
        lock = self.valid_lock()
        del lock["sources"][0]["artifact_relation"]
        self.assert_invalid(lock, "artifact_relation")
        lock = self.valid_lock()
        lock["sources"][0]["artifact_relation"] = "maybe"
        self.assert_invalid(lock, "artifact_relation")

    def test_rejects_duplicate_source_ids(self):
        lock = self.valid_lock()
        lock["sources"].append(dict(lock["sources"][0]))
        self.assert_invalid(lock, "duplicate")

    def test_rejects_wrong_target(self):
        lock = self.valid_lock()
        lock["target"] = "wasm32-wasi"
        self.assert_invalid(lock, "target")


if __name__ == "__main__":
    unittest.main()
