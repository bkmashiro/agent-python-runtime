import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("contention", Path(__file__).with_name("run-contention-study.py"))
assert spec is not None and spec.loader is not None
study = importlib.util.module_from_spec(spec)
spec.loader.exec_module(study)


class TraceAccountingTest(unittest.TestCase):
    def setUp(self):
        counts = dict(ok=1, timeout=0, rejected=0, cancelled=0, failed=0, incorrect=0)
        self.events = [
            {"kind":"header", "config":{"requests":1,"tool_capacity":1},
             "programs":[{"name":"short","expected_json":"42","expected_calls_on_success":1}]},
            {"kind":"tool_queued","request_id":0,"call_id":1,"args":{"value":42}},
            {"kind":"tool_started","request_id":0,"call_id":1},
            {"kind":"tool_end","request_id":0,"call_id":1,"args":{"value":42},"acquired":True},
            {"kind":"request_end","id":0,"work":"short","status":"ok","calls":1,"output":{"Value":42}},
            {"kind":"summary","complete":True,"scheduled":1,"counts":counts,"calls":1,
             "backend":{"attempts":1,"peak_inflight":1,"end_inflight":0}},
        ]

    def validate(self, events):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "trace.jsonl"
            path.write_text("".join(json.dumps(e)+"\n" for e in events))
            return study.validate_trace(path)

    def test_complete(self):
        self.assertEqual(self.validate(self.events)["counts"]["ok"], 1)

    def test_decision_and_deadline_validation(self):
        events = copy.deepcopy(self.events)
        events[0]["decision_events"] = True
        events[0]["config"].update(arm="spare-capacity", timeout_ns=10)
        events[4].update(admitted=True, latency_ns=5)
        decision = {"kind":"mode_decision", "request_id":0, "occupied_tool_slots":0, "early_reads":True}
        events.insert(1, decision)
        self.validate(events)
        decision["early_reads"] = False
        with self.assertRaises(ValueError):
            self.validate(events)
        decision["early_reads"] = True
        events[5]["latency_ns"] = 11
        with self.assertRaises(ValueError):
            self.validate(events)

    def test_rejects_partial_duplicate_and_unresolved(self):
        variants = [self.events[:-1], self.events[:3]+self.events[4:], self.events[:4]+[self.events[3]]+self.events[4:]]
        for events in variants:
            with self.subTest(events=events), self.assertRaises(ValueError):
                self.validate(events)

    def test_rejects_hidden_timeout_wrong_value_and_capacity(self):
        for field in ("status", "value", "capacity", "calls"):
            events = copy.deepcopy(self.events)
            if field == "status":
                events[4]["status"] = "timeout"
            elif field == "value":
                events[4]["output"]["Value"] = 41
            elif field == "capacity":
                events[-1]["backend"]["peak_inflight"] = 2
            else:
                events[4]["calls"] = 2
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.validate(events)


if __name__ == "__main__":
    unittest.main()
