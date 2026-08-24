import hashlib
import json
import unittest

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


class SourcePassTests(unittest.TestCase):
    def test_data_local_numpy_sum_emits_one_value_slot_materialization(self):
        source = (
            "import io\n"
            "import numpy as np\n"
            "dataset = np.load(io.BytesIO(open('/workspace/input.npy', 'rb').read()), allow_pickle=False)\n"
            "result = int(dataset.sum())\n"
        )
        raw_request = request(
            source,
            "data_local_numpy_sum",
            "pysolate.data-local-numpy-sum-pass.v2",
        )
        patch = json.loads(emit_source_pass_patch_request_json(raw_request))
        self.assertEqual("applied", patch["status"])
        self.assertEqual(1, patch["replacement_count"])
        self.assertEqual(
            "pass\npass\npass\nresult = _pysolate_materialize_slot('slot-numpy-sum-v1')\n",
            patch["derived_source"],
        )
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

    def test_split_phase_sources_read_keeps_submit_and_materialize_adjacent_for_one_call(self):
        source = 'first = sources.read("alpha")\nresult = first\n'
        raw = emit_source_pass_patch_request_json(request(
            source,
            "split_phase_sources_read",
            "pysolate.split-phase-sources-read-pass.v2",
        ))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        self.assertEqual(1, patch["replacement_count"])
        self.assertEqual(source.count("\n"), patch["derived_source"].count("\n"))
        first_line = patch["derived_source"].splitlines()[0]
        self.assertLess(first_line.index("_pysolate_call_submit"), first_line.index("_pysolate_call_materialize"))
        self.assertIn('["body"]', first_line)
        validate_source_pass_execution_request(source, raw)

    def test_split_phase_sources_read_stages_two_calls_before_source_order_materialization(self):
        source = (
            'first = sources.read("alpha")\n'
            'second = sources.read("beta")\n'
            'result = [first, second]\n'
        )
        raw = emit_source_pass_patch_request_json(request(
            source,
            "split_phase_sources_read",
            "pysolate.split-phase-sources-read-pass.v2",
        ))
        patch = json.loads(raw)
        self.assertEqual("applied", patch["status"])
        self.assertEqual(2, patch["replacement_count"])
        lines = patch["derived_source"].splitlines()
        self.assertEqual(2, lines[0].count("_pysolate_call_submit"))
        self.assertEqual(1, lines[0].count("_pysolate_call_materialize"))
        self.assertEqual(1, lines[1].count("_pysolate_call_materialize"))
        self.assertNotIn("_pysolate_call_submit", lines[1])
        self.assertEqual(source.count("\n"), patch["derived_source"].count("\n"))

    def test_split_phase_sources_read_preserves_dynamic_branch_and_loop_activation(self):
        branch = (
            'if inputs["take"]:\n'
            '    first = sources.read("alpha")\n'
            '    second = sources.read("beta")\n'
            '    result = [first, second]\n'
            'else:\n'
            '    result = []\n'
        )
        loop = (
            'result = []\n'
            'for item in inputs["items"]:\n'
            '    first = sources.read("alpha")\n'
            '    result.append(first)\n'
        )
        for source, count in ((branch, 2), (loop, 1)):
            with self.subTest(source=source):
                patch = json.loads(emit_source_pass_patch_request_json(request(
                    source,
                    "split_phase_sources_read",
                    "pysolate.split-phase-sources-read-pass.v2",
                )))
                self.assertEqual("applied", patch["status"])
                self.assertEqual(count, patch["replacement_count"])
                self.assertEqual(source.count("\n"), patch["derived_source"].count("\n"))
                self.assertIn("_pysolate_call_submit", patch["derived_source"])
                validate_source_pass_execution_request(source, json.dumps(patch, separators=(",", ":")))

    def test_split_phase_structured_lowering_submits_only_on_active_dynamic_paths(self):
        branch = (
            'if inputs["take"]:\n'
            '    first = sources.read("alpha")\n'
            '    second = sources.read("beta")\n'
            '    result = [first, second]\n'
            'else:\n'
            '    result = []\n'
        )
        loop = (
            'result = []\n'
            'for item in inputs["items"]:\n'
            '    first = sources.read("alpha")\n'
            '    result.append(first)\n'
        )
        def execute(source, inputs):
            patch = json.loads(emit_source_pass_patch_request_json(request(
                source, "split_phase_sources_read", "pysolate.split-phase-sources-read-pass.v2",
            )))
            submitted = []
            materialized = []
            namespace = {
                "inputs": inputs,
                "_pysolate_call_submit": lambda slot, raw: submitted.append(slot),
                "_pysolate_call_materialize": lambda slot: materialized.append(slot) or {"body": slot},
            }
            exec(compile(patch["derived_source"], "<derived>", "exec"), namespace, namespace)
            return namespace["result"], submitted, materialized
        result, submitted, materialized = execute(branch, {"take": False})
        self.assertEqual(([], [], []), (result, submitted, materialized))
        result, submitted, materialized = execute(branch, {"take": True})
        self.assertEqual(2, len(result))
        self.assertEqual(2, len(submitted))
        self.assertEqual(submitted, materialized)
        result, submitted, materialized = execute(loop, {"items": []})
        self.assertEqual(([], [], []), (result, submitted, materialized))
        result, submitted, materialized = execute(loop, {"items": [1, 2]})
        self.assertEqual(2, len(result))
        self.assertEqual(2, len(submitted))
        self.assertEqual(submitted, materialized)

        patch = json.loads(emit_source_pass_patch_request_json(request(
            loop, "split_phase_sources_read", "pysolate.split-phase-sources-read-pass.v2",
        )))
        submitted = []
        namespace = {
            "inputs": {"items": [1, 2]},
            "_pysolate_call_submit": lambda slot, raw: submitted.append(slot),
            "_pysolate_call_materialize": lambda slot: (_ for _ in ()).throw(RuntimeError("first failure")),
        }
        with self.assertRaisesRegex(RuntimeError, "first failure"):
            exec(compile(patch["derived_source"], "<derived>", "exec"), namespace, namespace)
        self.assertEqual(1, len(submitted))

    def test_split_phase_sources_read_rejects_unsafe_activation(self):
        source = (
            'if decide():\n'
            '    first = sources.read("alpha")\n'
            '    result = first\n'
            'else:\n'
            '    result = None\n'
        )
        patch = json.loads(emit_source_pass_patch_request_json(request(
            source,
            "split_phase_sources_read",
            "pysolate.split-phase-sources-read-pass.v2",
        )))
        self.assertEqual("not_applicable", patch["status"])

    def test_split_phase_sources_read_rejects_dynamic_or_observable_programs(self):
        cases = [
            'path = "alpha"\nfirst = sources.read(path)\nresult = first\n',
            'if True:\n    first = sources.read("alpha")\nresult = first\n',
            'first = sources.read("alpha")\nprint("visible")\nresult = first\n',
            'first = sources.read("alpha")\nresult = locals()\n',
            'first = sources.read(path="alpha")\nresult = first\n',
            'first = other.read("alpha")\nresult = first\n',
            'try:\n    first = sources.read("alpha")\n    result = first\nexcept Exception:\n    result = None\n',
            'while inputs["keep"]:\n    first = sources.read("alpha")\n    result = first\n',
            'with open("/workspace/input.npy", "rb") as handle:\n    first = sources.read("alpha")\n    result = first\n',
            'if inputs["take"]: first = sources.read("alpha"); result = first\n',
            'result = []\nfor item in inputs["items"]: first = sources.read("alpha"); result.append(first)\n',
            'result = []\nfor item in inputs["items"]:\n    first = sources.read(\n        "alpha"\n    )\n    result.append(first)\n',
            'if inputs["take"]:\n\tfirst = sources.read("alpha")\n\tsecond = sources.read("beta")\n\tresult = [first, second]\nelse:\n\tresult = []\n',
            'result = []\nfor item in inputs["items"]:\n\tfirst = sources.read("alpha")\n\tresult.append(first)\n',
        ]
        for source in cases:
            with self.subTest(source=source):
                patch = json.loads(emit_source_pass_patch_request_json(request(
                    source,
                    "split_phase_sources_read",
                    "pysolate.split-phase-sources-read-pass.v2",
                )))
                self.assertEqual("not_applicable", patch["status"])

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
