"""Single-invocation native Python runner. Local compatibility is intentionally broad."""
from __future__ import annotations
import json
import sys
import traceback
import _agent_runtime_host

_MAX_INPUT_BYTES = 16 * 1024 * 1024
_MAX_ERROR_CHARS = 4096

def _strict_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result

def _emit(document):
    sys.stdout.write(json.dumps(document, separators=(",", ":"), ensure_ascii=False, allow_nan=False))
    sys.stdout.write("\n")

def main() -> int:
    raw = sys.stdin.buffer.read(_MAX_INPUT_BYTES + 1)
    if not raw or len(raw) > _MAX_INPUT_BYTES:
        _emit({"status":"error","error":{"code":"invalid_request","message":"native request is empty or oversized"}})
        return 2
    try:
        envelope = json.loads(raw, object_pairs_hook=_strict_object)
        if set(envelope) != {"schema_version", "request", "trusted_prepare"} or envelope["schema_version"] != "pysolate.native-run.v1":
            raise ValueError("invalid native envelope")
        request = envelope["request"]
        if not isinstance(request, dict) or not {"run_id", "code", "inputs"}.issubset(request) or not isinstance(request["code"], str):
            raise ValueError("invalid run request")
        if not isinstance(envelope["trusted_prepare"], str):
            raise ValueError("trusted_prepare must be a string")
        readiness = _agent_runtime_host.ready()
        namespace = {"inputs": request["inputs"]}
        if envelope["trusted_prepare"]:
            exec(compile(envelope["trusted_prepare"], "<host-projection>", "exec"), namespace, namespace)
        exec(compile(request["code"], "<agent-python-native>", "exec"), namespace, namespace)
        if "result" not in namespace:
            raise RuntimeError("agent code did not assign result")
        encoded = json.dumps(namespace["result"], separators=(",", ":"), ensure_ascii=False, allow_nan=False)
        _emit({"status":"ok", "result":json.loads(encoded), "readiness":readiness})
        return 0
    except Exception as exc:
        message = str(exc) or type(exc).__name__
        detail = "".join(traceback.format_exception_only(type(exc), exc)).strip()
        _emit({"status":"error","error":{"code":"native_execution_failed","message":message[:_MAX_ERROR_CHARS],"detail":detail[:_MAX_ERROR_CHARS]}})
        return 1

if __name__ == "__main__":
    raise SystemExit(main())
