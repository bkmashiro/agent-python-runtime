#!/usr/bin/env python3
"""Trusted, single-world AppWorld JSON-lines host bridge."""

import argparse
import hashlib
import importlib.metadata
import json
import os
import re
import stat
import sys
import time
from contextlib import redirect_stdout


_IDENTIFIER = re.compile(r"^[A-Za-z][A-Za-z0-9_]*$")
_FORBIDDEN_ARGUMENTS = {
    "client",
    "track",
    "show",
    "raise_on_failure",
    "_app_name",
    "_api_name",
    "_system_datetime",

}


class ProtocolError(ValueError):
    pass


def _real_ns():
    # AppWorld freezes task time. Measurements must use the original wall clock.
    try:
        import freezegun.api
        return int(freezegun.api.real_perf_counter() * 1_000_000_000)
    except ImportError:
        return time.perf_counter_ns()


class PrivateTrace:
    """Append-only local trace created exclusively with owner-only permissions."""

    def __init__(self, path):
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
        if hasattr(os, "O_NOFOLLOW"):
            flags |= os.O_NOFOLLOW
        self.fd = os.open(path, flags, 0o600)
        os.fchmod(self.fd, stat.S_IRUSR | stat.S_IWUSR)
        self.stream = os.fdopen(self.fd, "w", encoding="utf-8")

    def write(self, value):
        self.stream.write(json.dumps(value, ensure_ascii=False, separators=(",", ":")) + "\n")
        self.stream.flush()
        os.fsync(self.stream.fileno())

    def close(self):
        if not self.stream.closed:
            self.stream.close()


class Bridge:
    def __init__(self, world, task, experiment):
        self.world = world
        self.task = task
        self.experiment = experiment
        self.tools = self._build_allowlist(world.apis)
        self.closed = False

    @staticmethod
    def _build_allowlist(apis):
        allowed = {}
        for app_name, app_apis in apis.items():
            if not isinstance(app_name, str) or app_name == "admin" or not _IDENTIFIER.match(app_name):
                continue
            try:
                entries = app_apis.items()
            except AttributeError:
                continue
            for api_name, function in entries:
                if (not isinstance(api_name, str) or not _IDENTIFIER.match(api_name)
                        or api_name.startswith("_") or not callable(function)):
                    continue
                allowed[app_name + "." + api_name] = function
        return allowed

    def ready_frame(self):
        return {"kind": "ready", "tools": [
            {"name": name, "python_path": "apis." + name}
            for name in sorted(self.tools)
        ]}

    def _validate_request(self, request):
        if not isinstance(request, dict):
            raise ProtocolError("request must be a JSON object")
        if "id" not in request or not isinstance(request["id"], (str, int)) or isinstance(request["id"], bool):
            raise ProtocolError("request id must be a string or integer")
        op = request.get("op")
        if op == "call":
            tool = request.get("tool")
            args = request.get("arguments")
            if not isinstance(tool, str) or tool not in self.tools:
                raise ProtocolError("tool is not in the public API allowlist")
            if not isinstance(args, dict):
                raise ProtocolError("arguments must be a JSON object")
            bad = [key for key in args if not isinstance(key, str) or key.startswith("_") or key in _FORBIDDEN_ARGUMENTS]
            if bad:
                raise ProtocolError("bridge-control argument is not permitted: " + repr(bad[0]))
            return op
        if op == "finish":
            if set(request) != {"id", "op"}:
                raise ProtocolError("finish accepts only id and op")
            return op
        raise ProtocolError("unsupported operation")

    def handle(self, request, evaluator=None):
        request_id = request.get("id") if isinstance(request, dict) else None
        try:
            op = self._validate_request(request)
            if self.closed:
                raise ProtocolError("world is closed")
            if op == "call":
                value = self.tools[request["tool"]](**request["arguments"])
                # Reject unserializable API results rather than leaking reprs or source.
                json.dumps(value, ensure_ascii=False)
                return {"id": request_id, "value": value}
            if evaluator is None:
                from appworld.evaluator import evaluate_task
                evaluator = lambda task, experiment: evaluate_task(
                    task_id=task, experiment_name=experiment
                ).to_dict(stats_only=True)
            self.world._save_state(self.world.output_db_home_path_on_disk)
            self.world.save_logs()
            value = evaluator(self.task, self.experiment)
            json.dumps(value, ensure_ascii=False)
            return {"id": request_id, "value": value}
        except ProtocolError as error:
            return {"id": request_id, "error": str(error), "fatal": True}
        except Exception as error:
            # Tool errors belong to the Python call site; Python may catch them.
            return {"id": request_id, "error": str(error) or error.__class__.__name__}
        finally:
            if isinstance(request, dict) and request.get("op") == "finish":
                self.close()

    def close(self):
        if not self.closed:
            self.closed = True
            self.world.close()


