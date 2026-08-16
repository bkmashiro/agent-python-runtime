import copy
import importlib.util
import pathlib
import unittest


SCRIPT = pathlib.Path(__file__).parents[1] / "attrs-770-profile.py"
SPEC = importlib.util.spec_from_file_location("attrs_770_profile", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
profile = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(profile)


class Attrs770ProfileReportTests(unittest.TestCase):
    def valid_report(self):
        return {
            "schema_version": profile.SCHEMA,
            "status": "supported",
            "qualification": {"phase": "restricted_agent_body"},
            "runs": {"natural_oracle": {"runs": 2, "passed": 2, "distinct_request_identities": True}},
            "verification": {
                "canonical_source_set_bound": True,
                "final_cache_bundle_reverified": True,
                "http_service_loaded_verified_identity": True,
                "post_copy_package_tree_reverified": True,
                "python_go_parser_fail_closed_parity": True,
            },
            "decision": {"profile_supported": True},
        }

    def test_accepts_minimal_valid_body_safe_report(self):
        profile.validate_report(self.valid_report())

    def test_rejects_verdict_and_qualification_phase_drift(self):
        for path, value in (("decision", False), ("qualification", "import_preamble")):
            report = copy.deepcopy(self.valid_report())
            if path == "decision":
                report[path]["profile_supported"] = value
            else:
                report[path]["phase"] = value
            with self.assertRaises(ValueError):
                profile.validate_report(report)

    def test_rejects_private_path_and_body_markers(self):
        for marker in profile.PRIVATE_MARKERS:
            report = self.valid_report()
            report["leak"] = marker
            with self.assertRaises(ValueError):
                profile.validate_report(report)


if __name__ == "__main__":
    unittest.main()
