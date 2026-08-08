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
        artifact_source = "c" * 40
        memory_budget, memory_reserve, max_cpu, greed = 8 << 30, 2 << 30, 16, 50
        validation_inputs = {
            "cell": cell, "revision": revision, "artifact_sha256": artifact_sha,
            "artifact_source_commit": artifact_source, "memory_budget_bytes": memory_budget,
            "memory_reserve_bytes": memory_reserve, "max_cpu": max_cpu, "greed": greed,
        }
        evidence = {
            "schema_version": 11,
            "evidence_kind": "cow-pressure",
            "evidence_class": "profile-candidate",
            "artifact": {"sha256": artifact_sha, "artifact_profile": "numpy-core", "source_commit": artifact_source},
            "host_source": {"revision": revision, "modified": False},
            "policy": {"max_memory_bytes": memory_budget, "max_cpu": max_cpu, "greed": greed},
            "limits": {
                "workload": cell.workload, "warmup_profile": "numpy-ready-v1", "consumers": cell.consumers,
                "max_slots": cell.slots, "runtime_budget_bytes": memory_budget,
                "reserved_bytes": memory_reserve, "allocation_bytes": memory_budget + memory_reserve,
            },
            "load": {
                "arrival": {"mode": "closed-loop", "window_ns": 0, "rate_per_second": 0, "queue_capacity": 0, "offered_requests": 3, "accepted_requests": 3, "rejected_requests": 0},
                "result_oracle": "numpy-exact-v1",
                "validated_results": 3,
                "latency_samples_ns": [1, 2, 3],
                "latency_total_ns": 6,
                "latency_mean_ns": 2,
                "latency_p50_ns": 2,
                "latency_p95_ns": 3,
                "latency_p99_ns": 3,
                "latency_max_ns": 3,
                "started_requests": 3,
                "completed_requests": 3,
                "failed_requests": 0,
                "replenish_status": "complete",
                "ready_before": 4,
                "ready_after": 4,
                "request_classes": [{"name": "numpy-tiny", "started": 3, "completed": 3, "failed": 0}],
            },
        }
        # A frozen Guest artifact source and the current Host harness source are separate identities.
        self.assertNotEqual(revision, artifact_source)
        MODULE.validate_output(evidence, **validation_inputs)
        open_cell = MODULE.cells_for_tier("canary")[1]
        open_evidence = json.loads(json.dumps(evidence))
        open_evidence["load"]["arrival"] = {
            "mode": "open-loop-fixed-v1", "window_ns": 5_000_000_000, "rate_per_second": 1,
            "queue_capacity": 4, "offered_requests": 5, "accepted_requests": 5, "rejected_requests": 0,
        }
        open_evidence["load"].update(
            validated_results=5, started_requests=5, completed_requests=5,
            latency_samples_ns=[1, 2, 3, 4, 5], latency_total_ns=15, latency_mean_ns=3,
            latency_p50_ns=3, latency_p95_ns=5, latency_p99_ns=5, latency_max_ns=5,
            request_classes=[{"name": "numpy-tiny", "started": 5, "completed": 5, "failed": 0}],
        )
        open_inputs = dict(validation_inputs, cell=open_cell)
        MODULE.validate_output(open_evidence, **open_inputs)
        open_evidence["load"]["arrival"]["offered_requests"] = 4
        with self.assertRaises(RuntimeError):
            MODULE.validate_output(open_evidence, **open_inputs)
        mutations = []
        wrong_artifact = json.loads(json.dumps(evidence))
        wrong_artifact["artifact"]["artifact_profile"] = "base"
        mutations.append(wrong_artifact)
        wrong_source = json.loads(json.dumps(evidence))
        wrong_source["artifact"]["source_commit"] = "d" * 40
        mutations.append(wrong_source)
        wrong_policy = json.loads(json.dumps(evidence))
        wrong_policy["policy"]["greed"] = 51
        mutations.append(wrong_policy)
        wrong_budget = json.loads(json.dumps(evidence))
        wrong_budget["limits"]["runtime_budget_bytes"] += 1
        mutations.append(wrong_budget)
        wrong_arrival = json.loads(json.dumps(evidence))
        wrong_arrival["load"]["arrival"]["offered_requests"] = 4
        mutations.append(wrong_arrival)
        incomplete = json.loads(json.dumps(evidence))
        incomplete["load"]["ready_after"] = 3
        mutations.append(incomplete)
        wrong_class = json.loads(json.dumps(evidence))
        wrong_class["load"]["request_classes"] = [{"name": "tiny-cpu", "started": 3, "completed": 3, "failed": 0}]
        mutations.append(wrong_class)
        wrong_class_count = json.loads(json.dumps(evidence))
        wrong_class_count["load"]["request_classes"][0].update(started=999, completed=999)
        mutations.append(wrong_class_count)
        wrong_class_totals = json.loads(json.dumps(evidence))
        wrong_class_totals["load"]["request_classes"][0].update(completed=2, failed=1)
        mutations.append(wrong_class_totals)
        wrong_oracle = json.loads(json.dumps(evidence))
        wrong_oracle["load"]["validated_results"] = 2
        mutations.append(wrong_oracle)
        wrong_latency = json.loads(json.dumps(evidence))
        wrong_latency["load"]["latency_total_ns"] = 12
        wrong_latency["load"]["latency_mean_ns"] = 4
        mutations.append(wrong_latency)
        wrong_latency_order = json.loads(json.dumps(evidence))
        wrong_latency_order["load"]["latency_samples_ns"] = [2, 1, 3]
        mutations.append(wrong_latency_order)
        for mutation in mutations:
            with self.assertRaises(RuntimeError):
                MODULE.validate_output(mutation, **validation_inputs)
        evidence["load"]["arrival"]["offered_requests"] = 4
        with self.assertRaises(RuntimeError):
            MODULE.validate_output(evidence, **validation_inputs)
        evidence["load"]["arrival"]["offered_requests"] = True
        with self.assertRaises(RuntimeError):
            MODULE.validate_output(evidence, **validation_inputs)

    def test_artifact_manifest_and_exact_validator_fail_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifact = root / "numpy.wasm"
            artifact.write_bytes(b"wasm")
            digest = MODULE.sha256_file(artifact)
            source = "e" * 40
            manifest = root / "manifest.json"
            manifest.write_text(json.dumps({
                "artifact_profile": "numpy-core",
                "artifact": {"filename": artifact.name, "sha256": digest, "size": 4},
                "build": {"repository_commit": source},
            }), encoding="utf-8")
            self.assertEqual(source, MODULE.artifact_source_identity(artifact, manifest, digest))
            manifest.write_text('{"artifact_profile":"numpy-core","artifact_profile":"base"}', encoding="utf-8")
            with self.assertRaises(RuntimeError):
                MODULE.artifact_source_identity(artifact, manifest, digest)

            schema = root / "schema.json"
            evidence = root / "evidence.json"
            schema.write_text("{}", encoding="utf-8")
            evidence.write_text("{}", encoding="utf-8")
            validator = root / "validator"
            validator.write_text("#!/bin/sh\nprintf '%s\\n' '{\"valid\":true,\"schema_version\":11,\"evidence_kind\":\"cow-pressure\"}'\nexit 0\n", encoding="utf-8")
            validator.chmod(0o700)
            completed = MODULE.validate_with_exact_binary(validator, root, schema, evidence)
            self.assertEqual(0, completed.returncode)
            validator.write_text("#!/bin/sh\nprintf 'semantic drift' >&2\nexit 7\n", encoding="utf-8")
            validator.chmod(0o700)
            with self.assertRaises(RuntimeError):
                MODULE.validate_with_exact_binary(validator, root, schema, evidence)


if __name__ == "__main__":
    unittest.main()
