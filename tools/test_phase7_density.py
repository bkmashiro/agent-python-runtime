import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


MODULE_PATH = Path(__file__).with_name("phase7_density.py")
SPEC = importlib.util.spec_from_file_location("phase7_density", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


def metric(value: int) -> dict:
    return {"status": "measured", "value": value}


def sample(strategy: str, slots: int, repeat: int) -> dict:
    cow = strategy == MODULE.COW_STRATEGY
    result = {
        "requested_slots": slots,
        "repeat": repeat,
        "runtime_shards": 1 if cow else (slots + 3) // 4,
        "process_instance_sha256": hashlib.sha256(f"{strategy}-{slots}-{repeat}".encode()).hexdigest(),
        "observed_at_unix_ns": {"status": "timestamp-observed", "value": repeat},
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


def arm(strategy: str, repeats: int = 1) -> dict:
    return {
        "schema_version": 2,
        "evidence_class": "lifecycle-density",
        "artifact": {
            "filename": "numpy.wasm", "sha256": "a" * 64, "size_bytes": 10,
            "source_commit": "b" * 40, "artifact_profile": "numpy-core",
            "target": "wasm32-wasip1", "execution_model": "reactor",
        },
        "host_source": {"revision": "c" * 40, "dirty": False},
        "backend": {"name": "wazero", "version": "v1", "reset_mode": "fresh-instance"},
        "environment": {"goos": "linux", "goarch": "amd64", "go_version": "go1", "kernel_release": "k", "page_size_bytes": 4096, "cgroup_version": "v2"},
        "strategy": {"requested": strategy, "active": strategy, "fallback": False},
        "warmup": {"profile": "numpy-ready-v1", "generation_sha256": "d" * 64},
        "plan": {
            "workload": "numpy-ready-idle", "slot_counts": MODULE.CANONICAL_SLOTS,
            "repeats": repeats, "child_timeout_ms": 120000, "memory_guard_bytes": 8 << 30,
        },
        "metric_semantics": {"status_values": ["measured"]},
        "observability": {"process_source": "/proc"},
        "samples": [sample(strategy, slots, repeat) for slots in MODULE.CANONICAL_SLOTS for repeat in range(1, repeats + 1)],
    }


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

    def test_pair_evidence_rejects_cross_arm_identity_drift(self) -> None:
        for field, mutate in {
            "artifact": lambda value: value["artifact"].update({"sha256": "f" * 64}),
            "warmup": lambda value: value["warmup"].update({"generation_sha256": "e" * 64}),
            "environment": lambda value: value["environment"].update({"kernel_release": "other"}),
            "plan": lambda value: value["plan"].update({"child_timeout_ms": 1}),
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
        self.assertIn("-child-timeout 4m", source)
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


if __name__ == "__main__":
    unittest.main()
