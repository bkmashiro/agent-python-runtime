import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


MODULE_PATH = Path(__file__).with_name("phase7_density.py")
SPEC = importlib.util.spec_from_file_location("phase7_density", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)

CONTROLLER_PATH = Path(__file__).with_name("phase7_cell_campaign.py")
CONTROLLER_SPEC = importlib.util.spec_from_file_location("phase7_cell_campaign", CONTROLLER_PATH)
assert CONTROLLER_SPEC is not None
CONTROLLER = importlib.util.module_from_spec(CONTROLLER_SPEC)
assert CONTROLLER_SPEC.loader is not None
CONTROLLER_SPEC.loader.exec_module(CONTROLLER)


def metric(value: int) -> dict:
    return {"status": "measured", "value": value}


def sample(strategy: str, slots: int, repeat_index: int, repeats: int) -> dict:
    cow = strategy == MODULE.COW_STRATEGY
    sample_index = MODULE.CANONICAL_SLOTS.index(slots) * repeats + repeat_index
    result = {
        "sample_index": sample_index,
        "repeat_index": repeat_index,
        "requested_slots": slots,
        "runtime_shards": 1 if cow else (slots + 3) // 4,
        "process_instance_sha256": hashlib.sha256(f"{strategy}-{slots}-{repeat_index}".encode()).hexdigest(),
        "observed_at_unix_ns": {"status": "timestamp-observed", "value": repeat_index + 1},
        "pool": {"target_capacity": slots, "ready": slots, "leased": 0, "executing": 0, "refilling": 0, "retiring": 0, "accounted_slots": slots},
        "phases": {"total_ns": metric(slots * (10 if cow else 20)), "warmup_ns": metric(slots if not cow else 1)},
        "process": {
            "rss_bytes": metric(slots * (100 if cow else 300)),
            "pss_bytes": metric(slots * (80 if cow else 240)),
            "private_dirty_bytes": metric(slots * (20 if cow else 180)),
        },
    }
    if cow:
        result["cow_mappings"] = {"mapping_count": slots, "pss_bytes": metric(slots * 60)}
    return result


def arm(strategy: str, repeats: int = 1, boundary64: bool = False) -> dict:
    samples = [sample(strategy, slots, repeat_index, repeats)
               for slots in MODULE.CANONICAL_SLOTS for repeat_index in range(repeats)
               if not (boundary64 and strategy == MODULE.NON_COW_STRATEGY and slots == 64)]
    boundaries = []
    if boundary64 and strategy == MODULE.NON_COW_STRATEGY:
        boundaries = [{
            "sample_index": 6 * repeats + repeat_index,
            "repeat_index": repeat_index,
            "requested_slots": 64,
            "process_instance_sha256": hashlib.sha256(f"boundary-{repeat_index}".encode()).hexdigest(),
            "status": "rss_guard",
            "max_observed_rss_bytes": MODULE.MAX_RSS_BYTES + 4096,
            "guard_rss_bytes": MODULE.MAX_RSS_BYTES,
        } for repeat_index in range(repeats)]
    result = {
        "schema_version": 3,
        "evidence_class": "lifecycle-density",
        "artifact": {
            "filename": "numpy.wasm", "sha256": "a" * 64, "size_bytes": 10,
            "source_commit": "b" * 40, "artifact_profile": "numpy-core",
            "target": "wasm32-wasip1", "execution_model": "reactor",
        },
        "host_source": {"revision": "c" * 40, "modified": False},
        "backend": {"name": "wazero", "version": "v1", "reset_mode": "fresh-instance"},
        "environment": {"goos": "linux", "goarch": "amd64", "go_version": "go1", "kernel_release": "k", "page_size_bytes": 4096, "cgroup_version": "v2"},
        "strategy": {"requested": strategy, "active": strategy, "fallback": False},
        "warmup": {"profile": "numpy-ready-v1", "generation_sha256": "d" * 64},
        "plan": {
            "workload": "numpy-ready-idle", "slot_counts": MODULE.CANONICAL_SLOTS,
            "repeats_per_slot": repeats, "fresh_process_per_sample": True,
            "child_timeout_ns": MODULE.CHILD_TIMEOUT_NS, "max_process_rss_bytes": MODULE.MAX_RSS_BYTES,
        },
        "metric_semantics": {"status_values": ["measured"]},
        "observability": {"process_source": "/proc"},
        "samples": samples,
        "summary": {"sample_count": len(samples), "boundary_count": len(boundaries)},
    }
    if boundaries:
        result["boundaries"] = boundaries
    return result


