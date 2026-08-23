import hashlib
import json
import unittest

from agent_runtime.source_pass import (
    emit_source_pass_patch_request_json,
    validate_source_pass_execution_request,
)


def digest(character):
    return "sha256:" + character * 64


def request(source):
    return json.dumps(
        {
            "pass_name": "pure_scalar_cse",
            "pass_version": "pysolate.pure-scalar-cse-pass.v1",
            "registration_sha256": digest("a"),
            "source": source,
        },
        sort_keys=True,
        separators=(",", ":"),
    )


class SourcePassTests(unittest.TestCase):
    def test_pure_scalar_cse_reuses_adjacent_expression_without_shifting_lines(self):
        source = "seed = 7\nleft = seed * seed + 3\nright = seed * seed + 3\nresult = [left, right]\n"
        raw = emit_source_pass_patch_request_json(request(source))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        self.assertEqual(1, patch["replacement_count"])
        self.assertEqual(source.count("\n"), patch["derived_source"].count("\n"))
        self.assertEqual(len(source.encode()), len(patch["derived_source"].encode()))
        self.assertIn("right = left", patch["derived_source"])
        tree = validate_source_pass_execution_request(source, raw)
        host_order = [
            "schema_version", "status", "pass_name", "pass_version", "registration_sha256",
            "original_source_sha256", "original_ast_sha256", "derived_source",
            "derived_source_sha256", "derived_ast_sha256", "replacement_count",
        ]
        host_raw = json.dumps({key: patch[key] for key in host_order}, separators=(",", ":")) + "\n"
        validate_source_pass_execution_request(source, host_raw)
        namespace = {}
        exec(compile(tree, "<agent-run>", "exec"), namespace, namespace)
        self.assertEqual([52, 52], namespace["result"])

    def test_non_scalar_or_non_adjacent_repetition_is_not_applicable(self):
        cases = [
            "left = work()\nright = work()\nresult = right\n",
            "seed = 7\nleft = seed // 2\nright = seed // 2\nresult = right\n",
            "seed = 7\nleft = seed + 'x'\nright = seed + 'x'\nresult = right\n",
            "seed = []\nleft = seed\nright = seed\nresult = right\n",
            "seed = 7\nleft = seed * seed\nmarker = 1\nright = seed * seed\nresult = right\n",
            "seed = 7\nleft = seed * seed\nseed = 8\nright = seed * seed\nresult = right\n",
            "seed = 7\nleft = seed * seed\nif True:\n    pass\nright = seed * seed\nresult = right\n",
        ]
        for source in cases:
            with self.subTest(source=source):
                patch = json.loads(emit_source_pass_patch_request_json(request(source)))
                self.assertEqual("not_applicable", patch["status"])
                self.assertEqual(0, patch["replacement_count"])
                self.assertEqual("", patch["derived_source"])

    def test_does_not_reuse_after_the_first_assignment_changes_an_input(self):
        source = "a = 1\na = a + 1\nb = a + 1\nresult = [a, b]\n"
        patch = json.loads(emit_source_pass_patch_request_json(request(source)))
        self.assertEqual("not_applicable", patch["status"])

    def test_unknown_call_clears_known_dependency_values(self):
        source = (
            "def mutate():\n"
            "    global seed\n"
            "    seed = 2\n"
            "seed = 1\n"
            "x = mutate()\n"
            "seed = seed * seed\n"
            "b = seed * seed\n"
            "result = [seed, b]\n"
        )
        patch = json.loads(emit_source_pass_patch_request_json(request(source)))
        self.assertEqual("not_applicable", patch["status"])

    def test_utf8_identifier_is_a_valid_reuse_target(self):
        source = "seed = 1\né = seed + 1\nê = seed + 1\nresult = [é, ê]\n"
        raw = emit_source_pass_patch_request_json(request(source))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        tree = validate_source_pass_execution_request(source, raw)
        original_namespace = {}
        derived_namespace = {}
        exec(source, original_namespace, original_namespace)
        exec(compile(tree, "<agent-run>", "exec"), derived_namespace, derived_namespace)
        self.assertEqual(original_namespace["result"], derived_namespace["result"])

    def test_execution_rederives_patch_from_original_source(self):
        source = "seed = 7\nleft = seed * seed\nright = seed * seed\nresult = right\n"
        patch = json.loads(emit_source_pass_patch_request_json(request(source)))
        patch["derived_source"] = patch["derived_source"].replace("right = left", "right = seed")
        patch["derived_source_sha256"] = "sha256:" + hashlib.sha256(patch["derived_source"].encode()).hexdigest()
        tampered = json.dumps(patch, sort_keys=True, separators=(",", ":")) + "\n"
        with self.assertRaisesRegex(ValueError, "does not match"):
            validate_source_pass_execution_request(source, tampered)


if __name__ == "__main__":
    unittest.main()
