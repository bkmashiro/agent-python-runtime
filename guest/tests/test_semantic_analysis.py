import copy
import json
import unittest

from agent_runtime.semantic import (
    ANALYSIS_SCHEMA_VERSION,
    analyze_request_json,
    analyze_source,
    canonical_analysis_json,
)


def digest(char):
    return "sha256:" + char * 64


BINDINGS = {
    "artifact_sha256": digest("a"),
    "execution_profile_sha256": digest("b"),
    "import_closure_sha256": digest("c"),
    "capability_plan_sha256": digest("d"),
}

CAPABILITIES = [
    {
        "name": "sources.read",
        "effect_class": "external_read",
        "playback": "live_only",
        "module": "sources",
        "method": "read",
        "global_alias": "",
        "arguments": [],
    },
    {
        "name": "mail.send",
        "effect_class": "external_write",
        "playback": "live_only",
        "module": "mail",
        "method": "send",
        "global_alias": "send_mail",
        "arguments": [],
    },
]


class SemanticAnalysisTests(unittest.TestCase):
    def test_over_bounded_deep_source_fails_closed_without_recursion_escape(self):
        source = "seed = 1\nvalue = seed" + " + 1" * 4096 + "\nresult = value\n"
        with self.assertRaises(ValueError):
            analyze_source(source, BINDINGS, [])

    def analyze(self, source, bindings=None, capabilities=None):
        return analyze_source(
            source,
            dict(BINDINGS if bindings is None else bindings),
            list(CAPABILITIES if capabilities is None else capabilities),
        )

    def test_local_calls_mutation_and_recursive_scc_remain_effect_free(self):
        report = self.analyze(
            "def leaf(value):\n"
            "    items = []\n"
            "    items.append(value)\n"
            "    return len(items)\n"
            "def left(n):\n"
            "    return 0 if n == 0 else right(n - 1)\n"
            "def right(n):\n"
            "    return left(n - 1)\n"
            "result = leaf(inputs['value']) + left(2)\n"
        )
        self.assertEqual(ANALYSIS_SCHEMA_VERSION, report["schema_version"])
        by_name = {row["name"]: row for row in report["functions"]}
        self.assertEqual({by_name["left"]["scc_id"], by_name["right"]["scc_id"]}, {by_name["left"]["scc_id"]})
        self.assertNotEqual(by_name["leaf"]["scc_id"], by_name["left"]["scc_id"])
        self.assertEqual([], report["barriers"])
        self.assertFalse(any(report["module_effects"].values()))

    def test_typed_read_and_write_color_callers_transitively(self):
        report = self.analyze(
            "def load():\n"
            "    return sources.read()\n"
            "def publish(value):\n"
            "    send_mail(value)\n"
            "data = load()\n"
            "publish(data)\n"
            "result = data\n"
        )
        by_name = {row["name"]: row for row in report["functions"]}
        self.assertTrue(by_name["load"]["effects"]["may_observe_live"])
        self.assertTrue(by_name["load"]["effects"]["may_suspend"])
        self.assertTrue(by_name["publish"]["effects"]["may_publish"])
        self.assertTrue(report["module_effects"]["may_publish"])
        self.assertTrue(report["module_effects"]["may_observe_live"])
        self.assertEqual(["sources.read"], by_name["load"]["direct_capabilities"])
        self.assertEqual(["mail.send"], by_name["publish"]["direct_capabilities"])

    def test_candidate_regions_expose_source_liveness_effects_and_barriers(self):
        capabilities = [{**copy.deepcopy(CAPABILITIES[0]), "arguments": ["key"]}, copy.deepcopy(CAPABILITIES[1])]
        report = self.analyze(
            "base = inputs['value']\n"
            "value = base + 1\n"
            "remote = sources.read('profile')\n"
            "if inputs['flag']:\n"
            "    value = value + 1\n"
            "result = value\n",
            capabilities=capabilities,
        )
        self.assertEqual("pysolate.semantic-analysis.v3", report["schema_version"])
        regions = sorted(report["candidate_regions"], key=lambda row: row["span"]["start_line"])
        self.assertEqual(5, len(regions))
        self.assertEqual(["inputs"], regions[0]["live_ins"])
        self.assertEqual(["base"], regions[0]["live_outs"])
        self.assertTrue(regions[0]["live_ins_canonical"])
        self.assertTrue(regions[0]["live_outs_canonical"])
        self.assertEqual(["base"], regions[1]["live_ins"])
        self.assertEqual(["value"], regions[1]["live_outs"])
        self.assertEqual([regions[0]["id"]], regions[1]["control_predecessors"])
        self.assertEqual(
            [{"name": "base", "producer_region_id": regions[0]["id"]}],
            regions[1]["data_dependencies"],
        )
        self.assertEqual([], regions[2]["live_ins"])
        self.assertEqual([report["call_sites"][0]["id"]], regions[2]["capability_occurrences"])
        self.assertTrue(regions[2]["effects"]["may_observe_live"])
        self.assertEqual("opaque_control", regions[3]["kind"])
        self.assertIn("opaque_control", regions[3]["rejection_reasons"])
        self.assertEqual(["value"], regions[4]["live_ins"])
        self.assertEqual(["result"], regions[4]["live_outs"])
        self.assertEqual(
            canonical_analysis_json(report),
            canonical_analysis_json(self.analyze(
                "base = inputs['value']\nvalue = base + 1\nremote = sources.read('profile')\nif inputs['flag']:\n    value = value + 1\nresult = value\n",
                capabilities=capabilities,
            )),
        )

    def test_candidate_graph_does_not_treat_declaration_bodies_as_live_state(self):
        report = self.analyze(
            "def double(value):\n"
            "    hidden = value + 1\n"
            "    return hidden * 2\n"
            "result = double(inputs['value'])\n"
        )
        declaration, call = report["candidate_regions"]
        self.assertEqual([], declaration["live_outs"])
        self.assertNotIn("hidden", declaration["live_ins"])
        self.assertNotIn("hidden", declaration["live_outs"])
        self.assertEqual(["inputs"], call["live_ins"])
        self.assertEqual([], call["data_dependencies"])
        self.assertIn("unknown_effect", call["rejection_reasons"])

    def test_candidate_regions_accept_proven_scalar_arithmetic_before_unrelated_effect(self):
        capabilities = [{**copy.deepcopy(CAPABILITIES[0]), "arguments": ["key"]}, copy.deepcopy(CAPABILITIES[1])]
        report = self.analyze(
            "seed = 40\n"
            "value = seed * 2 + 2\n"
            "remote = sources.read('profile')\n"
            "result = value\n",
            capabilities=capabilities,
        )
        seed, value, effect, result = report["candidate_regions"]
        self.assertEqual([], seed["rejection_reasons"])
        self.assertEqual([], value["rejection_reasons"])
        self.assertEqual({"start_line": 2, "start_column": 0, "end_line": 2, "end_column": 20}, value["span"])
        self.assertEqual(["seed"], value["live_ins"])
        self.assertEqual(["value"], value["live_outs"])
        self.assertEqual([], value["barriers"])
        self.assertEqual(
            [{"name": "seed", "producer_region_id": seed["id"]}],
            value["data_dependencies"],
        )
        self.assertEqual({"may_publish": False, "may_observe_live": False, "may_suspend": False, "may_be_unknown": False}, value["effects"])
        self.assertTrue(effect["effects"]["may_observe_live"])
        self.assertIn("may_raise", effect["rejection_reasons"])
        self.assertEqual([], result["rejection_reasons"])
        repeated = self.analyze(
            "seed = 40\nvalue = seed * 2 + 2\nremote = sources.read('profile')\nresult = value\n",
            capabilities=capabilities,
        )
        self.assertEqual(report["candidate_regions"], repeated["candidate_regions"])

    def test_candidate_regions_reject_non_scalar_name_alias_identity(self):
        report = self.analyze("items = [1]\nalias = items\nalias.append(2)\nresult = alias\n")
        alias = report["candidate_regions"][1]
        self.assertEqual(["identity_alias"], alias["rejection_reasons"])
        self.assertEqual(["items"], alias["live_ins"])
        self.assertEqual(["alias"], alias["live_outs"])
        self.assertEqual("items", alias["data_dependencies"][0]["name"])

    def test_prepared_region_helper_binding_is_reserved_for_the_whole_source(self):
        helper = "__pysolate_materialize_value__"
        unsafe_sources = (
            f"{helper} = lambda value: value\nresult = 1\n",
            f"def {helper}(value):\n    return value\nresult = 1\n",
            f"def f({helper}):\n    return 1\nresult = f(None)\n",
            f"del {helper}\nresult = 1\n",
            f"import json as {helper}\nresult = 1\n",
            f"result = {helper}('sha256:' + 'a' * 64)\n",
            "globals()['__pysolate_materialize_value__'] = lambda value: value\nresult = 1\n",
            "exec('__pysolate_materialize_value__ = None')\nresult = 1\n",
        )
        for source in unsafe_sources:
            with self.subTest(source=source):
                report = self.analyze(source)
                self.assertTrue(report["candidate_regions"])
                for region in report["candidate_regions"]:
                    self.assertIn("reserved_helper_binding", region["rejection_reasons"])

    def test_prepared_region_helper_name_inside_literal_does_not_reserve_binding(self):
        report = self.analyze("note = '__pysolate_materialize_value__'\nresult = 1\n")
        self.assertNotIn("reserved_helper_binding", report["candidate_regions"][1]["rejection_reasons"])

    def test_candidate_regions_reject_explicit_and_expression_exception_paths(self):
        for source in (
            "assert inputs['ok']\n",
            "raise RuntimeError('stop')\n",
            "result = 1 / 0\n",
        ):
            with self.subTest(source=source):
                region = self.analyze(source)["candidate_regions"][0]
                self.assertIn("may_raise", region["rejection_reasons"])

    def test_augassign_and_delete_preserve_liveness_without_forging_deleted_producers(self):
        report = self.analyze("x = 1\nx += 1\ndel x\nresult = 2\n")
        regions = report["candidate_regions"]
        self.assertEqual(["x"], regions[1]["live_ins"])
        self.assertEqual(["x"], regions[1]["live_outs"])
        self.assertEqual([{"name": "x", "producer_region_id": regions[0]["id"]}], regions[1]["data_dependencies"])
        self.assertIn("may_raise", regions[1]["rejection_reasons"])
        self.assertEqual(["x"], regions[2]["live_ins"])
        self.assertEqual([], regions[2]["live_outs"])
        self.assertEqual([{"name": "x", "producer_region_id": regions[1]["id"]}], regions[2]["data_dependencies"])
        self.assertIn("may_raise", regions[2]["rejection_reasons"])
        self.assertEqual([], regions[3]["data_dependencies"])

    def test_destructuring_assignment_is_rejected_as_may_raise(self):
        report = self.analyze("left, right = inputs['pair']\nresult = left\n")
        self.assertIn("may_raise", report["candidate_regions"][0]["rejection_reasons"])

    def test_overlay_emits_only_exact_module_entry_call_facts(self):
        capabilities = [
            {**copy.deepcopy(CAPABILITIES[0]), "arguments": ["key"]},
            {**copy.deepcopy(CAPABILITIES[1]), "arguments": ["value"]},
        ]
        report = self.analyze(
            "first = sources.read('alpha')\n"
            "second = sources.read('beta')\n"
            "if inputs['flag']:\n"
            "    sources.read('conditional')\n"
            "result = first\n",
            capabilities=capabilities,
        )
        self.assertEqual("pysolate.semantic-analysis.v3", report["schema_version"])
        self.assertEqual("positive_only", report["call_site_coverage"])
        self.assertEqual(2, len(report["call_sites"]))
        first, second = sorted(report["call_sites"], key=lambda row: row["span"]["start_line"])
        self.assertTrue(first["necessarily_reached"])
        self.assertEqual(1, first["dynamic_occurrence"])
        self.assertEqual({"key": "alpha"}, first["canonical_arguments"])
        self.assertFalse(second["necessarily_reached"])
        self.assertEqual({"key": "beta"}, second["canonical_arguments"])
        self.assertNotEqual(first["id"], second["id"])

    def test_overlay_rejects_dynamic_arguments_aliases_and_control_flow(self):
        capabilities = [{**copy.deepcopy(CAPABILITIES[0]), "arguments": ["key"]}, copy.deepcopy(CAPABILITIES[1])]
        for source in (
            "key = inputs['key']\nresult = sources.read(key)\n",
            "alias = sources.read\nresult = alias('x')\n",
            "if True:\n    result = sources.read('x')\n",
            "result = sources.read(key='x', extra='y')\n",
            "sources = object()\nresult = sources.read('x')\n",
            "import sources\nresult = sources.read('x')\n",
            "del sources\nresult = sources.read('x')\n",
            "def sources():\n    return None\nresult = sources.read('x')\n",
            "class sources:\n    pass\nresult = sources.read('x')\n",
        ):
            with self.subTest(source=source):
                report = self.analyze(source, capabilities=capabilities)
                self.assertEqual([], report["call_sites"])

    def test_overlay_does_not_skip_non_docstring_string_statement(self):
        capabilities = [{**copy.deepcopy(CAPABILITIES[0]), "arguments": ["key"]}, copy.deepcopy(CAPABILITIES[1])]
        report = self.analyze(
            '"doc"\n"second"\nresult = sources.read("<\u2028>")\n', capabilities=capabilities
        )
        self.assertEqual(1, len(report["call_sites"]))
        self.assertFalse(report["call_sites"][0]["necessarily_reached"])
        encoded = canonical_analysis_json(report)
        self.assertIn('"<\\u2028>"', encoded)

    def test_dynamic_calls_eval_import_and_tool_rebinding_fail_closed(self):
        report = self.analyze(
            "sources = object()\n"
            "len = object()\n"
            "len()\n"
            "def helper():\n"
            "    return 1\n"
            "helper = object()\n"
            "helper()\n"
            "def dynamic(fn):\n"
            "    return fn()\n"
            "value = eval('1 + 1')\n"
            "module = __import__('json')\n"
            "result = dynamic(lambda: value)\n"
        )
        codes = {row["code"] for row in report["barriers"]}
        self.assertTrue({"tool_rebinding", "dynamic_call", "eval_exec", "dynamic_import"}.issubset(codes))
        self.assertTrue(report["module_effects"]["may_be_unknown"])
        dynamic = next(row for row in report["functions"] if row["name"] == "dynamic")
        self.assertTrue(dynamic["effects"]["may_be_unknown"])
        for barrier in report["barriers"]:
            self.assertGreaterEqual(barrier["span"]["start_line"], 1)
            self.assertGreaterEqual(barrier["span"]["end_line"], barrier["span"]["start_line"])

    def test_higher_order_builtins_cannot_hide_tool_calls(self):
        for source in (
            "result=list(map(sources.read,[1]))\n",
            "result=list(filter(sources.read,[1]))\n",
            "result=sorted([1],key=sources.read)\n",
        ):
            with self.subTest(source=source):
                report = self.analyze(source)
                self.assertTrue(report["module_effects"]["may_be_unknown"])
                self.assertIn("dynamic_call", {row["code"] for row in report["barriers"]})

    def test_v0_complex_control_and_object_boundaries_are_opaque(self):
        cases = (
            "for value in [1]:\n    result=value\n",
            "try:\n    result=1\nexcept Exception:\n    result=2\n",
            "class Value:\n    pass\nresult=1\n",
            "items=[1]\nitems[0]=2\nresult=items\n",
            "def values():\n    yield 1\nresult=list(values())\n",
            "result=[value for value in [1]]\n",
        )
        for source in cases:
            with self.subTest(source=source):
                report = self.analyze(source)
                self.assertTrue(report["module_effects"]["may_be_unknown"])
                self.assertIn("unsupported_control_flow", {row["code"] for row in report["barriers"]})

    def test_live_wasi_attributes_are_observations_without_calls(self):
        report = self.analyze("import os, sys\nresult=(os.environ['HOME'], sys.argv[0])\n")
        self.assertTrue(report["module_effects"]["may_observe_live"])

    def test_definition_time_effects_and_forbidden_imports_are_not_missed(self):
        report = self.analyze(
            "import builtins\n"
            "def f(value=sources.read()):\n"
            "    return value\n"
            "result=f()\n"
        )
        self.assertTrue(report["module_effects"]["may_observe_live"])
        self.assertTrue(report["module_effects"]["may_suspend"])
        self.assertTrue(report["module_effects"]["may_be_unknown"])
        self.assertIn("dynamic_import", {row["code"] for row in report["barriers"]})

    def test_multiple_forbidden_imports_at_one_statement_are_deduplicated(self):
        report = self.analyze("import csv,json\nresult=1\n")
        keys = [
            (row["function_id"], row["code"], json.dumps(row["span"], separators=(",", ":")))
            for row in report["barriers"]
        ]
        self.assertEqual(len(keys), len(set(keys)))
        self.assertEqual(["dynamic_import"], [row["code"] for row in report["barriers"]])

    def test_clock_random_wasi_and_compound_flow_are_conservative(self):
        report = self.analyze(
            "import random, time\n"
            "def choose(flag):\n"
            "    if flag:\n"
            "        return sources.read()\n"
            "    return time.time() + random.random()\n"
            "with open('value.txt') as handle:\n"
            "    value = handle.read()\n"
            "result = choose(bool(value))\n"
        )
        choose = next(row for row in report["functions"] if row["name"] == "choose")
        self.assertTrue(choose["effects"]["may_observe_live"])
        self.assertTrue(choose["effects"]["may_suspend"])
        self.assertTrue(report["module_effects"]["may_observe_live"])
        self.assertTrue(report["module_effects"]["may_suspend"])

    def test_decorator_and_nested_function_are_opaque_barriers(self):
        report = self.analyze(
            "@decorator\n"
            "def outer():\n"
            "    def nested():\n"
            "        return 1\n"
            "    return nested()\n"
            "result = outer()\n"
        )
        codes = [row["code"] for row in report["barriers"]]
        self.assertIn("unsupported_decorator", codes)
        self.assertIn("unsupported_control_flow", codes)
        outer = next(row for row in report["functions"] if row["name"] == "outer")
        self.assertTrue(outer["effects"]["may_be_unknown"])
        self.assertTrue(report["module_effects"]["may_be_unknown"])

    def test_bounded_json_entrypoint_is_strict(self):
        request = {"source": "result = 1\n", "bindings": BINDINGS, "capabilities": CAPABILITIES}
        report = json.loads(analyze_request_json(json.dumps(request)))
        self.assertEqual(report["schema_version"], ANALYSIS_SCHEMA_VERSION)
        invalid = copy.deepcopy(request)
        invalid["extra"] = True
        with self.assertRaises(ValueError):
            analyze_request_json(json.dumps(invalid))
        with self.assertRaises(ValueError):
            analyze_source("x" * ((1 << 20) + 1), BINDINGS, CAPABILITIES)
        with self.assertRaises(ValueError):
            analyze_source("result = 1", BINDINGS, CAPABILITIES * 65)

    def test_report_is_deterministic_and_identity_bound(self):
        first = self.analyze("result = inputs['value'] + 1\n")
        second = self.analyze("result = inputs['value'] + 1\n")
        self.assertEqual(canonical_analysis_json(first), canonical_analysis_json(second))
        self.assertEqual(json.loads(canonical_analysis_json(first)), first)
        changed_source = self.analyze("result = inputs['value'] + 2\n")
        self.assertNotEqual(first["source_sha256"], changed_source["source_sha256"])
        self.assertNotEqual(first["ast_sha256"], changed_source["ast_sha256"])
        bindings = copy.deepcopy(BINDINGS)
        bindings["capability_plan_sha256"] = digest("e")
        changed_plan = self.analyze("result = inputs['value'] + 1\n", bindings=bindings)
        self.assertNotEqual(canonical_analysis_json(first), canonical_analysis_json(changed_plan))


if __name__ == "__main__":
    unittest.main()
