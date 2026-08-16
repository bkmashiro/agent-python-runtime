import importlib.util
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-paired-success-report.py"
SPEC = importlib.util.spec_from_file_location("tau2_paired_success_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


def row(lane):
    messages = []
    if lane == "direct":
        messages = [
            {"role": "assistant", "content": None, "tool_calls": [{"name": "get_reservation_details"}]},
            {"role": "assistant", "content": None, "tool_calls": [{"name": "get_user_details"}]},
            {"role": "assistant", "content": None, "tool_calls": [{"name": "transfer_to_human_agents"}]},
        ]
    else:
        messages = [{"role": "assistant", "content": "done", "tool_calls": None}]
    return {
        "schema_version": "pysolate.tau2-paired-private.v1", "source_revision": reporter.REVISION,
        "lane": lane, "model": reporter.MODEL, "seed": 42, "temperature": 0.0, "status": "completed",
        "simulation": {"termination_reason": "user_stop", "messages": messages, "reward_info": {"reward": 1.0, "reward_breakdown": {"COMMUNICATE": 1.0, "DB": 1.0}}},
        "pysolate_events": None,
    }


def treatment_row():
    value = row("treatment")
    events: list[dict] = [{"kind": "answer"}]
    for index, (capability, method) in enumerate((("tau2.airline.get_reservation_details", "reservation"), ("tau2.airline.get_user_details", "user"))):
        content = method
        events.append({"kind": "program", "capability": capability, "turn": {"content": content, "receipt": {"outcome": "ok", "response_sha256": reporter.response_digest(content), "source": {"claim_level": "source_bound", "occurrence_id": "sha256:" + str(index + 1) * 64}}}})
    events.extend([{"kind": "answer"}, {"kind": "answer"}, {"kind": "invalid_model_action"}])
    value["pysolate_events"] = events
    return value


class Tau2PairedSuccessReportTests(unittest.TestCase):
    def test_accepts_complete_paired_join(self):
        report = reporter.build_report(row("direct"), treatment_row())
        self.assertEqual(report["conclusion"], "PAIRED_CANARY_SUPPORTED")
        self.assertEqual(report["treatment"]["pysolate_physical_calls"], 2)
        self.assertFalse(report["interpretation"]["tool_surface_matched"])
        self.assertFalse(report["interpretation"]["performance_comparison_supported"])

    def test_rejects_missing_source_or_response_join(self):
        for mutation in ("source", "response"):
            treatment = treatment_row()
            turn = treatment["pysolate_events"][1]["turn"]
            if mutation == "source":
                turn["receipt"]["source"] = None
            else:
                turn["receipt"]["response_sha256"] = "0" * 64
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                reporter.build_report(row("direct"), treatment)


if __name__ == "__main__":
    unittest.main()
