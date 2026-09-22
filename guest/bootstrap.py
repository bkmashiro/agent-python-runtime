"""Execution convention: inputs in, result out; optional whole-program early reads."""
import json
import os
import sys
from _pysolate import call, prepare, resolve
import pysolate

def decode(response):
    response = json.loads(response)
    if "error" in response:
        raise RuntimeError(response["error"])
    return response["value"]


def invoke_tool(name, args):
    request = json.dumps({"tool": name, "args": args})
    return decode(call(request))


def make_tool(name):
    def proxy(**args):
        return invoke_tool(name, args)
    proxy.__name__ = name
    return proxy


def prepare_call(thunk):
    try:
        name, args = thunk()
        request = json.dumps({"tool": name, "args": args})
        return prepare(request)
    except Exception:
        # Argument lookup/encoding errors belong at the original Python call, not here.
        return 0


def resolve_call(handle, name, **args):
    return decode(resolve(handle, json.dumps({"tool": name, "args": args})))


def execute(request):
    transformed = ""
    try:
        request = json.loads(request)
        scope = {"__name__": "__main__", "inputs": request["inputs"]}
        scope.update(pysolate.configure(request["manifest"], invoke_tool))
        if request.get("workspace"):
            os.chdir("/workspace")
            if "/workspace" not in sys.path:
                sys.path.insert(0, "/workspace")
        code = request["source"]
        if request.get("plm"):
            import ast
            from plm import transform
            code, helpers = transform(code, request["manifest"])
            if helpers:
                scope.update(zip(helpers, (prepare_call, resolve_call)))
                transformed = ast.unparse(code)
        exec(compile(code, "<pysolate>", "exec"), scope)
        response = {"value": scope.get("result"), "transformed": transformed}
        return json.dumps(response).encode("utf-8")
    except BaseException as error:
        return json.dumps({"error": f"{type(error).__name__}: {error}", "transformed": transformed}).encode("utf-8")
    finally:
        sys.stdout.flush()
