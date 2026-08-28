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
        self.assertIn('case "$suite" in baseline|prepared-family|evaluation|evaluation-sweeps)', text)
        self.assertIn("git status --porcelain", text)
        self.assertIn("git archive --format=tar HEAD", text)
        self.assertIn('case "$gateway" in shell2|shell3)', text)
        self.assertNotIn("eval ", text)
        self.assertNotIn("bash -c", text)

    def test_worker_is_target_bounded_and_uses_shared_go(self) -> None:
        text = WORKER.read_text()
        self.assertIn("gpu31|gpu32|gpu33|gpu34|gpu35", text)
        self.assertIn("expected_hostname", text)
        self.assertIn("toolchains/go", text)
        self.assertIn('case "$suite" in', text)
        self.assertIn("./runtime/prepareddataset", text)
        self.assertIn("./runtime/engine/...", text)
        self.assertIn("run-linux-evaluation-suite.sh", text)
        self.assertIn("./scripts/run-linux-evaluation-suite.sh", text)
        self.assertNotIn('$stage/scripts/run-linux-evaluation-suite.sh', text)
        self.assertIn("rglob(\"*\")", text)
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

    def test_verifier_accepts_evaluation_bundle_without_acceptance_report(self) -> None:
        verifier = load_verifier()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            commit, tree = "a" * 40, "b" * 40
            result = {
                "schema_version": "pysolate.workstation-host-test.v2",
                "acceptance_report": False,
                "source_commit": commit,
                "source_tree": tree,
                "builder": "gpu31.doc.ic.ac.uk",
                "target": "linux/amd64",
                "suite": "evaluation-sweeps",
                "passed": True,
                "go_version": "go1.25.0",
                "duration_millis": 1,
            }
            (root / "RESULT.READY").write_text(json.dumps(result, sort_keys=True, separators=(",", ":")) + "\n")
            (root / "test.log").write_text("ok\n")
            evidence = root / "evaluation-sweeps"
            evidence.mkdir()
            (evidence / "suite-manifest.json").write_text("{}\n")
            verifier.write_checksums(root)
            verified = verifier.verify(root, commit, tree, "evaluation-sweeps")
            self.assertTrue(verified["passed"])

    def test_verifier_accepts_prepared_family_report_and_rejects_source_drift(self) -> None:
        verifier = load_verifier()
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            commit, tree = "a" * 40, "b" * 40
            digest = lambda character: "sha256:" + character * 64
            result = {
                "schema_version": "pysolate.workstation-host-test.v2",
                "acceptance_report": True,
                "source_commit": commit,
                "source_tree": tree,
                "builder": "gpu31.doc.ic.ac.uk",
                "target": "linux/amd64",
                "suite": "prepared-family",
                "passed": True,
                "go_version": "go1.25.0",
                "duration_millis": 1,
            }
            member = {
                "schema_version": "pysolate.prepared-family-member.v1",
                "family_sha256": digest("f"), "input_sha256": digest("b"), "member_id": 1,
                "run_id": "child", "agent_run_id": "parent", "turn_seq": 0,
                "output_item_seq": 0, "segment_seq": 0, "invocation_id": "invocation",
                "invocation_attempt": 1, "execution_id": "child", "plan_sha256": digest("c"),
                "grants_sha256": digest("d"), "physical_disposition": "private_copy",
                "outcome": "ok", "final_workspace_sha256": digest("e"),
            }
            report = {
                "schema_version": "pysolate.prepared-family-acceptance.v1",
                "source_commit": commit, "source_tree": tree, "artifact_sha256": digest("a"),
                "execution_profile_sha256": digest("1"), "input_sha256": digest("b"),
                "family_sha256": digest("f"), "physical_disposition": "private_copy",
                "created": 1, "terminal": 1, "selected_root_sha256": digest("e"), "members": [member],
            }
            (root / "RESULT.READY").write_text(json.dumps(result, sort_keys=True, separators=(",", ":")) + "\n")
            (root / "test.log").write_text("ok\n")
            (root / "acceptance-report.json").write_text(json.dumps(report, sort_keys=True, separators=(",", ":")) + "\n")
            verifier.write_checksums(root)
            verifier.verify(root, commit, tree, "prepared-family")
            report["source_tree"] = "c" * 40
            (root / "acceptance-report.json").write_text(json.dumps(report, sort_keys=True, separators=(",", ":")) + "\n")
            verifier.write_checksums(root)
            with self.assertRaisesRegex(ValueError, "report source mismatch"):
                verifier.verify(root, commit, tree, "prepared-family")


if __name__ == "__main__":
    unittest.main()
