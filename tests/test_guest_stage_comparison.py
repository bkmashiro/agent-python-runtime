import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
WRITER_SPEC = importlib.util.spec_from_file_location(
    "write_guest_stage_evidence", ROOT / "tools" / "write_guest_stage_evidence.py"
)
WRITER = importlib.util.module_from_spec(WRITER_SPEC)
WRITER_SPEC.loader.exec_module(WRITER)
COMPARE_SPEC = importlib.util.spec_from_file_location(
    "compare_guest_stage_evidence", ROOT / "tools" / "compare_guest_stage_evidence.py"
)
COMPARE = importlib.util.module_from_spec(COMPARE_SPEC)
COMPARE_SPEC.loader.exec_module(COMPARE)


class GuestStageComparisonTests(unittest.TestCase):
    def write_stage(self, root, replica, raw, final, vfs_payload=b"same-vfs", cli=b"same-cli"):
        inputs = root / f"inputs-{replica}"
        inputs.mkdir()
        vfs = root / f"vfs-{replica}"
        vfs.mkdir()
        (vfs / "stdlib.py").write_bytes(vfs_payload)
        values = {
            "raw": raw,
            "final": final,
            "archive": b"same-archive",
            "object": b"same-object",
            "cli": cli,
            "lock": b'{"schema_version": 1, "target": "wasm32-wasip1", "sources": []}\n',
        }
        for name, payload in values.items():
            (inputs / name).write_bytes(payload)
        evidence = root / f"evidence-{replica}"
        prepack_manifest = root / f"prepack-vfs-manifest-{replica}.json"
        WRITER.write_vfs_manifest(vfs, prepack_manifest)
        WRITER.write_evidence(
            evidence_dir=evidence,
            raw_wasm=inputs / "raw",
            final_wasm=inputs / "final",
            patched_wasi_vfs_archive=inputs / "archive",
            linked_storage_object=inputs / "object",
            wasi_vfs_cli=inputs / "cli",
            source_lock=inputs / "lock",
            vfs_manifest=prepack_manifest,
            repository_commit="a" * 40,
            source_date_epoch="1784734890",
            run_id="42",
            run_attempt="1",
            job="build",
            replica=replica,
            runner_os="Linux",
            runner_arch="X64",
            build_dir=f"/runner/{replica}/build",
            dist_dir=f"/runner/{replica}/dist",
            configured_vfs_root="/dev/shm/agent-runtime-vfs",
        )
        return evidence

    def test_equal_raw_and_inputs_localize_final_drift_to_pack_stage(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            left = self.write_stage(root, "one", b"same-raw", b"final-one")
            right = self.write_stage(root, "two", b"same-raw", b"final-two")
            report = COMPARE.compare_stage_evidence(left, right)

        self.assertEqual(report["outcome"], "pack-stage drift established")
        self.assertTrue(report["raw_wasm_match"])
        self.assertFalse(report["final_wasm_match"])
        self.assertTrue(all(report["pack_input_matches"].values()))
        self.assertEqual(report["validation_errors"], [])

    def test_different_raw_localizes_first_observed_drift_to_raw_or_earlier(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            left = self.write_stage(root, "one", b"raw-one", b"final-one")
            right = self.write_stage(root, "two", b"raw-two", b"final-two")
            report = COMPARE.compare_stage_evidence(left, right)

        self.assertEqual(report["outcome"], "raw-stage drift established")
        self.assertFalse(report["raw_wasm_match"])
        self.assertEqual(report["validation_errors"], [])

    def test_changed_pack_input_blocks_pack_stage_attribution(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            left = self.write_stage(root, "one", b"same-raw", b"final-one", vfs_payload=b"vfs-one")
            right = self.write_stage(root, "two", b"same-raw", b"final-two", vfs_payload=b"vfs-two")
            report = COMPARE.compare_stage_evidence(left, right)

        self.assertEqual(report["outcome"], "inconclusive due to mismatched pack inputs")
        self.assertFalse(report["pack_input_matches"]["vfs_manifest"])

    def test_tampered_retained_file_fails_validation(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            left = self.write_stage(root, "one", b"same-raw", b"final-one")
            right = self.write_stage(root, "two", b"same-raw", b"final-two")
            (right / "agent-python-runtime.raw.wasm").write_bytes(b"tampered")
            report = COMPARE.compare_stage_evidence(left, right)

        self.assertEqual(report["outcome"], "inconclusive due to missing or invalid evidence")
        self.assertTrue(any("raw_wasm" in error for error in report["validation_errors"]))


if __name__ == "__main__":
    unittest.main()
