import importlib.util
import json
import pathlib
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-qualification.py"
SPEC = importlib.util.spec_from_file_location("tau2_qualification", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
qualification = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(qualification)


TOOLS_SOURCE = '''
class ToolType:
    READ = "read"
    WRITE = "write"
    GENERIC = "generic"

def is_tool(value):
    return lambda function: function

@is_tool(ToolType.READ)
def lookup(value):
    return value

@is_tool(ToolType.WRITE)
def update(value):
    return value

@is_tool(ToolType.GENERIC)
def transfer():
    return None
'''


class Tau2QualificationTests(unittest.TestCase):
    def make_checkout(self, root: pathlib.Path) -> None:
        (root / "src/tau2/domains/airline").mkdir(parents=True)
        (root / "data/tau2/domains/airline").mkdir(parents=True)
        (root / "src/tau2/domains/airline/tools.py").write_text(TOOLS_SOURCE)
        tasks = [
            {"id": "read", "evaluation_criteria": {"actions": [{"name": "lookup"}], "reward_basis": ["DB", "COMMUNICATE"], "communicate_info": ["answer"]}},
            {"id": "write", "evaluation_criteria": {"actions": [{"name": "lookup"}, {"name": "update"}], "reward_basis": ["DB"], "communicate_info": []}},
            {"id": "generic", "evaluation_criteria": {"actions": [{"name": "transfer"}], "reward_basis": ["DB"], "communicate_info": []}},
            {"id": "none", "evaluation_criteria": {"actions": [], "reward_basis": ["DB"], "communicate_info": []}},
            {"id": "unknown", "evaluation_criteria": {"actions": [{"name": "missing"}], "reward_basis": ["DB"], "communicate_info": []}},
        ]
        (root / "data/tau2/domains/airline/tasks.json").write_text(json.dumps(tasks))
        (root / "pyproject.toml").write_text('[project]\nversion = "1.0.1"\nlicense = "MIT"\n')
        (root / "LICENSE").write_text("MIT License\n")

    def test_audit_classifies_reference_actions_without_executing_upstream(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.make_checkout(root)
            report = qualification.audit_checkout(root, "a" * 40, ["airline"])
        domain = report["domains"]["airline"]
        self.assertEqual({"READ": 1, "WRITE": 1, "GENERIC": 1}, domain["tool_definitions"])
        self.assertEqual(
            {
                "read_only_reference": 1,
                "write_reference": 1,
                "generic_or_mixed_reference": 1,
                "no_reference_action": 1,
                "unclassifiable_reference": 1,
            },
            domain["task_reference_classes"],
        )
        self.assertEqual(5, domain["tasks"])

    def test_report_is_body_safe_deterministic_and_identity_bound(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.make_checkout(root)
            first = qualification.audit_checkout(root, "b" * 40, ["airline"])
            second = qualification.audit_checkout(root, "b" * 40, ["airline"])
        self.assertEqual(first, second)
        self.assertEqual("pysolate.tau2-qualification.v1", first["schema_version"])
        self.assertEqual("b" * 40, first["source"]["revision"])
        self.assertEqual("1.0.1", first["source"]["version"])
        self.assertEqual("MIT", first["source"]["license"])
        encoded = qualification.canonical_json(first).decode()
        self.assertNotIn("answer", encoded)
        self.assertNotIn("lookup", encoded)
        self.assertNotIn(str(root), encoded)
        self.assertTrue(first["identity"].startswith("sha256:"))


if __name__ == "__main__":
    unittest.main()
