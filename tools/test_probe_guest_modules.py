import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("probe-guest-modules.py")
SPEC = importlib.util.spec_from_file_location("probe_guest_modules", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cannot load probe-guest-modules.py")
probe = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(probe)


class ProfileTests(unittest.TestCase):
    def test_committed_profile_generates_every_non_workspace_probe(self):
        profile = probe.load_profile(probe.DEFAULT_PROFILE)
        source = probe.guest_source(profile)
        compile(source, "<qualification>", "exec")
        for item in profile["qualification"]:
            if item["operation"] not in probe.ACCEPTANCE_OPERATIONS:
                self.assertIn(repr(item["name"]), source)
        self.assertNotIn("workspace_edit_acceptance", source)
        self.assertNotIn("common_usecases_acceptance", source)

    def test_unknown_operation_and_duplicate_name_fail_closed(self):
        base = {
            "schema_version": 1,
            "profile": "agent-core",
            "qualification": [
                {"name": "same", "operation": "json_roundtrip"},
                {"name": "same", "operation": "unknown"},
            ],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory, "profile.json")
            path.write_text(json.dumps(base), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "duplicate qualification name"):
                probe.load_profile(path)
            base["qualification"][1]["name"] = "other"
            path.write_text(json.dumps(base), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "unknown qualification operation"):
                probe.load_profile(path)


if __name__ == "__main__":
    unittest.main()
