import importlib.util
import json
import pathlib
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).parents[1] / "attrs-770-spike.py"
SPEC = importlib.util.spec_from_file_location("attrs_770_spike", SCRIPT)
assert SPEC is not None
spike = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(spike)


class Attrs770SpikeTests(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.directory.name)
        for name, body in {
            "artifact.wasm": b"wasm",
            "manifest.json": b"{}",
            "runner": b"runner",
            "model.patch": b"patch",
            "oracle_runner.py": b"oracle",
            "guest-source.py": b"guest",
        }.items():
            (self.root / name).write_bytes(body)
        private = {
            "schema_version": "pysolate.attrs-770-spike-private.v1",
            "instance_id": "python-attrs__attrs-770",
            "repo": "python-attrs/attrs",
            "base_commit": "58d2adce57f2c4e447eb12b892ebbb09cccbdcc3",
            "dataset_revision": "ad4805a5aa7de70d99cab0bb8f99b15304c76de0",
            "dataset_license_id": "cc-by-4.0",
            "repository_license_id": "mit",
            "raw_source_sha256": spike.digest_bytes(b"source"),
            "corpus_manifest_identity": spike.digest_bytes(b"corpus"),
            "corpus_item_id": spike.digest_bytes(b"item"),
            "source_record_sha256": spike.digest_bytes(b"record"),
            "trajectory_source_sha256": spike.digest_bytes(b"trajectory"),
            "resolved": 1,
            "fail_to_pass": 1,
            "pass_to_pass": 237,
            "model_patch_sha256": spike.digest_bytes(b"patch"),
            "oracle_sha256": spike.digest_bytes(b"oracle"),
        }
        (self.root / "private-manifest.json").write_text(json.dumps(private))
        (self.root / "exit-codes.json").write_text(json.dumps({
            "native": {"base": 1, "patched": 0},
            "guest_unbound": {"base": 0, "patched": 0},
            "guest_bound_requested_package": {"base": 2, "patched": 2},
            "guest_bound_source_reject": {"base": 2, "patched": 2},
        }))
        (self.root / "native-base.json").write_text(json.dumps({
            "oracle": "failed", "error_type": "TypeError",
            "error_message": "type() doesn't support MRO entry resolution; use types.new_class()",
        }))
        (self.root / "native-patched.json").write_text(json.dumps({
            "oracle": "passed", "module": "__main__", "name": "test",
        }))
        self._guest("base", "error", None, "TypeError", "sha256:" + "a" * 64, 100)
        self._guest("patched", "ok", {"oracle": "passed", "module": "__main__", "name": "test"}, None,
                    "sha256:" + "b" * 64, 101)
        for lane in ("base", "patched"):
            (self.root / f"guest-bound-{lane}.json").write_text("")
            (self.root / f"guest-bound-{lane}.stderr").write_text("execution profile artifact mismatch\n")
            (self.root / f"guest-bound-source-reject-{lane}.json").write_text("")
            (self.root / f"guest-bound-source-reject-{lane}.stderr").write_text(
                "execution profile source comparison failed\n"
            )

    def tearDown(self) -> None:
        self.directory.cleanup()

    def _guest(self, lane, status, result, error_type, workspace_sha, total_bytes):
        payload = {
            "status": status,
            "result": result,
            "receipts": [],
            "metrics": {"capability_calls": 0, "result_bytes": 0},
            "error": None if error_type is None else {"code": "python_exception", "error_type": error_type},
            "workspace_receipt": {
                "policy": "discard", "disposition": "discarded",
                "initial_workspace_sha256": workspace_sha,
                "final_workspace_sha256": workspace_sha,
                "final_tree_sha256": "sha256:" + "c" * 64,
                "entry_count": 21, "total_bytes": total_bytes,
            },
        }
        (self.root / f"guest-unbound-{lane}.json").write_text(json.dumps(payload))

    def build(self):
        return spike.build_report(
            self.root, self.root / "artifact.wasm", self.root / "manifest.json", self.root / "runner",
            "388ef3291a1586a3b02cf4b5f0c31c6407be152f",
        )

    def test_report_preserves_red_green_and_fail_closed_boundary(self) -> None:
        report = self.build()
        self.assertEqual("validated", report["runtime_feasibility"]["verdict"])
        self.assertEqual("unsupported_fail_closed", report["verified_profile_admission"]["verdict"])
        self.assertFalse(report["verified_profile_admission"]["physical_guest_started"])
        self.assertEqual("expected_failure", report["native_oracle"]["base"])
        self.assertEqual("passed", report["guest_oracle"]["patched"])
        self.assertEqual("mit", report["instance"]["repository_license_id"])
        self.assertEqual("cc-by-4.0", report["instance"]["dataset_license_id"])

    def test_report_rejects_workspace_mutation(self) -> None:
        path = self.root / "guest-unbound-patched.json"
        payload = json.loads(path.read_text())
        payload["workspace_receipt"]["final_workspace_sha256"] = "sha256:" + "d" * 64
        path.write_text(json.dumps(payload))
        with self.assertRaisesRegex(ValueError, "workspace changed"):
            self.build()

    def test_report_rejects_exit_code_reclassification(self) -> None:
        path = self.root / "exit-codes.json"
        payload = json.loads(path.read_text())
        payload["guest_unbound"]["base"] = 2
        path.write_text(json.dumps(payload))
        with self.assertRaisesRegex(ValueError, "exit-code evidence"):
            self.build()

    def test_report_is_body_and_path_safe(self) -> None:
        encoded = json.dumps(self.build(), sort_keys=True)
        for marker in ("/Users/", "~/.hermes", "type() doesn't", "diff --git", "traceback", "workspace-"):
            self.assertNotIn(marker, encoded)


if __name__ == "__main__":
    unittest.main()
