import importlib.util
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
TOOL_PATH = ROOT / "guest" / "build" / "import_qualification.py"


def load_tool():
    spec = importlib.util.spec_from_file_location("import_qualification", TOOL_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load import qualification tool")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ImportQualificationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tool = load_tool()

    def qualified_responses(self, profile="base"):
        responses = []
        for probe in self.tool.probe_specs(profile):
            responses.append({
                "status": "ok",
                "result": {
                    "schema_version": 1,
                    "artifact_profile": profile,
                    "probe": "guest-import-exec-v1",
                    "implementation": "cpython",
                    "python_version": "3.14.0",
                    "name": probe["name"],
                    "operation": probe["operation"],
                    "status": "qualified",
                    "error": "",
                },
            })
        return responses

    def test_requests_are_one_module_per_guest_and_profile_specific(self):
        requests = self.tool.build_requests("numpy-core")
        probes = self.tool.probe_specs("numpy-core")
        self.assertEqual(len(probes), len(requests))
        self.assertEqual(sorted(row["inputs"]["module"] for row in requests), [row["name"] for row in probes])
        self.assertEqual(len(requests), len({row["run_id"] for row in requests}))
        for request in requests:
            self.assertEqual("numpy-core", request["inputs"]["artifact_profile"])
            self.assertNotIn("compatibility", request)
            self.assertIn("guest-import-exec-v1", request["code"])
            compile(request["code"], "<qualification-probe>", "exec")

    def test_extract_builds_sorted_qualified_catalog(self):
        catalog = self.tool.extract_qualification(self.qualified_responses(), "base")
        self.assertEqual(1, catalog["schema_version"])
        self.assertEqual("guest-import-exec-v1", catalog["probe"])
        self.assertEqual("cpython", catalog["implementation"])
        self.assertEqual("3.14.0", catalog["python_version"])
        self.assertEqual(
            [row["name"] for row in self.tool.probe_specs("base")],
            catalog["qualified_roots"],
        )
        self.assertTrue(all(row["status"] == "qualified" for row in catalog["results"]))

    def test_optional_failure_is_preserved_but_not_qualified(self):
        responses = self.qualified_responses()
        optional = next(
            row for row in responses
            if row["result"]["name"] not in self.tool.required_roots("base")
        )
        optional["result"]["status"] = "operation_failed"
        optional["result"]["error"] = "ValueError"
        catalog = self.tool.extract_qualification(responses, "base")
        self.assertNotIn(optional["result"]["name"], catalog["qualified_roots"])
        result = next(row for row in catalog["results"] if row["name"] == optional["result"]["name"])
        self.assertEqual("operation_failed", result["status"])
        self.assertEqual("ValueError", result["error"])

    def test_required_root_must_qualify(self):
        responses = self.qualified_responses()
        required = next(row for row in responses if row["result"]["name"] == "json")
        required["result"]["status"] = "import_failed"
        required["result"]["error"] = "ImportError"
        with self.assertRaisesRegex(ValueError, "required profile import root is not qualified"):
            self.tool.extract_qualification(responses, "base")

    def test_missing_duplicate_and_identity_drift_fail_closed(self):
        responses = self.qualified_responses()
        for name, mutate in {
            "missing": lambda rows: rows.pop(),
            "duplicate": lambda rows: rows.append(rows[0]),
            "profile": lambda rows: rows[0]["result"].update(artifact_profile="numpy-core"),
            "operation": lambda rows: rows[0]["result"].update(operation="different"),
            "version": lambda rows: rows[0]["result"].update(python_version="3.13.0"),
        }.items():
            with self.subTest(name=name):
                candidate = [
                    {"status": row["status"], "result": dict(row["result"])}
                    for row in responses
                ]
                mutate(candidate)
                with self.assertRaises(ValueError):
                    self.tool.extract_qualification(candidate, "base")


if __name__ == "__main__":
    unittest.main()
