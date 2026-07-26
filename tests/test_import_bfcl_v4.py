import importlib.util
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "eval" / "agentic" / "scripts" / "import_bfcl_v4.py"


def load_importer():
    spec = importlib.util.spec_from_file_location("import_bfcl_v4", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load BFCL importer")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class BFCLImporterOracleTests(unittest.TestCase):
    def setUp(self):
        self.importer = load_importer()

    def test_normalizes_stateful_call_to_bounded_json(self):
        self.assertEqual(
            {"name": "cd", "arguments": {"folder": "VisionX"}},
            self.importer.parse_stateful_call("cd(folder='VisionX')", {"cd"}),
        )
        self.assertEqual(
            {
                "name": "echo",
                "arguments": {
                    "content": "Create a file named '79.pdf'.",
                    "file_name": "79.pdf",
                },
            },
            self.importer.parse_stateful_call(
                "echo(content=\"Create a file named '79.pdf'.\",file_name='79.pdf')",
                {"echo"},
            ),
        )
        self.assertEqual(
            {"name": "du", "arguments": {"human_readable": True}},
            self.importer.parse_stateful_call("du(human_readable=True)", {"du"}),
        )

    def test_rejects_nonliteral_or_ambiguous_stateful_calls(self):
        invalid = [
            "cd('VisionX')",
            "cd(folder=get_name())",
            "fs.cd(folder='VisionX')",
            "cd(**{'folder': 'VisionX'})",
            "cd(folder='one', folder='two')",
            "unknown(value=1)",
            "cd(folder=['VisionX'])",
        ]
        for source in invalid:
            with self.subTest(source=source):
                with self.assertRaises(ValueError):
                    self.importer.parse_stateful_call(source, {"cd"})

    def test_normalizes_turns_and_rejects_oversized_trace(self):
        normalized = self.importer.normalize_stateful_trace(
            [["pwd()"], ["touch(file_name='x.txt')"]], {"pwd", "touch"}
        )
        self.assertEqual(
            [
                [{"name": "pwd", "arguments": {}}],
                [{"name": "touch", "arguments": {"file_name": "x.txt"}}],
            ],
            normalized,
        )
        with self.assertRaises(ValueError):
            self.importer.normalize_stateful_trace([["pwd()"] * 129], {"pwd"})


if __name__ == "__main__":
    unittest.main()
