import gzip
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("import-corpus.py")
SPEC = importlib.util.spec_from_file_location("import_corpus", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cannot load import-corpus.py")
importer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(importer)


class ImportCorpusTests(unittest.TestCase):
    def test_import_humaneval_builds_fixed_reference_program(self):
        row = {
            "task_id": "HumanEval/0",
            "prompt": "def add(a, b):\n",
            "canonical_solution": "    return a + b\n",
            "test": "def check(candidate):\n    assert candidate(1, 2) == 3\n",
            "entry_point": "add",
        }
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory, "HumanEval.jsonl.gz")
            output = Path(directory, "cases.jsonl")
            with gzip.open(source, "wt", encoding="utf-8") as stream:
                stream.write(json.dumps(row) + "\n")
            count = importer.import_humaneval(source, output, "deadbeef", 0)
            self.assertEqual(count, 1)
            case = json.loads(output.read_text())
            self.assertEqual(case["origin"], {"dataset": "openai/human-eval", "revision": "deadbeef", "case_id": "HumanEval/0"})
            self.assertIn("check(add)", case["source"])
            self.assertEqual(case["expected"], {"passed": True})
            compile(case["source"], "<case>", "exec")

    def test_import_bfcl_builds_manifest_program_and_exact_replay(self):
        question = {
            "id": "parallel_0",
            "question": [[{"role": "user", "content": "ignored by runtime replay"}]],
            "function": [{
                "name": "spotify.play",
                "description": "Play music",
                "parameters": {"type": "dict", "properties": {"artist": {"type": "string"}, "duration": {"type": "integer"}}, "required": ["artist", "duration"]},
            }],
        }
        answer = {
            "id": "parallel_0",
            "ground_truth": [
                {"spotify.play": {"artist": ["Taylor Swift"], "duration": [20]}},
                {"spotify.play": {"artist": ["Maroon 5"], "duration": [15]}},
            ],
        }
        with tempfile.TemporaryDirectory() as directory:
            questions = Path(directory, "questions.json")
            answers = Path(directory, "answers.json")
            output = Path(directory, "cases.jsonl")
            questions.write_text(json.dumps(question) + "\n")
            answers.write_text(json.dumps(answer) + "\n")
            count = importer.import_bfcl(questions, answers, output, "cafebabe", "parallel", 0)
            self.assertEqual(count, 1)
            case = json.loads(output.read_text())
            self.assertEqual(case["tools"][0]["name"], "bfcl/spotify.play")
            self.assertEqual(case["tools"][0]["python_path"], "bfcl.spotify.play")
            self.assertIn("bfcl.spotify.play", case["source"])
            self.assertEqual(len(case["replay"]), 2)
            self.assertEqual(case["expected"][0]["args"]["artist"], "Taylor Swift")
            compile(case["source"], "<case>", "exec")

    def test_bfcl_rejects_missing_answers_and_invalid_argument_names(self):
        base = {
            "id": "simple_0",
            "question": [],
            "function": [{"name": "f", "parameters": {"type": "dict", "properties": {}}}],
        }
        with self.assertRaisesRegex(ValueError, "missing BFCL ground truth"):
            importer.convert_bfcl_case(base, {}, "rev", "simple")
        answer = {"id": "simple_0", "ground_truth": [{"f": {"not-valid": [1]}}]}
        with self.assertRaisesRegex(ValueError, "Python keyword argument"):
            importer.convert_bfcl_case(base, answer, "rev", "simple")


if __name__ == "__main__":
    unittest.main()
