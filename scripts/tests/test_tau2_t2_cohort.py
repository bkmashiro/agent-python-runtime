import importlib.util
import json
import pathlib
import tempfile
import unittest
import argparse

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-t2-cohort.py"
SPEC = importlib.util.spec_from_file_location("tau2_t2_cohort", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
cohort = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(cohort)


class Tau2T2CohortTests(unittest.TestCase):
    def test_path_repair_preserves_venv_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            target = root / "system-python"
            target.write_text("")
            link = root / "venv-python"
            link.symlink_to(target)
            args = argparse.Namespace(tau2_python=str(link), source_root=directory, repo_root=directory, artifact=directory, private_manifest=directory, evidence_root=directory)
            resolved = cohort.resolve_paths(args)
            self.assertEqual(resolved.tau2_python, str(link.absolute()))
            self.assertEqual(pathlib.Path(resolved.source_root), root.resolve())

    def actions(self):
        return [
            {"name": "get_user_details", "arguments": {"user_id": "u1"}},
            {"name": "search_direct_flight", "arguments": {"origin": "A", "destination": "B", "date": "2024-01-01"}},
        ]

    def test_parses_positional_and_keyword_scalar_reads(self):
        action = cohort.parse_action('{"kind":"program","source":"result = tools.get_user_details(\\"u1\\")"}', self.actions())
        self.assertEqual(action["arguments"], {"user_id": "u1"})
        action = cohort.parse_action(
            '{"kind":"program","source":"result = tools.search_direct_flight(origin=\\"A\\", destination=\\"B\\", date=\\"2024-01-01\\")"}',
            self.actions(),
        )
        self.assertEqual(action["argument_names"], ["origin", "destination", "date"])

    def test_rejects_dynamic_mixed_and_out_of_scope_calls(self):
        invalid = [
            'result = tools.get_user_details(inputs["id"])',
            'result = tools.search_direct_flight("A", destination="B", date="2024-01-01")',
            'result = tools.get_user_details("other")',
            'result = tools.cancel_reservation("x")',
            'x = tools.get_user_details("u1")',
        ]
        for source in invalid:
            with self.subTest(source=source), self.assertRaises((SyntaxError, ValueError)):
                cohort.inspect_program(source, self.actions())

    def test_provider_budget_fails_before_extra_call(self):
        budget = cohort.ProviderBudget(2)
        observed = []
        function = lambda value: observed.append(value) or value
        self.assertEqual(budget.call(function, 1), 1)
        self.assertEqual(budget.call(function, 2), 2)
        with self.assertRaises(cohort.ProviderBudgetExceeded):
            budget.call(function, 3)
        self.assertEqual(observed, [1, 2])
        self.assertEqual(budget.calls, 2)

    def test_answer_contract_is_strict(self):
        self.assertEqual(cohort.parse_action('{"kind":"answer","content":"hello"}', self.actions())["content"], "hello")
        for value in ('{"kind":"answer","content":""}', '{"kind":"answer","content":"x","extra":1}', 'not json'):
            with self.assertRaises((ValueError, json.JSONDecodeError)):
                cohort.parse_action(value, self.actions())


if __name__ == "__main__":
    unittest.main()
