import importlib.util
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-paired-canary.py"
SPEC = importlib.util.spec_from_file_location("tau2_paired_canary", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
paired = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(paired)


class Tau2PairedCanaryTests(unittest.TestCase):
    def test_treatment_uses_provider_json_mode(self):
        source = MODULE_PATH.read_text()
        self.assertIn('response_format={"type": "json_object"}', source)
        self.assertIn('AssistantMessage(role="assistant", content=raw)', source)
        self.assertNotIn('AssistantMessage(role="assistant", content=None)', source)

    def test_accepts_only_exact_single_tool_programs(self):
        action = paired.parse_program_action(
            '{"kind":"program","source":"result = tools.get_reservation_details(\\"JMO1MG\\")"}'
        )
        self.assertEqual(action["capability"], "tau2.airline.get_reservation_details")
        self.assertEqual(action["arguments"], {"reservation_id": "JMO1MG"})
        for source in [
            "x = tools.get_reservation_details('JMO1MG')",
            "result = tools.get_reservation_details(user_input)",
            "result = tools.get_reservation_details('other')",
            "result = tools.get_reservation_details('JMO1MG')\nresult = tools.get_user_details('anya_garcia_5901')",
        ]:
            with self.subTest(source=source), self.assertRaises(ValueError):
                paired.inspect_single_tool_program(source)

    def test_accepts_nonempty_answer_and_rejects_extra_fields(self):
        self.assertEqual(
            paired.parse_program_action('{"kind":"answer","content":"There are 4 bags."}')["content"],
            "There are 4 bags.",
        )
        with self.assertRaises(ValueError):
            paired.parse_program_action('{"kind":"answer","content":"4","source":"x"}')


if __name__ == "__main__":
    unittest.main()
