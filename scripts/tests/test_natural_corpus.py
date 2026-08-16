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


if __name__ == "__main__":
    unittest.main()
