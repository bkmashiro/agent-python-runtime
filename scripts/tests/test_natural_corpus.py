import importlib.util
import json
import pathlib
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "natural-corpus.py"
SPEC = importlib.util.spec_from_file_location("natural_corpus", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
corpus = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(corpus)


def codeact_row(record_id: str, conversations: list) -> dict:
    return {"row_idx": 0, "row": {"id": record_id, "conversations": conversations}, "truncated_cells": []}


class NaturalCorpusTests(unittest.TestCase):
    def test_extracts_code_actions_and_classifies_probe_boundary(self) -> None:
        rows = [
            {"row": {"id": "record-a", "conversations": [
                {"role": "assistant", "content": "<execute>x = 1\nprint(x)</execute>"},
                {"role": "assistant", "content": "<execute>import pandas as pd\nprint(pd)</execute>"},
                {"role": "assistant", "content": "<execute>print(look())</execute>"},
            ]}}
        ]
        items = corpus.extract_codeact(rows)
        self.assertEqual(3, len(items))
        self.assertEqual("probe_candidate", items[0]["status"])
        self.assertEqual("third_party_import", items[1]["reason"])
        self.assertEqual("environment_dependency", items[2]["reason"])
        self.assertNotIn("code", items[0]["public"])
        self.assertEqual("x = 1\nprint(x)", items[0]["code"])

    def test_census_is_body_free_deterministic_and_keeps_denominators(self) -> None:
        codeact = {"rows": [
            {"row": {"id": "record-a", "conversations": [{"role": "assistant", "content": "<execute>value=2\nprint(value)</execute>"}]}},
            {"row": {"id": "record-b", "conversations": [{"role": "assistant", "content": "no action"}]}},
        ]}
        open_swe = {"rows": [
            {"row": {"instance_id": "python-a", "language": "python", "resolved": 1, "trajectory": [{"tool_calls": [{"id": "c"}]}], "metadata": {"model_patch": {"patch": "secret body"}}}},
            {"row": {"instance_id": "go-a", "language": "go", "resolved": 0, "trajectory": [{"tool_calls": []}], "metadata": {"model_patch": {"patch": "other body"}}}},
        ]}
        source_manifest = {"schema_version": "pysolate.natural-corpus-download.v1", "sources": [
            {"dataset": "xingyaoww/code-act", "config": "default", "split": "codeact", "offset": 0, "length": 2, "bytes": 10, "sha256": "sha256:" + "a" * 64, "file": "codeact-50.json", "url": "private"},
            {"dataset": "nvidia/Open-SWE-Traces", "config": "openhands", "split": "minimax_m25", "offset": 0, "length": 2, "bytes": 20, "sha256": "sha256:" + "b" * 64, "file": "open-swe-10.json", "url": "private"},
        ]}
        first, selected = corpus.build_census(source_manifest, codeact, open_swe, 8)
        second, _ = corpus.build_census(source_manifest, codeact, open_swe, 8)
        self.assertEqual(first, second)
        self.assertEqual(2, first["denominators"]["codeact_records"])
        self.assertEqual(1, first["denominators"]["codeact_actions"])
        self.assertEqual(2, first["denominators"]["open_swe_trajectories"])
        self.assertEqual({"go": 1, "python": 1}, first["open_swe"]["languages"])
        self.assertEqual(1, first["open_swe"]["tool_calls"])
        encoded = json.dumps(first, sort_keys=True)
        self.assertNotIn("secret body", encoded)
        self.assertNotIn("record-a", encoded)
        self.assertNotIn("private", encoded)
        self.assertEqual(1, len(selected["programs"]))
        self.assertIn("value=2", selected["programs"][0]["code"])

    def test_load_source_bundle_rejects_hash_and_count_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            raw = json.dumps({"rows": [{"row": {}}]}).encode()
            (root / "source.json").write_bytes(raw)
            manifest = {"schema_version": "pysolate.natural-corpus-download.v1", "sources": [{
                "dataset": "dataset/a", "config": "default", "split": "train", "offset": 0,
                "length": 1, "bytes": len(raw), "sha256": "sha256:" + "0" * 64,
                "file": "source.json", "url": "https://example.invalid",
            }]}
            (root / "download-manifest.json").write_text(json.dumps(manifest))
            with self.assertRaises(ValueError):
                corpus.load_source_bundle(root)

    def test_probe_report_is_body_free_and_records_both_lanes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            runner = root / "fake-apyrun"
            runner.write_text("#!/usr/bin/env python3\nimport json,sys\nrequest=json.load(sys.stdin)\nprint(json.dumps({'status':'ok','result':{'probe':'completed'},'metrics':{'guest_time_ms':1.5}}))\n")
            runner.chmod(0o700)
            artifact = root / "guest.wasm"; artifact.write_bytes(b"wasm")
            manifest = root / "manifest.json"; manifest.write_text("{}")
            selection = {"schema_version": corpus.PRIVATE_SCHEMA, "public_report_sha256": "sha256:" + "a" * 64, "programs": [{
                "item_id": "sha256:" + "b" * 64, "code_sha256": corpus.digest(b"value=2"), "code_bytes": 7,
                "imports": [], "status": "probe_candidate", "reason": "", "code": "value=2",
            }]}
            private, public = corpus.run_probes(selection, runner, artifact, manifest, "c" * 40)
            self.assertEqual(1, public["aggregate"]["baseline_ok"])
            self.assertEqual(1, public["aggregate"]["guest_ok"])
            self.assertTrue(public["programs"][0]["profile_bound"])
            encoded = json.dumps(public)
            self.assertNotIn("value=2", encoded)
            self.assertIn("value=2", json.dumps(private))

    def test_manifest_preserves_states_denominators_and_body_safety(self) -> None:
        codeact = [
            codeact_row("a", [{"role": "assistant", "content": "<execute>value=2</execute>"}]),
            codeact_row("b", [{"role": "assistant", "content": "<execute>print(look())</execute>"}]),
            {**codeact_row("c", [{"role": "assistant", "content": "<execute>value=3</execute>"}]), "truncated_cells": ["row.conversations"]},
        ]
        open_swe = [
            {"row_idx": 0, "row": {"instance_id": "py", "language": "python", "resolved": 1, "trajectory": []}, "truncated_cells": []},
            {"row_idx": 1, "row": {"instance_id": "unknown", "language": "python", "resolved": -1, "trajectory": []}, "truncated_cells": []},
            {"row_idx": 2, "row": {"instance_id": "go", "language": "go", "resolved": 0, "trajectory": []}, "truncated_cells": []},
        ]
        sources = {"schema_version": corpus.SOURCE_SCHEMA, "sources": [
            {"dataset": "xingyaoww/code-act", "config": "default", "split": "codeact", "offset": 0, "length": 3, "bytes": 1, "sha256": "sha256:" + "1" * 64, "dataset_revision_observed": "a" * 40, "license_id": "apache-2.0"},
            {"dataset": "nvidia/Open-SWE-Traces", "config": "openhands", "split": "minimax_m25", "offset": 0, "length": 3, "bytes": 2, "sha256": "sha256:" + "2" * 64, "dataset_revision_observed": "b" * 40, "license_id": "cc-by-4.0"},
        ]}
        manifest = corpus.build_corpus_manifest(codeact, open_swe, sources, set())
        corpus.validate_corpus_manifest(manifest)
        self.assertEqual(["apache-2.0", "cc-by-4.0"], [source["license_id"] for source in manifest["sources"]])
        self.assertEqual({"included": 2, "rejected": 2, "unclassifiable": 1, "truncated": 1}, manifest["denominator"]["states"])
        encoded = json.dumps(manifest)
        self.assertNotIn("value=2", encoded)
        self.assertNotIn("instance_id", encoded)

        dropped = json.loads(json.dumps(manifest))
        removed = next(item for item in dropped["items"] if item["dataset"] == "nvidia/Open-SWE-Traces")
        dropped["items"].remove(removed); dropped["denominator"]["items"] -= 1; dropped["denominator"]["open_swe_trajectories"] -= 1; dropped["denominator"]["states"][removed["state"]] -= 1
        dropped["identity"] = corpus._manifest_identity(dropped)
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(dropped)

        manifest["items"][0]["state"] = "unknown"
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(manifest)

    def test_manifest_rejects_tamper_duplicate_and_private_path(self) -> None:
        codeact = [codeact_row("a", [{"role": "assistant", "content": "<execute>value=2</execute>"}])]
        sources = {"schema_version": corpus.SOURCE_SCHEMA, "sources": [
            {"dataset": "xingyaoww/code-act", "config": "default", "split": "codeact", "offset": 0, "length": 1, "bytes": 1, "sha256": "sha256:" + "1" * 64, "dataset_revision_observed": "a" * 40, "license_id": "apache-2.0"},
            {"dataset": "nvidia/Open-SWE-Traces", "config": "openhands", "split": "minimax_m25", "offset": 0, "length": 0, "bytes": 0, "sha256": "sha256:" + "2" * 64, "dataset_revision_observed": "b" * 40, "license_id": "cc-by-4.0"},
        ]}
        manifest = corpus.build_corpus_manifest(codeact, [], sources, set())
        duplicate = json.loads(json.dumps(manifest)); duplicate["items"].append(duplicate["items"][0]); duplicate["denominator"]["items"] += 1; duplicate["denominator"]["states"]["included"] += 1; duplicate["identity"] = corpus._manifest_identity(duplicate)
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(duplicate)
        leaked = json.loads(json.dumps(manifest)); leaked["items"][0]["reason"] = "/Users/yuzhe/private"; leaked["identity"] = corpus._manifest_identity(leaked)
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(leaked)
        tampered = json.loads(json.dumps(manifest)); tampered["items"][0]["source_bytes"] += 1
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(tampered)
        unknown_class = json.loads(json.dumps(manifest)); unknown_class["items"][0]["privacy_class"] = "unknown"; unknown_class["identity"] = corpus._manifest_identity(unknown_class)
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(unknown_class)
        rebound = json.loads(json.dumps(manifest)); rebound["items"][0]["source_index"] += 1; rebound["identity"] = corpus._manifest_identity(rebound)
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(rebound)
        unbounded = json.loads(json.dumps(manifest)); unbounded["sources"][0]["bytes"] = corpus.MAX_SOURCE_BYTES + 1; unbounded["identity"] = corpus._manifest_identity(unbounded)
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(unbounded)
        unknown_license = json.loads(json.dumps(manifest)); unknown_license["sources"][0]["license_id"] = "unknown"; unknown_license["identity"] = corpus._manifest_identity(unknown_license)
        with self.assertRaises(ValueError):
            corpus.validate_corpus_manifest(unknown_license)

    def test_opportunity_report_rejects_sequential_duplicates_as_sharing_evidence(self) -> None:
        codeact = [codeact_row("a", [
            {"role": "assistant", "content": "<execute>value=2</execute>"},
            {"role": "assistant", "content": "<execute>value=2</execute>"},
        ])]
        bash = {"function": {"name": "execute_bash", "arguments": json.dumps({"command": "python test.py"})}}
        open_swe = [{"row_idx": 0, "row": {"instance_id": "py", "language": "python", "resolved": 1, "trajectory": [
            {"role": "assistant", "tool_calls": [bash]}, {"role": "assistant", "tool_calls": [bash]},
        ]}, "truncated_cells": []}]
        report = corpus.build_opportunity_report("sha256:" + "a" * 64, codeact, open_swe)
        corpus.validate_opportunity_report(report)
        self.assertEqual(1, report["codeact"]["exact_duplicate_instances"])
        self.assertEqual(1, report["open_swe"]["sequential_duplicate_bash_calls"])
        self.assertEqual(0, report["open_swe"]["parallel_bash_messages"])
        self.assertEqual("insufficient_evidence", report["sharing_gate"]["verdict"])
        report["sharing_gate"]["verdict"] = "candidate"
        with self.assertRaises(ValueError):
            corpus.validate_opportunity_report(report)

    def test_generate_contract_writes_valid_joined_artifacts(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); root.chmod(0o700)
            codeact_value = {"rows": [codeact_row("a", [{"role": "assistant", "content": "<execute>value=2</execute>"}])]}
            open_swe_value = {"rows": []}
            sources = []
            for filename, dataset, config, split, value in [
                ("codeact-50.json", "xingyaoww/code-act", "default", "codeact", codeact_value),
                ("open-swe-10.json", "nvidia/Open-SWE-Traces", "openhands", "minimax_m25", open_swe_value),
            ]:
                raw = corpus.canonical_json(value); (root / filename).write_bytes(raw)
                license_id = "apache-2.0" if dataset == "xingyaoww/code-act" else "cc-by-4.0"
                sources.append({"dataset": dataset, "config": config, "split": split, "offset": 0, "length": len(value["rows"]), "url": "test", "bytes": len(raw), "sha256": corpus.digest(raw), "file": filename, "dataset_revision_observed": "a" * 40, "license_id": license_id})
            (root / "download-manifest.json").write_bytes(corpus.canonical_json({"schema_version": corpus.SOURCE_SCHEMA, "sources": sources}))
            probe = root / "probe.json"; probe.write_bytes(corpus.canonical_json({"schema_version": corpus.PROBE_PUBLIC_SCHEMA, "programs": []}))
            manifest_output = root / "manifest-output.json"; opportunity_output = root / "opportunity-output.json"
            corpus.generate_contract(root, probe, manifest_output, opportunity_output)
            manifest = json.loads(manifest_output.read_text()); opportunity = json.loads(opportunity_output.read_text())
            corpus.validate_corpus_manifest(manifest)
            self.assertEqual(manifest["identity"], opportunity["manifest_identity"])


if __name__ == "__main__":
    unittest.main()
