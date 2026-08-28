import ast
import hashlib
import json
import unittest

from agent_runtime import source_pass
from agent_runtime.source_pass import (
    emit_source_pass_patch_request_json,
    validate_source_pass_execution_request,
)


def digest(character):
    return "sha256:" + character * 64


def request(source, pass_name="pure_scalar_cse", pass_version="pysolate.pure-scalar-cse-pass.v1"):
    return json.dumps(
        {
            "pass_name": pass_name,
            "pass_version": pass_version,
            "registration_sha256": digest("a"),
            "source": source,
        },
        sort_keys=True,
        separators=(",", ":"),
    )


def capability_request(source, projections):
    return plm_capability_request(source, projections)


def plm_capability_request(source, projections):
    value = json.loads(request(
        source,
        "plm_capability_calls",
        "pysolate.plm-capability-calls-pass.v1",
    ))
    value["capability_projections"] = projections
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


CAPABILITY_PROJECTIONS = [
    {
        "capability": "tools.get",
        "module": "tools",
        "method": "get",
        "arguments": ["key"],
        "result_field": "value",
    },
    {
        "capability": "tools.price",
        "module": "tools",
        "method": "price",
        "arguments": ["value"],
        "result_field": "quote",
    },
]


class SourcePassTests(unittest.TestCase):
    def assert_text_order(self, text, *parts):
        positions = [text.index(part) for part in parts]
        self.assertEqual(sorted(positions), positions)

    def selected_plm_source(self, source, raw):
        tree = validate_source_pass_execution_request(source, raw)
        return ast.unparse(tree)

    def test_source_patch_v3_uses_verifiable_source_identities_only(self):
        raw = emit_source_pass_patch_request_json(
            plm_capability_request('value = tools.get("alpha")\nresult = value\n', CAPABILITY_PROJECTIONS)
        )
        patch = json.loads(raw)
        self.assertEqual("pysolate.source-pass-patch.v3", patch["schema_version"])
        self.assertNotIn("original_ast_sha256", patch)
        self.assertNotIn("derived_ast_sha256", patch)
        self.assertNotIn("derived_source", patch)
        self.assertNotIn("derived_source_sha256", patch)

    def test_same_guest_selection_does_not_replay_plm_transform(self):
        source = 'value = tools.get("alpha")\nresult = value\n'
        key = ("plm_capability_calls", "pysolate.plm-capability-calls-pass.v1")
        transform = source_pass._TRANSFORMS[key]
        calls = 0

        def counted_transform(*args):
            nonlocal calls
            calls += 1
            return transform(*args)

        source_pass._TRANSFORMS[key] = counted_transform
        try:
            raw = emit_source_pass_patch_request_json(
                plm_capability_request(source, CAPABILITY_PROJECTIONS)
            )
            tree = validate_source_pass_execution_request(source, raw)
        finally:
            source_pass._TRANSFORMS[key] = transform

        self.assertEqual(1, calls)
        self.assertIsNotNone(tree)

    def test_same_guest_selection_rejects_a_changed_plm_patch(self):
        source = 'value = tools.get("alpha")\nresult = value\n'
        patch = json.loads(emit_source_pass_patch_request_json(
            plm_capability_request(source, CAPABILITY_PROJECTIONS)
        ))
        patch["replacement_count"] += 1
        tampered = json.dumps(patch, sort_keys=True, separators=(",", ":"))

        with self.assertRaisesRegex(ValueError, "does not match"):
            validate_source_pass_execution_request(source, tampered)

    def test_plm_capability_calls_emits_prepare_and_original_point_linearize(self):
        source = (
            'a = tools.get("alpha")\n'
            'x = a + 1\n'
            'independent = 3 * 4\n'
            'b = tools.price(x)\n'
            'result = [a, b, independent]\n'
        )
        raw = emit_source_pass_patch_request_json(plm_capability_request(source, CAPABILITY_PROJECTIONS))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        self.assertEqual(2, patch["replacement_count"])
        derived = self.selected_plm_source(source, raw)
        self.assertEqual(2, derived.count("_pysolate_plm_prepare"))
        self.assertEqual(2, derived.count("_pysolate_plm_linearize"))
        self.assert_text_order(
            derived,
            "_pysolate_plm_prepare('slot-s1",
            "_pysolate_plm_linearize('slot-s1",
            "_pysolate_plm_prepare('slot-s4",
            "independent = 3 * 4",
            "_pysolate_plm_linearize('slot-s4",
        )
        self.assertIn("{'value': x}", derived)
        self.assertNotIn("_pysolate_call_issue", derived)
        self.assertNotIn("_pysolate_call_collect", derived)

    def test_plm_capability_calls_preserves_branch_loop_and_unsupported_fallback(self):
        for source in (
            'if inputs["take"]:\n    value = tools.get("alpha")\n    result = value\nelse:\n    result = 0\n',
            'values = []\nfor item in inputs["items"]:\n    value = tools.get(item)\n    values.append(value)\nresult = values\n',
        ):
            raw = emit_source_pass_patch_request_json(plm_capability_request(source, CAPABILITY_PROJECTIONS))
            patch = json.loads(raw)
            self.assertEqual("applied", patch["status"])
            derived = self.selected_plm_source(source, raw)
            self.assertIn("_pysolate_plm_prepare", derived)
            self.assertIn("_pysolate_plm_linearize", derived)
        unsupported = 'value = tools.get(make_key())\nresult = value\n'
        patch = json.loads(emit_source_pass_patch_request_json(plm_capability_request(unsupported, CAPABILITY_PROJECTIONS)))
        self.assertEqual("not_applicable", patch["status"])
        self.assertNotIn("derived_source", patch)

    def test_plm_capability_calls_issues_runtime_dependency_after_value_is_ready(self):
        source = (
            'a = tools.get("alpha")\n'
            'x = a + 1\n'
            'independent = 3 * 4\n'
            'b = tools.price(x)\n'
            'result = [b, independent]\n'
        )
        raw = emit_source_pass_patch_request_json(capability_request(source, CAPABILITY_PROJECTIONS))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        self.assertEqual(2, patch["replacement_count"])
        derived = self.selected_plm_source(source, raw)
        self.assert_text_order(
            derived,
            "_pysolate_plm_prepare('slot-s1",
            "_pysolate_plm_linearize('slot-s1",
            "_pysolate_plm_prepare('slot-s4",
            "independent = 3 * 4",
            "_pysolate_plm_linearize('slot-s4",
        )

    def test_plm_capability_calls_rejects_semicolon_siblings(self):
        for source in (
            'values.append("before"); value = tools.get("alpha")\nresult = value\n',
            'value = tools.get("alpha"); values.append("after")\nresult = value\n',
            'if inputs["take"]: value = tools.get("alpha")\nresult = value\n',
            'for item in inputs["items"]: value = tools.get(item)\nresult = value\n',
        ):
            patch = json.loads(emit_source_pass_patch_request_json(
                plm_capability_request(source, CAPABILITY_PROJECTIONS)
            ))
            self.assertEqual("not_applicable", patch["status"])
            self.assertNotIn("derived_source", patch)

    def test_plm_capability_calls_stage_independent_literals_before_linearize(self):
        source = (
            'a = tools.get("alpha")\n'
            'b = tools.get("beta")\n'
            'result = [a, b]\n'
        )
        raw = emit_source_pass_patch_request_json(capability_request(source, CAPABILITY_PROJECTIONS))
        patch = json.loads(raw)
        derived = self.selected_plm_source(source, raw)
        self.assertEqual("applied", patch["status"])
        self.assert_text_order(
            derived,
            "_pysolate_plm_prepare('slot-s1",
            "_pysolate_plm_prepare('slot-s2",
            "_pysolate_plm_linearize('slot-s1",
            "_pysolate_plm_linearize('slot-s2",
        )

    def test_plm_capability_call_uses_definition_before_later_redefinition(self):
        source = (
            'key = "alpha"\n'
            "value = tools.get(key)\n"
            'key = "beta"\n'
            "result = value\n"
        )
        raw = emit_source_pass_patch_request_json(capability_request(source, CAPABILITY_PROJECTIONS))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        derived = self.selected_plm_source(source, raw)
        self.assert_text_order(
            derived,
            "key = 'alpha'",
            "_pysolate_plm_prepare",
            "_pysolate_plm_linearize",
            "key = 'beta'",
        )

    def test_plm_capability_calls_preserves_branch_and_loop_activation(self):
        branch = (
            'if inputs["take"]:\n'
            '    value = tools.get("alpha")\n'
            '    result = value\n'
            'else:\n'
            '    result = 0\n'
        )
        loop = (
            'result = []\n'
            'for key in inputs["keys"]:\n'
            '    value = tools.get(key)\n'
            '    result.append(value)\n'
        )
        for source in (branch, loop):
            with self.subTest(source=source):
                raw = emit_source_pass_patch_request_json(capability_request(source, CAPABILITY_PROJECTIONS))
                patch = json.loads(raw)
                self.assertEqual("applied", patch["status"])
                self.assertEqual(1, patch["replacement_count"])
                self.assertIn("_pysolate_plm_linearize", self.selected_plm_source(source, raw))

    def test_plm_capability_calls_rejects_opaque_argument_evaluation(self):
        source = 'value = tools.get(make_key())\nresult = value\n'
        patch = json.loads(emit_source_pass_patch_request_json(capability_request(source, CAPABILITY_PROJECTIONS)))
        self.assertEqual("not_applicable", patch["status"])
        self.assertNotIn("derived_source", patch)

    def test_plm_capability_calls_rejects_compiled_code_observation(self):
        source = 'value = tools.get("alpha")\ntry:\n    1 / 0\nexcept Exception as error:\n    result = error.__traceback__.tb_frame.f_code.co_code.hex()\n'
        patch = json.loads(emit_source_pass_patch_request_json(capability_request(source, CAPABILITY_PROJECTIONS)))
        self.assertEqual("not_applicable", patch["status"])

    def test_plm_capability_calls_rejects_receiver_drift_and_dynamic_introspection(self):
        cases = (
            'tools = None\nvalue = tools.get("alpha")\nresult = value\n',
            'tools.get = lambda key: {"value": "local"}\nvalue = tools.get("alpha")\nresult = value\n',
            'from other import tools\nvalue = tools.get("alpha")\nresult = value\n',
            'tools.__dict__["get"] = lambda key: {"value": "local"}\nvalue = tools.get("alpha")\nresult = value\n',
            'import sys\nsys.settrace(lambda *args: None)\nvalue = tools.get("alpha")\nresult = value\n',
            'value = tools.get("alpha")\nresult = getattr(getattr(globals()["_pysolate_agent_main"], "__code__"), "co_code")\n',
        )
        for source in cases:
            with self.subTest(source=source):
                patch = json.loads(emit_source_pass_patch_request_json(capability_request(source, CAPABILITY_PROJECTIONS)))
                self.assertEqual("not_applicable", patch["status"])

    def test_data_local_numpy_sum_emits_one_value_slot_materialization(self):
        source = "import io\nimport numpy as np\ndataset = np.load(io.BytesIO(open('/workspace/input.npy', 'rb').read()), allow_pickle=False)\nresult = int(dataset.sum())\n"
        raw = request(source, "data_local_numpy_sum", "pysolate.data-local-numpy-sum-pass.v2")
        patch = json.loads(emit_source_pass_patch_request_json(raw))
        self.assertEqual("applied", patch["status"])
        self.assertEqual("pass\npass\npass\nresult = _pysolate_materialize_slot('slot-numpy-sum-v1')\n", patch["derived_source"])
        tree = validate_source_pass_execution_request(source, json.dumps(patch, separators=(",", ":")))
        self.assertIsNotNone(tree)

    def test_data_local_numpy_sum_rejects_variants_without_rewrite(self):
        variants = (
            "import io\nimport numpy as np\ndataset = np.load(io.BytesIO(open(path, 'rb').read()), allow_pickle=False)\nresult = int(dataset.sum())\n",
            "import io\nimport numpy as np\ndataset = np.load(io.BytesIO(open('/workspace/input.npy', 'r').read()), allow_pickle=False)\nresult = int(dataset.sum())\n",
            "import io as bytes_io\nimport numpy as np\ndataset = np.load(bytes_io.BytesIO(open('/workspace/input.npy', 'rb').read()), allow_pickle=False)\nresult = int(dataset.sum())\n",
            "import io\nimport numpy as np\ndataset = np.load(io.BytesIO(open('/workspace/input.npy', 'rb').read()), allow_pickle=True)\nresult = int(dataset.sum())\n",
            "import io\nimport numpy as np\nraw = open('/workspace/input.npy', 'rb').read()\ndataset = np.load(io.BytesIO(raw), allow_pickle=False)\nresult = int(dataset.sum())\n",
            "import numpy as np\ndataset = np.load(path, allow_pickle=False)\nresult = int(dataset.sum())\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy')\nresult = int(dataset.sum())\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=True)\nresult = int(dataset.sum())\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nresult = dataset.sum()\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nresult = int(dataset.mean())\n",
            "import numpy as numpy\ndataset = numpy.load('/workspace/input.npy', allow_pickle=False)\nresult = int(dataset.sum())\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\ndataset[0] = 0\nresult = int(dataset.sum())\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nresult = int(dataset[dataset > 0].sum())\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nresult = int(sum(map(lambda item: item, dataset)))\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nresult = locals()\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nraise RuntimeError('visible')\n",
            "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nprint(dataset.shape)\nresult = int(dataset.sum())\n",
        )
        for source in variants:
            with self.subTest(source=source):
                raw_request = request(
                    source,
                    "data_local_numpy_sum",
                    "pysolate.data-local-numpy-sum-pass.v2",
                )
                patch = json.loads(emit_source_pass_patch_request_json(raw_request))
                self.assertEqual("not_applicable", patch["status"])
                self.assertEqual("", patch["derived_source"])

    def test_scalar_passes_reject_programs_that_can_observe_compiled_code(self):
        source = (
            "left = 1 + 2\nright = 1 + 2\n"
            "try:\n    1 / 0\nexcept Exception as error:\n"
            "    result = error.__traceback__.tb_frame.f_code.co_code.hex()\n"
        )
        passes = [
            ("pure_scalar_cse", "pysolate.pure-scalar-cse-pass.v1"),
            ("pure_scalar_fold", "pysolate.pure-scalar-fold-pass.v1"),
        ]
        for pass_name, pass_version in passes:
            with self.subTest(pass_name=pass_name):
                raw = emit_source_pass_patch_request_json(request(source, pass_name, pass_version))
                self.assertEqual("not_applicable", json.loads(raw)["status"])

    def test_scalar_passes_reject_integer_identity_observation(self):
        cases = [
            (
                "pure_scalar_cse",
                "pysolate.pure-scalar-cse-pass.v1",
                "a = 1000\nleft = a * a + 0\nright = a * a + 0\nresult = left is right\n",
            ),
            (
                "pure_scalar_fold",
                "pysolate.pure-scalar-fold-pass.v1",
                "a = 1000\nliteral = 1000000\nfolded = a * a + 0\nresult = folded is literal\n",
            ),
        ]
        for pass_name, pass_version, source in cases:
            with self.subTest(pass_name=pass_name):
                raw = emit_source_pass_patch_request_json(request(source, pass_name, pass_version))
                self.assertEqual("not_applicable", json.loads(raw)["status"])

    def test_pure_scalar_fold_replaces_known_total_expression(self):
        source = "seed = 7\nfolded = seed * seed + 3\nresult = folded\n"
        raw = emit_source_pass_patch_request_json(request(
            source,
            "pure_scalar_fold",
            "pysolate.pure-scalar-fold-pass.v1",
        ))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        self.assertEqual(1, patch["replacement_count"])
        self.assertEqual(len(source.encode()), len(patch["derived_source"].encode()))
        self.assertIn("folded = 52", patch["derived_source"])
        tree = validate_source_pass_execution_request(source, raw)
        namespace = {}
        exec(compile(tree, "<agent-run>", "exec"), namespace, namespace)
        self.assertEqual(52, namespace["result"])

    def test_pure_scalar_fold_preserves_self_reassignment_values(self):
        source = "a = 1\na = a + 1\nb = a + 1\nresult = [a, b]\n"
        raw = emit_source_pass_patch_request_json(request(
            source,
            "pure_scalar_fold",
            "pysolate.pure-scalar-fold-pass.v1",
        ))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        self.assertEqual(2, patch["replacement_count"])
        tree = validate_source_pass_execution_request(source, raw)
        original_namespace = {}
        derived_namespace = {}
        exec(source, original_namespace, original_namespace)
        exec(compile(tree, "<agent-run>", "exec"), derived_namespace, derived_namespace)
        self.assertEqual([2, 3], original_namespace["result"])
        self.assertEqual(original_namespace["result"], derived_namespace["result"])

    def test_pure_scalar_fold_rejects_partial_or_effectful_expression(self):
        cases = [
            "value = 1 // 0\nresult = value\n",
            "value = work()\nresult = value\n",
            "seed = []\nvalue = seed + seed\nresult = value\n",
            "seed = 1\nmutate()\nvalue = seed + 1\nresult = value\n",
            "value = 9223372036854775807 + 1\nresult = value\n",
        ]
        for source in cases:
            with self.subTest(source=source):
                patch = json.loads(emit_source_pass_patch_request_json(request(
                    source,
                    "pure_scalar_fold",
                    "pysolate.pure-scalar-fold-pass.v1",
                )))
                self.assertEqual("not_applicable", patch["status"])

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
            "original_source_sha256", "derived_source", "derived_source_sha256", "replacement_count",
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
