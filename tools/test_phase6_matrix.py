#!/usr/bin/env python3

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("phase6_matrix.py")
SPEC = importlib.util.spec_from_file_location("phase6_matrix", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class Phase6MatrixTest(unittest.TestCase):
    def test_tiers_are_bounded_and_formal_is_explicit(self) -> None:
        canary = MODULE.cells_for_tier("canary")
        small = MODULE.cells_for_tier("small")
        self.assertEqual(2, len(canary))
        self.assertEqual(11, len(small))
        self.assertEqual(len(small), len({cell.cell_id for cell in small}))
        with self.assertRaises(ValueError):
            MODULE.cells_for_tier("formal")
        with self.assertRaises(ValueError):
            MODULE.cells_for_tier("formal", ["unknown"])
        selected = [small[0].cell_id, small[-1].cell_id]
        formal = MODULE.cells_for_tier("formal", selected)
        self.assertEqual(6, len(formal))
        self.assertEqual([1, 2, 3, 1, 2, 3], [cell.repetition for cell in formal])
        with self.assertRaises(ValueError):
            MODULE.cells_for_tier("formal", [selected[0], selected[0]])

    def test_commands_are_numpy_profile_bound(self) -> None:
        for cell in MODULE.cells_for_tier("canary") + MODULE.cells_for_tier("small"):
            command = MODULE.command_for_cell(
                cell,
                binary=Path("/tmp/apyrun-benchmark"),
                artifact=Path("/tmp/python-numpy.wasm"),
                artifact_manifest=Path("/tmp/python-numpy.manifest.json"),
                output=Path(f"/tmp/{cell.cell_id}.json"),
                memory_budget_bytes=8 << 30,
                memory_reserve_bytes=2 << 30,
                max_cpu=16,
                greed=50,
            )
            joined = "\n".join(command)
            self.assertIn("-class=profile-candidate", command)
            self.assertIn("-cow-warmup-profile=numpy-ready-v1", command)
            self.assertIn(f"-pressure-workload={cell.workload}", command)
            self.assertIn("-pressure-refill-workers=0", command)
            self.assertNotIn("-class=production-safe", command)
            self.assertNotIn("artifact_profile=base", joined)
            if cell.arrival_mode == "closed-loop":
                self.assertIn("-pressure-arrival-rate=0", command)
                self.assertIn("-pressure-queue-capacity=0", command)
            else:
                self.assertGreater(cell.arrival_rate, 0)
                self.assertGreater(cell.queue_capacity, 0)

    def test_selection_file_must_be_string_array(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "selection.json"
            path.write_text(json.dumps([1]), encoding="utf-8")
            with self.assertRaises(ValueError):
                MODULE.load_selection(path)
            path.write_text(json.dumps(["closed-numpy-v1-s64-c1"]), encoding="utf-8")
            self.assertEqual(["closed-numpy-v1-s64-c1"], MODULE.load_selection(path))

    def test_evidence_summary_validation_fails_closed(self) -> None:
        cell = MODULE.cells_for_tier("canary")[0]
        revision = "a" * 40
        artifact_sha = "b" * 64
        evidence = {
            "schema_version": 11,
            "evidence_kind": "cow-pressure",
            "evidence_class": "profile-candidate",
            "artifact": {"sha256": artifact_sha, "artifact_profile": "numpy-core"},
            "host_source": {"revision": revision, "modified": False},
            "limits": {"workload": cell.workload, "warmup_profile": "numpy-ready-v1", "consumers": cell.consumers, "max_slots": cell.slots},
            "load": {
                "arrival": {"mode": "closed-loop", "rate_per_second": 0, "queue_capacity": 0, "offered_requests": 3, "accepted_requests": 3, "rejected_requests": 0},
                "started_requests": 3,
                "completed_requests": 3,
                "failed_requests": 0,
                "replenish_status": "complete",
                "ready_before": 4,
                "ready_after": 4,
                "request_classes": [{"name": "numpy-tiny"}],
            },
        }
        MODULE.validate_output(evidence, cell=cell, revision=revision, artifact_sha256=artifact_sha)
        mutations = []
        wrong_artifact = json.loads(json.dumps(evidence))
        wrong_artifact["artifact"]["artifact_profile"] = "base"
        mutations.append(wrong_artifact)
        wrong_arrival = json.loads(json.dumps(evidence))
        wrong_arrival["load"]["arrival"]["offered_requests"] = 4
        mutations.append(wrong_arrival)
        incomplete = json.loads(json.dumps(evidence))
        incomplete["load"]["ready_after"] = 3
        mutations.append(incomplete)
        wrong_class = json.loads(json.dumps(evidence))
        wrong_class["load"]["request_classes"] = [{"name": "tiny-cpu"}]
        mutations.append(wrong_class)
        for mutation in mutations:
            with self.assertRaises(RuntimeError):
                MODULE.validate_output(mutation, cell=cell, revision=revision, artifact_sha256=artifact_sha)
        evidence["load"]["arrival"]["offered_requests"] = 4
        with self.assertRaises(RuntimeError):
            MODULE.validate_output(evidence, cell=cell, revision=revision, artifact_sha256=artifact_sha)
        evidence["load"]["arrival"]["offered_requests"] = True
        with self.assertRaises(RuntimeError):
            MODULE.validate_output(evidence, cell=cell, revision=revision, artifact_sha256=artifact_sha)


if __name__ == "__main__":
    unittest.main()
