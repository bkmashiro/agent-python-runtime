from __future__ import annotations

import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
WATCHER = ROOT / "tools/phase6_slurm_watch.py"
JOB = ROOT / "tools/phase6_slurm_job.sh"


def load_watcher():
    spec = importlib.util.spec_from_file_location("phase6_slurm_watch", WATCHER)
    if spec is None or spec.loader is None:
        raise RuntimeError("unable to load Phase 6 watcher")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class Phase6SlurmContractTests(unittest.TestCase):
    def test_job_is_t4_source_bound_and_ack_gated(self) -> None:
        source = JOB.read_text(encoding="utf-8")
        self.assertIn("#SBATCH --partition=t4", source)
        self.assertNotIn("a100", source.lower())
        self.assertIn('test "${SLURM_JOB_PARTITION:-}" = "t4"', source)
        self.assertIn("sha256sum --check payload.SHA256", source)
        self.assertIn('cmp -- "$0" "$REPO/tools/phase6_slurm_job.sh"', source)
        self.assertIn("tools/phase6_matrix.py", source)
        self.assertIn("READY-${SLURM_JOB_ID}", source)
        self.assertIn("ACK-${SLURM_JOB_ID}", source)
        self.assertIn("ACKED-${SLURM_JOB_ID}", source)
        self.assertIn('test -d "$STAGE"', source)
        self.assertIn('test ! -L "$STAGE"', source)
        self.assertIn('find "$STAGE/input" -type l', source)

    def test_watcher_rejects_duplicate_json_keys(self) -> None:
        module = load_watcher()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "duplicate.json"
            path.write_text('{"a":1,"a":2}', encoding="utf-8")
            with self.assertRaises(ValueError):
                module.unique_json(path)

    def test_watcher_rejects_duplicate_and_traversing_archive_members(self) -> None:
        module = load_watcher()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            duplicate = root / "duplicate.tar.gz"
            with tarfile.open(duplicate, "w:gz") as archive:
                for payload in (b"first", b"second"):
                    info = tarfile.TarInfo("result/evidence.json")
                    info.size = len(payload)
                    archive.addfile(info, io.BytesIO(payload))
            with self.assertRaises(RuntimeError):
                module.safe_extract(duplicate, root / "duplicate-out")

            traversal = root / "traversal.tar.gz"
            with tarfile.open(traversal, "w:gz") as archive:
                payload = b"escape"
                info = tarfile.TarInfo("../escape")
                info.size = len(payload)
                archive.addfile(info, io.BytesIO(payload))
            with self.assertRaises(RuntimeError):
                module.safe_extract(traversal, root / "traversal-out")

            oversized = root / "oversized.tar.gz"
            with tarfile.open(oversized, "w:gz") as archive:
                payload = b"five!"
                info = tarfile.TarInfo("result/evidence.json")
                info.size = len(payload)
                archive.addfile(info, io.BytesIO(payload))
            original_limit = module.MAX_EXTRACTED_BYTES
            module.MAX_EXTRACTED_BYTES = 4
            try:
                with self.assertRaises(RuntimeError):
                    module.safe_extract(oversized, root / "oversized-out")
            finally:
                module.MAX_EXTRACTED_BYTES = original_limit

    def test_identity_validation_is_fail_closed(self) -> None:
        module = load_watcher()
        valid = {
            "job": "12345",
            "host": "gpucluster2",
            "stage": "/vol/bitbucket/ys25/pysolate-phase6-64666a5",
            "expected_revision": "6" * 40,
            "expected_tree": "d" * 40,
            "expected_artifact_sha256": "a" * 64,
            "expected_artifact_manifest_sha256": "c" * 64,
            "expected_artifact_source": "7" * 40,
            "validator_sha256": "b" * 64,
            "expected_tier": "canary",
            "interval": 60,
        }
        module.validate_identity_args(**valid)
        for field, bad in (
            ("job", "12;rm"),
            ("host", "gpucluster2;rm"),
            ("stage", "/tmp/wrong"),
            ("expected_revision", "6" * 39),
            ("expected_tree", "d" * 39),
            ("expected_artifact_sha256", "true"),
            ("expected_artifact_manifest_sha256", "c" * 63),
            ("expected_artifact_source", "7" * 41),
            ("validator_sha256", "b" * 63),
            ("expected_tier", "unknown"),
            ("interval", 59),
        ):
            mutation = dict(valid)
            mutation[field] = bad
            with self.assertRaises(RuntimeError, msg=field):
                module.validate_identity_args(**mutation)

    def test_record_set_is_complete_unique_and_exact(self) -> None:
        module = load_watcher()
        canonical = module.expected_plan_cells(ROOT, "canary", None)
        self.assertEqual(2, len(canonical))
        cells = [
            {"cell_id": "first", "repetition": 1},
            {"cell_id": "second", "repetition": 1},
        ]
        records = [
            {"cell": cells[0], "evidence": "first.json"},
            {"cell": cells[1], "evidence": "second.json"},
        ]
        self.assertEqual(["first.json", "second.json"], module.validate_record_set(records, cells, "canary"))
        for mutation in (
            records[:1],
            [records[0], records[0]],
            [{"cell": cells[1], "evidence": "first.json"}, records[1]],
            [records[0], {"cell": cells[1], "evidence": "first.json"}],
        ):
            with self.assertRaises(RuntimeError):
                module.validate_record_set(mutation, cells, "canary")

        formal = [{"cell_id": "selected", "repetition": 3}]
        self.assertEqual(
            ["selected-r3.json"],
            module.validate_record_set(
                [{"cell": formal[0], "evidence": "selected-r3.json"}], formal, "formal"
            ),
        )

    def test_slurm_and_acked_parsers_require_exact_rows(self) -> None:
        module = load_watcher()
        self.assertEqual("RUNNING", module.parse_squeue_state("123|RUNNING\n", "123"))
        with self.assertRaises(RuntimeError):
            module.parse_squeue_state("999|RUNNING\n", "123")
        with self.assertRaises(RuntimeError):
            module.parse_squeue_state("123|RUNNING\n123|PENDING\n", "123")

        accounting = "123|COMPLETED|0:0|00:10|1G|16G|4|kingfisher|\n"
        row = module.parse_sacct_accounting(accounting, "123")
        self.assertEqual("COMPLETED", row["state"])
        with self.assertRaises(RuntimeError):
            module.parse_sacct_accounting(accounting + "123.batch|COMPLETED|0:0||||||\n", "123")

        controller = module.parse_scontrol_job(
            "JobId=123 JobName=p6 JobState=COMPLETED ExitCode=0:0 Partition=t4\n", "123"
        )
        self.assertEqual("COMPLETED", controller["JobState"])
        with self.assertRaises(RuntimeError):
            module.parse_scontrol_job(
                "JobId=999 JobName=p6 JobState=COMPLETED ExitCode=0:0 Partition=t4\n", "123"
            )

        digest = "a" * 64
        module.validate_acked_text(f"{digest}  2026-08-08T18:00:00Z\n", digest)
        for text in (digest + "\n", f"{digest}  now\n", f"{digest}  2026-08-08T18:00:00Z\nextra\n"):
            with self.assertRaises(RuntimeError):
                module.validate_acked_text(text, digest)


if __name__ == "__main__":
    unittest.main()