class Phase7DensityTests(unittest.TestCase):
    def test_pair_evidence_is_deterministic_and_derives_integer_ratios(self) -> None:
        cow = arm(MODULE.COW_STRATEGY, repeats=3)
        non_cow = arm(MODULE.NON_COW_STRATEGY, repeats=3)
        first = MODULE.pair_evidence(cow, non_cow, b"cow", b"non-cow")
        second = MODULE.pair_evidence(copy.deepcopy(cow), copy.deepcopy(non_cow), b"cow", b"non-cow")
        self.assertEqual(first, second)
        self.assertEqual(21, len(first["pairs"]))
        self.assertEqual(7, len(first["summary_by_slots"]))
        self.assertEqual(666666, first["pairs"][0]["derived"]["pss_reduction_ppm"])
        self.assertEqual(2000000, first["pairs"][0]["derived"]["non_cow_to_cow_ready_time_ppm"])

    def test_pair_evidence_records_non_cow_64_rss_boundary(self) -> None:
        cow = arm(MODULE.COW_STRATEGY, repeats=3)
        non_cow = arm(MODULE.NON_COW_STRATEGY, repeats=3, boundary64=True)
        paired = MODULE.pair_evidence(cow, non_cow, b"cow", b"non-cow")
        self.assertEqual(18, len(paired["pairs"]))
        self.assertEqual(6, len(paired["summary_by_slots"]))
        self.assertEqual(3, len(paired["boundary_outcomes"]))
        self.assertEqual(3, paired["coverage_by_slots"][-1]["cow_measured"])
        self.assertEqual(0, paired["coverage_by_slots"][-1]["non_cow_measured"])
        self.assertEqual(3, paired["coverage_by_slots"][-1]["non_cow_rss_guard"])

        invalid = arm(MODULE.NON_COW_STRATEGY, boundary64=True)
        invalid["boundaries"][0]["max_observed_rss_bytes"] = MODULE.MAX_RSS_BYTES
        with self.assertRaisesRegex(MODULE.ValidationError, "boundary"):
            MODULE.pair_evidence(arm(MODULE.COW_STRATEGY), invalid, b"cow", b"non-cow")

    def test_pair_evidence_rejects_cross_arm_identity_drift(self) -> None:
        for field, mutate in {
            "artifact": lambda value: value["artifact"].update({"sha256": "f" * 64}),
            "warmup": lambda value: value["warmup"].update({"generation_sha256": "e" * 64}),
            "environment": lambda value: value["environment"].update({"kernel_release": "other"}),
            "plan": lambda value: value["plan"].update({"child_timeout_ns": 1}),
        }.items():
            with self.subTest(field=field):
                cow, non_cow = arm(MODULE.COW_STRATEGY), arm(MODULE.NON_COW_STRATEGY)
                mutate(non_cow)
                with self.assertRaises(MODULE.ValidationError):
                    MODULE.pair_evidence(cow, non_cow, b"cow", b"non-cow")

    def test_pair_evidence_rejects_runtime_topology_and_mapping_drift(self) -> None:
        cow, non_cow = arm(MODULE.COW_STRATEGY), arm(MODULE.NON_COW_STRATEGY)
        non_cow["samples"][-1]["runtime_shards"] = 1
        with self.assertRaisesRegex(MODULE.ValidationError, "topology"):
            MODULE.pair_evidence(cow, non_cow, b"cow", b"non-cow")
        cow, non_cow = arm(MODULE.COW_STRATEGY), arm(MODULE.NON_COW_STRATEGY)
        cow["samples"][-1]["cow_mappings"]["mapping_count"] = 1
        with self.assertRaisesRegex(MODULE.ValidationError, "mapping"):
            MODULE.pair_evidence(cow, non_cow, b"cow", b"non-cow")

    def test_slurm_wrapper_is_t4_source_bound_and_runs_exact_pair(self):
        source = Path(__file__).with_name("phase7_slurm_job.sh").read_text(encoding="utf-8")
        self.assertIn("#SBATCH --partition=t4", source)
        self.assertIn("#SBATCH --gres=gpu:tesla_t4:1", source)
        self.assertIn("#SBATCH --cpus-per-task=4", source)
        self.assertIn("#SBATCH --mem=16G", source)
        self.assertIn("#SBATCH --export=NIL", source)
        self.assertIn("single-use-preinitialized", source)
        self.assertIn("cow-ready-single-use", source)
        self.assertIn("-prepared-warmup-profile numpy-ready-v1", source)
        self.assertIn("-max-rss-bytes 8589934592", source)
        self.assertIn("validate-lifecycle-density", source)
        self.assertEqual(2, source.count("-schema \"$REPO/benchmark/v1/lifecycle-density.schema.json\""))
        self.assertIn("validate-phase7-paired-density", source)
        self.assertIn("-schema \"$REPO/benchmark/v1/phase7-paired-density.schema.json\"", source)
        self.assertIn("paired-summary.json.validation.json", source)
        self.assertLess(source.index("validate-phase7-paired-density"), source.index("sha256sum cow.json"))
        self.assertIn("phase7_density.py", source)
        self.assertIn("source.bundle", source)
        self.assertIn("payload.SHA256", source)
        self.assertNotIn("BINARY_METADATA.txt", source)
        self.assertIn('-kind binary-source-identity', source)
        self.assertIn('cmp -- "$0" "$INPUT/phase7_slurm_job.sh"', source)
        self.assertIn("expected_payload_max_bytes=", source)
        self.assertIn("copy_bounded_regular", source)
        self.assertIn("f00f22ac94a66f2f2e67573da11ef879f8b5e46622eb9379300cc1e6a5b40a30", source)
        self.assertIn("458a4e4bbec1ad225f0f3c38357738f1937b1e16d5388f76cdf4c460ce6839fa", source)
        self.assertGreaterEqual(source.count("os.O_NOFOLLOW"), 2)
        self.assertIn("os.fstat", source)
        self.assertIn("publish_exclusive", source)
        self.assertNotIn('mv "$archive_tmp" "$archive"', source)
        self.assertGreaterEqual(source.count('ln -- "$'), 2)
        self.assertIn('OWNER_MARKER_TMP="$NODE_ROOT/.phase7-owner.partial"', source)
        self.assertIn('rm -f -- "$source_path" || true', source)
        self.assertIn('chmod 500 "$INPUT/bin/apyrun-benchmark-linux-amd64"', source)
        self.assertIn('cd "$REPO"', source)
        self.assertLess(source.index('cd "$REPO"'), source.index(' -kind binary-source-identity'))
        self.assertLess(source.index('cd "$REPO"'), source.index(' -kind lifecycle-density'))
        self.assertIn('-child-timeout 4m', source)
        self.assertNotIn("-child-timeout 12m", source)
        self.assertIn("#SBATCH --output=/vol/bitbucket/ys25/pysolate-p7-slurm-%j.out", source)
        self.assertIn("READY-${SLURM_JOB_ID}", source)
        self.assertIn("ACK-${SLURM_JOB_ID}", source)
        self.assertIn("FAILED-${SLURM_JOB_ID}", source)
        self.assertIn("failure_line=%s", source)
        self.assertLess(source.index("trap record_failure_and_cleanup EXIT"), source.index('test "${SLURM_JOB_PARTITION:-}" = t4'))
        self.assertIn('abort_job "$LINENO" 72 "ACK timeout for $archive_sha"', source)
        self.assertIn('abort_job "$LINENO" 143 "received SIGTERM"', source)
        self.assertNotIn("a100", source.lower())
        self.assertNotIn("sh -c", source)

    def test_cell_slurm_wrapper_is_one_array_task_per_paired_cell(self):
        source = Path(__file__).with_name("phase7_cell_slurm_job.sh").read_text(encoding="utf-8")
        for token in (
            "#SBATCH --partition=t4", "#SBATCH --gres=gpu:tesla_t4:1", "#SBATCH --cpus-per-task=4",
            "#SBATCH --mem=16G", "#SBATCH --export=NIL", "SLURM_ARRAY_JOB_ID", "SLURM_ARRAY_TASK_ID",
            "CELL_COUNT=$((7 * SAMPLES))", "SLOT_INDEX=$((SLURM_ARRAY_TASK_ID / SAMPLES))",
            '[[ "$SLURM_ARRAY_JOB_ID" =~ ^[1-9][0-9]*$ ]]', '[[ "${SLURM_JOB_ID:-}" =~ ^[1-9][0-9]*$ ]]',
            "REPEAT_INDEX=$((SLURM_ARRAY_TASK_ID % SAMPLES))", "-kind phase7-paired-density-cell",
            "-kind validate-phase7-density-cell", "phase7-paired-density-cell.schema.json",
            "-max-rss-bytes 8589934592", "-child-timeout 4m", "P7_ARM_ORDER", "ACK-${CELL_TAG}",
            "READY-${CELL_TAG}", "FAILED-${CELL_TAG}", "publish_exclusive", "source.bundle",
            'cmp -- "$0" "$INPUT/phase7_cell_slurm_job.sh"',
        ):
            self.assertIn(token, source)
        self.assertIn('test -d "$OUTBOX" && test ! -L "$OUTBOX"', source)
        self.assertNotIn('mkdir "$OUTBOX"', source)
        self.assertNotIn("phase7_density.py", source)
        self.assertNotIn("-kind lifecycle-density ", source)
        self.assertNotIn("a100", source.lower())
        self.assertNotIn("sh -c", source)

    def test_strict_load_rejects_duplicate_keys_and_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            duplicate = root / "duplicate.json"
            duplicate.write_text('{"schema_version":2,"schema_version":2}\n')
            with self.assertRaisesRegex(MODULE.ValidationError, "duplicate"):
                MODULE.strict_load(duplicate)
            target = root / "target.json"
            target.write_text("{}\n")
            link = root / "link.json"
            link.symlink_to(target)
            with self.assertRaisesRegex(MODULE.ValidationError, "bounded regular"):
                MODULE.strict_load(link)

    def test_dynamic_array_map_is_bounded_strict_and_freezable(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            mapping_path = root / "map.json"
            mapping_path.write_text(json.dumps({str(task): "900" for task in range(14)}))
            args = SimpleNamespace(array_job_ids=[], array_job_map=mapping_path, count=21, bound_task_jobs=None)
            self.assertEqual(CONTROLLER.load_array_job_map(args), {task: "900" for task in range(14)})
            frozen = CONTROLLER.load_array_job_map(args)
            args.bound_task_jobs = frozen
            mapping_path.write_text(json.dumps({"0": "999"}))
            self.assertEqual(CONTROLLER.load_array_job_map(args), frozen)
            args.bound_task_jobs = None
            mapping_path.write_text('{"0":"900","0":"901"}')
            with self.assertRaisesRegex(ValueError, "duplicate"):
                CONTROLLER.load_array_job_map(args)
            mapping_path.write_text('{"21":"900"}')
            with self.assertRaisesRegex(RuntimeError, "out of range"):
                CONTROLLER.load_array_job_map(args)
            mapping_path.write_text('{"0":"0009"}')
            with self.assertRaisesRegex(RuntimeError, "canonical"):
                CONTROLLER.load_array_job_map(args)
            target = root / "target.json"
            target.write_text('{"0":"900"}')
            mapping_path.unlink()
            mapping_path.symlink_to(target)
            with self.assertRaisesRegex(RuntimeError, "bounded regular"):
                CONTROLLER.load_array_job_map(args)

    def test_dynamic_array_map_progresses_in_validated_ack_waves(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            mapping_path = root / "map.json"
            mapping_path.write_text('{"0":"900"}')
            args = SimpleNamespace(array_job_ids=[], array_job_map=mapping_path, count=3, bound_task_jobs=None,
                                   remote_root="/remote", local_root=root)
            acknowledgements = []

            def snapshot(_remote_root, task_jobs):
                rows = [{"task": task, "ready": 1, "failed": 0, "acked": 1} for task in task_jobs]
                if len(task_jobs) == 1:
                    mapping_path.write_text('{"0":"900","1":"901","2":"901"}')
                return rows

            def validate(_args, task, _campaign_root, _array_job=None):
                return ({"job_id": str(1000 + task), "cgroup_sha256": f"{task + 1:064x}"}, f"{task + 10:064x}")

            def completed(job, *_expected):
                task = int(job.rsplit('_', 1)[1])
                return ({"JobId": str(1000 + task)}, "gres/gpu:tesla_t4:1")

            with patch.object(CONTROLLER, "remote_snapshot", side_effect=snapshot), \
                 patch.object(CONTROLLER, "validate_cell", side_effect=validate), \
                 patch.object(CONTROLLER, "assert_running_shape"), \
                 patch.object(CONTROLLER, "assert_completed", side_effect=completed), \
                 patch.object(CONTROLLER, "write_ack", side_effect=lambda path, digest: acknowledgements.append((path, digest))), \
                 patch.object(CONTROLLER.time, "sleep"):
                entries, _, accepted_jobs, terminal = CONTROLLER.validate_and_ack_cells(args, 3)
            self.assertEqual(accepted_jobs, ["900", "901", "901"])
            self.assertEqual(len(entries), 3)
            self.assertEqual(len(terminal), 3)
            self.assertEqual(len(acknowledgements), 3)
            self.assertTrue(acknowledgements[0][0].endswith("ACK-900_0"))
            self.assertTrue(acknowledgements[2][0].endswith("ACK-901_2"))

    def test_accepted_array_alias_cannot_be_remapped_before_terminal_snapshot(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            mapping_path = root / "map.json"
            mapping_path.write_text('{"0":"900"}')
            args = SimpleNamespace(array_job_ids=[], array_job_map=mapping_path, count=2, bound_task_jobs=None,
                                   remote_root="/remote", local_root=root)
            calls = 0

            def snapshot(_remote_root, task_jobs):
                nonlocal calls
                calls += 1
                if calls == 1:
                    mapping_path.write_text('{"0":"999"}')
                return [{"task": task, "ready": 1, "failed": 0, "acked": 0} for task in task_jobs]

            with patch.object(CONTROLLER, "remote_snapshot", side_effect=snapshot), \
                 patch.object(CONTROLLER, "validate_cell", return_value=({"job_id": "1000", "cgroup_sha256": "1" * 64}, "2" * 64)), \
                 patch.object(CONTROLLER, "assert_running_shape"), patch.object(CONTROLLER, "write_ack"), \
                 patch.object(CONTROLLER.time, "sleep"):
                with self.assertRaisesRegex(RuntimeError, "mapping drifted"):
                    CONTROLLER.validate_and_ack_cells(args, 2)

    def test_final_terminal_iteration_rechecks_map_before_freeze(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            mapping_path = root / "map.json"
            mapping_path.write_text('{"0":"900"}')
            args = SimpleNamespace(array_job_ids=[], array_job_map=mapping_path, count=1, bound_task_jobs=None,
                                   remote_root="/remote", local_root=root)

            def completed(*_args):
                mapping_path.write_text('{"0":"999"}')
                return ({"JobId": "1000"}, "gres/gpu:tesla_t4:1")

            with patch.object(CONTROLLER, "remote_snapshot", return_value=[{"task": 0, "ready": 1, "failed": 0, "acked": 1}]), \
                 patch.object(CONTROLLER, "validate_cell", return_value=({"job_id": "1000", "cgroup_sha256": "1" * 64}, "2" * 64)), \
                 patch.object(CONTROLLER, "assert_running_shape"), patch.object(CONTROLLER, "write_ack"), \
                 patch.object(CONTROLLER, "assert_completed", side_effect=completed):
                with self.assertRaisesRegex(RuntimeError, "mapping drifted"):
                    CONTROLLER.validate_and_ack_cells(args, 1)

    def test_remote_retries_only_transport_exit_255(self):
        failed = subprocess.CompletedProcess(["ssh"], 255, stdout=b"", stderr=b"closed")
        succeeded = subprocess.CompletedProcess(["ssh"], 0, stdout=b"ok", stderr=b"")
        with patch.object(CONTROLLER.subprocess, "run", side_effect=[failed, succeeded]) as run, \
             patch.object(CONTROLLER.time, "sleep") as sleep:
            result = CONTROLLER.remote("true")
        self.assertEqual(result.stdout, b"ok")
        self.assertEqual(run.call_count, 2)
        sleep.assert_called_once_with(60)

        rejected = subprocess.CompletedProcess(["ssh"], 1, stdout=b"", stderr=b"bad command")
        with patch.object(CONTROLLER.subprocess, "run", return_value=rejected) as run, \
             patch.object(CONTROLLER.time, "sleep") as sleep:
            with self.assertRaises(subprocess.CalledProcessError):
                CONTROLLER.remote("false")
        self.assertEqual(run.call_count, 1)
        sleep.assert_not_called()

    def test_terminal_scheduler_snapshot_is_fully_bound(self):
        values = {"JobId": "901", "ArrayJobId": "900", "ArrayTaskId": "0", "JobState": "COMPLETED",
                  "ExitCode": "0:0", "Partition": "t4", "NumCPUs": "4", "NumNodes": "1",
                  "MinMemoryNode": "16G", "Restarts": "0", "TresPerNode": "gres/gpu:tesla_t4:1"}
        with patch.object(CONTROLLER, "scontrol", return_value=(values, "snapshot")):
            CONTROLLER.assert_completed("900_0", "901", "900", 0)
        for field, wrong in (("ArrayJobId", "999"), ("ArrayTaskId", "1"), ("Partition", "a100"),
                             ("NumCPUs", "8"), ("MinMemoryNode", "32G"),
                             ("TresPerNode", "gres/gpu:tesla_a100:1")):
            drift = dict(values); drift[field] = wrong
            with patch.object(CONTROLLER, "scontrol", return_value=(drift, "snapshot")):
                with self.assertRaisesRegex(RuntimeError, "drift"):
                    CONTROLLER.assert_completed("900_0", "901", "900", 0)

    def test_committed_cell_controller_binds_multi_array_scheduler_receipts(self):
        args = SimpleNamespace(array_job_ids=["900", "901", "902"], array_job_map=None, count=21, bound_task_jobs=None)
        self.assertEqual(
            [CONTROLLER.array_job_for_task(args, task) for task in (0, 6, 7, 13, 14, 20)],
            ["900", "900", "901", "901", "902", "902"],
        )
        source = CONTROLLER_PATH.read_text(encoding="utf-8")
        for token in (
            "scontrol show job ", "assert_running_shape", "assert_completed",
            "READY-{tag}", "ACKED-{tag}", "validate_and_ack_cells",
            "snapshot_filename", "snapshot_sha256", "assert_scheduler_shape",
            "allocation or cgroup identity was reused",
        ):
            self.assertIn(token, source)


if __name__ == "__main__":
    unittest.main()
