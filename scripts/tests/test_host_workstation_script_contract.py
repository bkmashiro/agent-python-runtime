import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
WRAPPER = ROOT / "scripts" / "test-host-workstation.sh"
WORKER = ROOT / "scripts" / "internal" / "test-host-workstation-worker.sh"
VERIFIER = ROOT / "scripts" / "verify-workstation-host-test.py"


def load_verifier():
    spec = importlib.util.spec_from_file_location("verify_workstation_host_test", VERIFIER)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class HostWorkstationScriptContractTests(unittest.TestCase):
    def test_wrapper_is_clean_head_and_enum_only(self) -> None:
        text = WRAPPER.read_text()
        self.assertIn('case "$suite" in baseline|prepared-family)', text)
        self.assertIn("git status --porcelain", text)
        self.assertIn("git archive --format=tar HEAD", text)
        self.assertIn('case "$gateway" in shell2|shell3)', text)
        self.assertNotIn("eval ", text)
        self.assertNotIn("bash -c", text)

    def test_worker_is_gpu31_bounded_and_uses_shared_go(self) -> None:
        text = WORKER.read_text()
        self.assertIn("gpu31.doc.ic.ac.uk", text)
        self.assertIn("/vol/bitbucket/ys25/pysolate/toolchains/go", text)
        self.assertIn('case "$suite" in', text)
        self.assertIn("./runtime/prepareddataset", text)
        self.assertIn("./runtime/engine/...", text)
        self.assertNotIn("eval ", text)
        self.assertNotIn("$command", text)

    def test_verifier_accepts_exact_success_bundle_and_rejects_extra_file(self) -> None:
        verifier = load_verifier()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            commit = "a" * 40
            tree = "b" * 40
            result = {
                "schema_version": "pysolate.workstation-host-test.v1",
                "source_commit": commit,
                "source_tree": tree,
                "builder": "gpu31.doc.ic.ac.uk",
                "target": "linux/amd64",
                "suite": "baseline",
                "passed": True,
                "go_version": "go1.25.0",
                "duration_millis": 1,
            }
            (root / "RESULT.READY").write_text(json.dumps(result, sort_keys=True, separators=(",", ":")) + "\n")
            (root / "test.log").write_text("ok\n")
            verifier.write_checksums(root)
            verified = verifier.verify(root, commit, tree, "baseline")
            self.assertTrue(verified["passed"])
            (root / "extra").write_text("not covered\n")
            with self.assertRaisesRegex(ValueError, "exact evidence file set"):
                verifier.verify(root, commit, tree, "baseline")


if __name__ == "__main__":
    unittest.main()