def run_protocol(world, task, experiment, trace, stdin=None, stdout=None):
    stdin = stdin or sys.stdin
    stdout = stdout or sys.stdout
    bridge = Bridge(world, task, experiment)
    try:
        stdout.write(json.dumps(bridge.ready_frame(), separators=(",", ":")) + "\n")
        stdout.flush()
        for line in stdin:
            try:
                request = json.loads(line)
            except Exception as error:
                trace.write({"request_raw": line.rstrip("\n"), "result": {"error": str(error)}})
                stdout.write(json.dumps({"id": None, "error": "invalid JSON request"}) + "\n")
                stdout.flush()
                break
            # API implementations and evaluator helpers must not contaminate stdout.
            trace.write({"kind": "request_start", "request": request})
            began = _real_ns()
            with redirect_stdout(sys.stderr):
                result = bridge.handle(request)
            trace.write({"kind": "request_end", "request": request, "result": result,
                         "elapsed_ns": _real_ns() - began})
            stdout.write(json.dumps(result, ensure_ascii=False, separators=(",", ":")) + "\n")
            stdout.flush()
            if result.get("fatal") or (isinstance(request, dict) and request.get("op") == "finish"):
                break
    finally:
        with redirect_stdout(sys.stderr):
            bridge.close()


def _parser():
    parser = argparse.ArgumentParser(description="Run the trusted AppWorld JSON-lines bridge")
    parser.add_argument("--task", required=True)
    parser.add_argument("--experiment", required=True)
    parser.add_argument("--trace", required=True)
    return parser


def ensure_new_output(base, experiment, task):
    if not all(re.fullmatch(r"[A-Za-z0-9_-]+", value) for value in (experiment, task)):
        raise ProtocolError("task and experiment must be simple identifiers")
    output = os.path.join(base, experiment, "tasks", task)
    if os.path.lexists(output):
        raise FileExistsError("refusing to reset existing task output")


def main(argv=None):
    args = _parser().parse_args(argv)
    root = os.environ.get("APPWORLD_ROOT")
    if not root:
        _parser().error("APPWORLD_ROOT environment variable is required")
    root = os.path.abspath(root)
    if not os.path.isdir(root):
        _parser().error("APPWORLD_ROOT must name an existing directory")
    trace = PrivateTrace(args.trace)
    try:
        with open(__file__, "rb") as source:
            source_sha256 = hashlib.sha256(source.read()).hexdigest()
        version = importlib.metadata.version("appworld")
        if version != "0.1.3.post1":
            raise RuntimeError("this benchmark bridge requires appworld==0.1.3.post1")
        trace.write({"kind": "header", "appworld_version": version,
                     "host_source_sha256": source_sha256, "python": sys.version,
                     "task": args.task, "experiment": args.experiment,
                     "load_ground_truth": False, "raise_on_failure": False})
        # Keep AppWorld initialization, APIs, and grading chatter off protocol stdout.
        with redirect_stdout(sys.stderr):
            os.chdir(root)
            from appworld.environment import AppWorld
            from appworld.common.path_store import path_store
            ensure_new_output(path_store.experiment_outputs, args.experiment, args.task)
            world = AppWorld(
                task_id=args.task,
                experiment_name=args.experiment,
                load_ground_truth=False,
                raise_on_failure=False,  # Match the official reference verification path.
            )
        run_protocol(world, args.task, args.experiment, trace)
    finally:
        trace.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
