import io
import json
import os
import tempfile
import unittest
from types import SimpleNamespace

import host


class FakeAPI:
    def __init__(self, result):
        self.result = result
        self.calls = []
        self.print_on_call = False

    def __call__(self, **kwargs):
        self.calls.append(kwargs)
        if self.print_on_call:
            print("not protocol")
        return self.result


class FakeWorld:
    def __init__(self):
        self.lookup = FakeAPI({"ok": True})
        self.hidden = FakeAPI("bad")
        self.apis: dict = {
            "mail": {"send": self.lookup, "_internal": self.hidden},
            "admin": {"delete_everything": self.hidden},
        }
        self.closed = False
        self.persisted = False
        self.output_db_home_path_on_disk = "/private/output/dbs"

    def _save_state(self, path):
        self.persisted = path

    def close(self):
        self.closed = True

    def save_logs(self):
        pass


class HostProtocolTests(unittest.TestCase):
    def make_bridge(self, world=None):
        return host.Bridge(world or FakeWorld(), "task", "experiment")

    def test_ready_lists_only_public_non_admin_apis(self):
        bridge = self.make_bridge()
        ready = bridge.ready_frame()
        self.assertEqual(ready["kind"], "ready")
        self.assertEqual(ready["tools"], [{"name": "mail.send", "python_path": "apis.mail.send"}])

    def test_call_dispatches_and_returns_value(self):
        world = FakeWorld()
        bridge = self.make_bridge(world)
        result = bridge.handle({"id": 7, "op": "call", "tool": "mail.send", "arguments": {"to": "a"}})
        self.assertEqual(result, {"id": 7, "value": {"ok": True}})
        self.assertEqual(world.lookup.calls, [{"to": "a"}])

    def test_rejects_injected_bridge_controls_without_dispatch(self):
        world = FakeWorld()
        bridge = self.make_bridge(world)
        for name in ("client", "track", "show", "_app_name", "_api_name", "_system_datetime", "raise_on_failure"):
            with self.subTest(name=name):
                result = bridge.handle({"id": "x", "op": "call", "tool": "mail.send", "arguments": {name: False}})
                self.assertIn("error", result)
        self.assertEqual(world.lookup.calls, [])

    def test_business_date_field_is_not_a_bridge_control(self):
        world = FakeWorld()
        result = self.make_bridge(world).handle({"id": 1, "op": "call", "tool": "mail.send",
                                                "arguments": {"date_and_time": "fixture-date"}})
        self.assertNotIn("error", result)
        self.assertEqual(world.lookup.calls, [{"date_and_time": "fixture-date"}])

    def test_rejects_unknown_and_admin_tools(self):
        bridge = self.make_bridge()
        for tool in ("mail._internal", "admin.delete_everything", "mail.missing"):
            with self.subTest(tool=tool):
                result = bridge.handle({"id": 1, "op": "call", "tool": tool, "arguments": {}})
                self.assertIn("error", result)

    def test_rejects_execute_source_and_malformed_messages(self):
        bridge = self.make_bridge()
        self.assertIn("error", bridge.handle({"id": 1, "op": "execute", "source": "x"}))
        for message in ({"op": "call"}, {"id": 1, "op": "call", "tool": "mail.send", "arguments": []}):
            self.assertIn("error", bridge.handle(message))

    def test_finish_persists_runs_original_evaluator_then_closes(self):
        world = FakeWorld()
        seen = []
        evaluator = lambda task, experiment: seen.append((task, experiment)) or {"success": True}
        bridge = self.make_bridge(world)
        result = bridge.handle({"id": 9, "op": "finish"}, evaluator=evaluator)
        self.assertEqual(result, {"id": 9, "value": {"success": True}})
        self.assertEqual(world.persisted, world.output_db_home_path_on_disk)
        self.assertEqual(seen, [("task", "experiment")])
        self.assertTrue(world.closed)

    def test_close_occurs_if_evaluator_fails(self):
        world = FakeWorld()
        bridge = self.make_bridge(world)
        def fail(task, experiment):
            raise RuntimeError("grading failed")
        result = bridge.handle({"id": 2, "op": "finish"}, evaluator=fail)
        self.assertIn("error", result)
        self.assertTrue(world.closed)

    def test_run_protocol_frames_trace_and_closes_on_eof(self):
        world = FakeWorld()
        world.lookup.print_on_call = True
        world.lookup.result = {"ok": True}
        stdout = io.StringIO()
        with tempfile.TemporaryDirectory() as directory:
            trace = host.PrivateTrace(os.path.join(directory, "trace.jsonl"))
            try:
                host.run_protocol(world, "task", "experiment", trace,
                                  io.StringIO('{"id":1,"op":"call","tool":"mail.send","arguments":{}}\n'), stdout)
            finally:
                trace.close()
            frames = [json.loads(line) for line in stdout.getvalue().splitlines()]
            self.assertEqual(frames[0]["tools"][0]["name"], "mail.send")
            self.assertEqual(frames[1], {"id": 1, "value": {"ok": True}})
            self.assertTrue(world.closed)
            with open(os.path.join(directory, "trace.jsonl"), encoding="utf-8") as stream:
                records = [json.loads(line) for line in stream]
                self.assertEqual([r["kind"] for r in records], ["request_start", "request_end"])

    def test_api_exception_can_be_caught_without_destroying_world(self):
        world = FakeWorld()
        def fail(**kwargs):
            raise RuntimeError("ordinary API failure")
        world.apis["mail"]["fail"] = fail
        source = "\n".join(json.dumps(r) for r in [
            {"id": 1, "op": "call", "tool": "mail.fail", "arguments": {}},
            {"id": 2, "op": "call", "tool": "mail.send", "arguments": {}},
        ]) + "\n"
        stdout = io.StringIO()
        with tempfile.TemporaryDirectory() as directory:
            trace = host.PrivateTrace(os.path.join(directory, "trace.jsonl"))
            try:
                host.run_protocol(world, "task", "experiment", trace, io.StringIO(source), stdout)
            finally:
                trace.close()
        frames = [json.loads(line) for line in stdout.getvalue().splitlines()]
        self.assertEqual(len(frames), 3)
        self.assertIn("error", frames[1])
        self.assertEqual(frames[2]["value"], {"ok": True})

    def test_trace_creation_is_exclusive_private_and_captures_io(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "trace.jsonl")
            trace = host.PrivateTrace(path)
            trace.write({"request": {"secret": "local"}, "result": {"ok": True}})
            trace.close()
            self.assertEqual(os.stat(path).st_mode & 0o777, 0o600)
            with open(path, encoding="utf-8") as stream:
                self.assertEqual(json.loads(stream.readline())["request"]["secret"], "local")
            with self.assertRaises(FileExistsError):
                host.PrivateTrace(path)

    def test_existing_world_and_path_traversal_are_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            host.ensure_new_output(directory, "new-run", "task_1")
            os.makedirs(os.path.join(directory, "new-run", "tasks", "task_1"))
            with self.assertRaises(FileExistsError):
                host.ensure_new_output(directory, "new-run", "task_1")
            with self.assertRaises(host.ProtocolError):
                host.ensure_new_output(directory, "../escape", "task_1")


if __name__ == "__main__":
    unittest.main()
