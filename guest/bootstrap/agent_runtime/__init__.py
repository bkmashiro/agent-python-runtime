"""Trusted bootstrap for the neutral Agent Python Runtime guest."""

from __future__ import annotations

import __future__
import ast
import codeop
import dis
import hashlib
import inspect
import json
import operator
import sys
import time
import traceback
import types
from typing import Any

from .ast_support import ast_digest_bounded, fix_missing_locations_bounded, validate_ast_recursion_shape_bounded, walk_ast_bounded

_ALLOWED_REQUEST_FIELDS = {"run_id", "code", "inputs", "output_schema", "compatibility", "requirements"}
_TRACEBACK_MAX = 16_384
_prepared_globals: dict[str, Any] = {}
_runtime_config: dict[str, Any] = {}
_validated_request_json: str | None = None
_validated_code: types.CodeType | None = None
_validated_source_tree: ast.Module | None = None
_validated_import_globals: dict[str, Any] = {}
_validated_effective_ast_sha256: str | None = None
_prepared_numpy_installed = False
_stream_session: _StreamingSession | None = None
_SOURCE_CONTRACT_OK = 0
_SOURCE_CONTRACT_UNSUPPORTED = 1
_SOURCE_CONTRACT_INVALID = 2
_FORBIDDEN_DYNAMIC_NAMES = {"__import__", "eval", "exec", "import_module"}
_FORBIDDEN_IMPORT_ROOTS = {"builtins", "importlib"}
_ORIGINAL_IMPORT = __import__
_WRAPPER_CONTRACT = "pysolate.agent-output-wrapper.v1:return+legacy-result+bounded-stdout"
_WRAPPER_CONTRACT_SHA256 = "sha256:" + hashlib.sha256(_WRAPPER_CONTRACT.encode("utf-8")).hexdigest()
_WRAPPER_MAIN = "_pysolate_agent_main"
_WRAPPER_RETURN = "_pysolate_explicit_return"
_WRAPPER_FALLTHROUGH = "_pysolate_fallthrough"
_WRAPPER_MISSING = "_pysolate_missing"
_WRAPPER_GLOBALS = "_pysolate_module_globals"
_STDOUT_MAX_BYTES = 64 * 1024
_STDOUT_MAX_LINES = 256
_STDOUT_TRUNCATION_MARKER = "[pysolate stdout truncated]"
_PREPARED_REGION_HELPER = "__pysolate_materialize_value__"
_PREPARED_REGION_PAYLOAD_MAX = 256
_PLM_PREPARE_HELPER = "_pysolate_plm_prepare"
_PLM_LINEARIZE_HELPER = "_pysolate_plm_linearize"
_VALUE_SLOT_HELPER = "_pysolate_materialize_slot"
_plm_occurrence_counts: dict[str, int] = {}
_plm_pending_slots: dict[str, list[tuple[str, str, str]]] = {}
_FUTURE_FLAGS = sum(getattr(__future__, name).compiler_flag for name in __future__.all_feature_names)


class _OutputLimitExceeded(Exception):
    pass


class _BoundedStdout:
    def __init__(self) -> None:
        self._parts: list[str] = []
        self._bytes = 0
        self._truncated = False

    @property
    def truncated(self) -> bool:
        return self._truncated

    def write(self, value: str) -> int:
        if not isinstance(value, str):
            raise TypeError("stdout writes must be strings")
        encoded = value.encode("utf-8")
        if self._bytes + len(encoded) > _STDOUT_MAX_BYTES:
            marker_bytes = len(_STDOUT_TRUNCATION_MARKER.encode("utf-8"))
            content_budget = _STDOUT_MAX_BYTES - marker_bytes
            combined = ("".join(self._parts) + value).encode("utf-8")
            prefix = combined[:content_budget].decode("utf-8", errors="ignore")
            self._parts = [prefix]
            self._bytes = len(prefix.encode("utf-8"))
            self._truncated = True
            raise _OutputLimitExceeded("model logs exceed the output byte bound")
        self._parts.append(value)
        self._bytes += len(encoded)
        return len(value)

    def flush(self) -> None:
        return None

    def logs(self) -> list[str]:
        text = "".join(self._parts)
        lines = text.splitlines()
        if text and not lines:
            lines = [text]
        truncated = self._truncated or len(lines) > _STDOUT_MAX_LINES
        if truncated:
            lines = [*lines[: _STDOUT_MAX_LINES - 1], _STDOUT_TRUNCATION_MARKER]
        return lines


def _declared_sealed_importer(declared_roots: list[str]):
    admitted = frozenset(declared_roots)

    def sealed_import(name, globals=None, locals=None, fromlist=(), level=0):
        if not isinstance(name, str) or not name or not isinstance(level, int) or level < 0:
            raise ImportError("invalid sealed import request")
        resolved = name
        if level:
            package = globals.get("__package__") if isinstance(globals, dict) else None
            if not isinstance(package, str) or not package:
                raise ImportError("relative import has no package context")
            parts = package.split(".")
            if level > len(parts):
                raise ImportError("relative import escapes package")
            prefix = parts[: len(parts) - level + 1]
            resolved = ".".join([*prefix, *([name] if name else [])])
        root = resolved.partition(".")[0]
        if root not in admitted:
            raise ImportError(f"module root is outside the declared import set: {root}")
        sys.audit("agent_runtime.import", resolved)
        return _ORIGINAL_IMPORT(name, globals, locals, fromlist, level)

    return sealed_import


class _StreamingSession:
    """Trusted append-only executor using this exact Guest interpreter."""

    def __init__(self, inputs: Any, speculation_max_calls: int):
        if not isinstance(speculation_max_calls, int) or isinstance(speculation_max_calls, bool) or speculation_max_calls < 0:
            raise ValueError("speculation_max_calls must be a non-negative integer")
        builtins_source = vars(__builtins__) if isinstance(__builtins__, types.ModuleType) else __builtins__
        restricted = dict(builtins_source)
        for forbidden in ("__import__", "eval", "exec", "compile"):
            restricted.pop(forbidden, None)
        self.namespace: dict[str, Any] = dict(_prepared_globals)
        self.namespace.update({"__builtins__": restricted, "inputs": inputs})
        self.compiler = codeop.CommandCompiler()
        self.started = time.monotonic()
        self.timeline: list[dict[str, Any]] = []
        self.source = ""
        self.pending = ""
        self.executed_bytes = 0
        self.suites: list[dict[str, Any]] = []
        self.ended = False
        self.preamble_open = True
        self.imports_sealed = False
        self.staged_results: dict[str, tuple[bool, Any]] = {}
        self.preflighted_occurrences: set[str] = set()
        self.consumed_occurrences: set[str] = set()
        self.stdout = _BoundedStdout()
        self.speculation_max_calls = speculation_max_calls
        self.namespace["_stream_invoke_eager"] = self._invoke_eager

    def _occurrence(self, node: ast.Call) -> str:
        return f"{self.executed_bytes}:{node.lineno}:{node.col_offset}"

    def _elapsed_ms(self) -> float:
        return max(0.0, (time.monotonic() - self.started) * 1000.0)

    def _invoke_eager(self, occurrence: str, target: Any, *args: Any, **kwargs: Any) -> Any:
        if occurrence in self.staged_results:
            self.consumed_occurrences.add(occurrence)
            succeeded, value = self.staged_results.pop(occurrence)
            if not succeeded:
                raise value
            return value
        result = target(*args, **kwargs)
        self.consumed_occurrences.add(occurrence)
        return result

    def _eager_preflight(self, tree: ast.Module) -> None:
        eager = self.namespace.get("_stream_eager_calls", {})
        if not isinstance(eager, dict):
            return
        calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call)]
        calls.sort(key=operator.attrgetter("lineno", "col_offset"))
        for node in calls:
            if len(self.preflighted_occurrences) >= self.speculation_max_calls:
                return

            if isinstance(node.func, ast.Name):
                name = node.func.id
            elif isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Name):
                name = node.func.value.id + "." + node.func.attr
            else:
                continue
            target = eager.get(name)
            if target is None or any(keyword.arg is None for keyword in node.keywords):
                continue
            try:
                arguments = [ast.literal_eval(value) for value in node.args]
                keywords = {keyword.arg: ast.literal_eval(keyword.value) for keyword in node.keywords}
            except (ValueError, TypeError):
                continue
            occurrence = self._occurrence(node)
            started = self._elapsed_ms()
            try:
                staged = (True, target(*arguments, **keywords))
                outcome = "ok"
            except BaseException as exc:
                staged = (False, exc)
                outcome = "error"
            self.staged_results[occurrence] = staged
            self.timeline.append({"kind": "eager_read", "occurrence": occurrence, "outcome": outcome, "start_ms": started, "end_ms": self._elapsed_ms()})
            self.preflighted_occurrences.add(occurrence)

    def _rewrite_eager(self, tree: ast.Module) -> ast.Module:
        eager = self.namespace.get("_stream_eager_calls", {})
        session = self

        class Rewriter(ast.NodeTransformer):
            def visit_Call(self, node: ast.Call) -> ast.AST:
                self.generic_visit(node)
                if isinstance(node.func, ast.Name):
                    name = node.func.id
                elif isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Name):
                    name = node.func.value.id + "." + node.func.attr
                else:
                    return node
                if not isinstance(eager, dict) or name not in eager:
                    return node
                return ast.copy_location(ast.Call(
                    func=ast.Name(id="_stream_invoke_eager", ctx=ast.Load()),
                    args=[ast.Constant(session._occurrence(node)), node.func, *node.args],
                    keywords=node.keywords,
                ), node)

        return ast.fix_missing_locations(Rewriter().visit(tree))

    def chunk(self, text: str) -> dict[str, Any]:
        if self.ended or not isinstance(text, str) or not text:
            raise ValueError("stream chunk must be non-empty before end")
        encoded = text.encode("utf-8")
        if len(self.source.encode("utf-8")) + len(encoded) > 1_048_576:
            raise ValueError("stream source exceeds one MiB")
        self.source += text
        self.pending += text
        try:
            code = self.compiler(self.pending, "<agent-stream>", "exec")
        except (SyntaxError, ValueError, TypeError, MemoryError) as exc:
            raise SyntaxError("invalid streamed Python source") from exc
        if code is None:
            return {"status": "incomplete", "suites": []}
        status, admitted, imports = _validate_agent_source(self.source, None)
        if status != _SOURCE_CONTRACT_OK or admitted is None:
            raise SyntaxError("stream source violates the static preamble contract")
        pending_tree = ast.parse(self.pending, filename="<agent-stream>", mode="exec")
        pending_has_import = any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in pending_tree.body)
        pending_is_preamble = all(isinstance(node, (ast.Import, ast.ImportFrom)) or _module_docstring(node) for node in pending_tree.body)
        execution_started = self._elapsed_ms()
        if pending_has_import and (not self.preamble_open or not pending_is_preamble):
            raise SyntaxError("late import in streamed Python source")
        previous_stdout = sys.stdout
        previous_dunder_stdout = sys.__stdout__
        sys.stdout = self.stdout
        sys.__stdout__ = self.stdout
        try:
            if pending_is_preamble:
                import_namespace = {"__builtins__": __builtins__}
                exec(code, import_namespace, import_namespace)
                for key, value in import_namespace.items():
                    if key != "__builtins__":
                        self.namespace[key] = value
            else:
                if not self.imports_sealed:
                    import _agent_runtime_host  # type: ignore[import-not-found]
                    _agent_runtime_host.seal_imports(tuple(sorted(sys.modules)))
                    self.imports_sealed = True
                self.preamble_open = False
                self._eager_preflight(pending_tree)
                rewritten = self._rewrite_eager(pending_tree)
                exec(compile(rewritten, "<agent-stream>", "exec", flags=code.co_flags & _FUTURE_FLAGS, dont_inherit=True), self.namespace, self.namespace)
            if self.stdout.truncated:
                raise _OutputLimitExceeded("model logs exceed the output byte bound")
        except BaseException:
            self.ended = True
            raise
        finally:
            sys.stdout = previous_stdout
            sys.__stdout__ = previous_dunder_stdout
        start = self.executed_bytes
        end = len(self.source.encode("utf-8"))
        digest = hashlib.sha256(self.pending.encode("utf-8")).hexdigest()
        suite = {"start": start, "end": end, "sha256": "sha256:" + digest}
        suite["end_ms"] = self._elapsed_ms()
        if not pending_is_preamble:
            suite["start_ms"] = execution_started
        else:
            suite["start_ms"] = suite["end_ms"]
        self.suites.append(suite)
        self.timeline.append({"kind": "suite", **suite})
        self.executed_bytes = end
        self.pending = ""
        return {"status": "complete", "suites": [dict(suite)]}

    def end(self) -> dict[str, Any]:
        if self.ended:
            raise ValueError("stream already ended")
        self.ended = True
        if self.pending:
            try:
                code = self.compiler(self.pending, "<agent-stream>", "exec")
            except (SyntaxError, ValueError, TypeError, MemoryError) as exc:
                raise SyntaxError("invalid final streamed Python source") from exc
            if code is None:
                raise SyntaxError("incomplete final streamed Python source")
        status, _, _ = _validate_agent_source(self.source, None)
        if status != _SOURCE_CONTRACT_OK:
            raise SyntaxError("final stream violates the source contract")
        result_present = "result" in self.namespace
        return {
            "result": self.namespace.get("result"),
            "logs": self.stdout.logs(),
            "result_present": result_present,
            "result_source": "legacy_result" if result_present else "missing",
            "suites": [dict(value) for value in self.suites],
            "timeline": [dict(value) for value in self.timeline],
            "eager": {
                "dispatched": len(self.preflighted_occurrences),
                "consumed": len(self.consumed_occurrences),
                "orphaned": len(self.preflighted_occurrences - self.consumed_occurrences),
            },
        }


def _stream_begin(inputs: Any, speculation_max_calls: int = 0) -> None:
    global _stream_session
    _stream_session = _StreamingSession(inputs, speculation_max_calls)


def _stream_chunk(text: str) -> dict[str, Any]:
    if _stream_session is None:
        raise ValueError("stream has not begun")
    return _stream_session.chunk(text)


def _stream_end() -> dict[str, Any]:
    if _stream_session is None:
        raise ValueError("stream has not begun")
    return _stream_session.end()


def _stream_cancel() -> None:
    global _stream_session
    _stream_session = None


def _error(code: str, message: str, *, error_type: str | None = None, trace: str | None = None) -> dict[str, Any]:
    bounded_message = message or error_type or code
    detail: dict[str, Any] = {"code": code, "message": bounded_message[:4096]}
    if error_type is not None:
        detail["error_type"] = error_type[:256]
    if trace is not None:
        detail["traceback"] = trace[-_TRACEBACK_MAX:]
    return {
        "status": "error",
        "result": None,
        "receipts": [],
        "metrics": {"capability_calls": 0, "result_bytes": 0},
        "error": detail,
    }


def _initialize(config_json: str) -> None:
    if not isinstance(config_json, str):
        raise TypeError("config_json must be a string")
    value = json.loads(config_json)
    if not isinstance(value, dict):
        raise ValueError("runtime config must be an object")
    global _runtime_config, _prepared_globals, _validated_request_json, _validated_code, _validated_source_tree, _validated_import_globals, _validated_effective_ast_sha256, _prepared_numpy_installed
    _runtime_config = dict(value)
    _prepared_globals = {}
    _prepared_numpy_installed = False
    _validated_request_json = None
    _validated_code = None
    _validated_source_tree = None
    _validated_import_globals = {}
    _validated_effective_ast_sha256 = None
    plm_source_pass = sys.modules.get(f"{__name__}.plm_source_pass")
    if plm_source_pass is not None:
        plm_source_pass.reset_state()



def _decode_prepared_numpy_descriptor(descriptor_json: str, body: bytes | bytearray) -> dict[str, Any]:
    if not isinstance(descriptor_json, str) or len(descriptor_json.encode("utf-8")) > 4096:
        raise ValueError("prepared numpy descriptor exceeds the bound")
    if not isinstance(body, (bytes, bytearray)) or not body or len(body) > 8 * 1024 * 1024:
        raise ValueError("prepared numpy body exceeds the bound")

    def unique_object(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError("prepared numpy descriptor has a duplicate field")
            value[key] = item
        return value

    value = json.loads(descriptor_json, object_pairs_hook=unique_object)
    fields = {
        "schema_version", "name", "codec", "dtype", "shape", "order",
        "endianness", "nbytes", "body_sha256", "input_sha256",
    }
    if not isinstance(value, dict) or set(value) != fields:
        raise ValueError("prepared numpy descriptor fields are invalid")
    canonical = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    if canonical != descriptor_json:
        raise ValueError("prepared numpy descriptor is not canonical")
    name = value["name"]
    if (
        not isinstance(name, str)
        or not name
        or len(name) > 128
        or not (name[0] == "_" or "A" <= name[0] <= "Z" or "a" <= name[0] <= "z")
        or any(not (character == "_" or "A" <= character <= "Z" or "a" <= character <= "z" or "0" <= character <= "9") for character in name[1:])
        or name == "__builtins__"
    ):
        raise ValueError("prepared numpy name is invalid")
    dtype_sizes = {
        "|b1": 1, "|i1": 1, "|u1": 1,
        "<i2": 2, "<u2": 2, "<f2": 2,
        "<i4": 4, "<u4": 4, "<f4": 4, "<c8": 8,
        "<i8": 8, "<u8": 8, "<f8": 8, "<c16": 16,
    }
    dtype = value["dtype"]
    shape = value["shape"]
    if dtype not in dtype_sizes or not isinstance(shape, list) or not shape or len(shape) > 8:
        raise ValueError("prepared numpy layout is invalid")
    elements = 1
    for dimension in shape:
        if not isinstance(dimension, int) or isinstance(dimension, bool) or dimension <= 0 or dimension > 1 << 31:
            raise ValueError("prepared numpy shape is invalid")
        elements *= dimension
        if elements > 8 * 1024 * 1024:
            raise ValueError("prepared numpy shape exceeds the bound")
    expected_bytes = elements * dtype_sizes[dtype]
    expected_endianness = "not_applicable" if dtype_sizes[dtype] == 1 else "little"
    if (
        value["schema_version"] != "pysolate.prepared-numpy-input.v1"
        or value["codec"] != "numpy_ndarray_c_v1"
        or value["order"] != "C"
        or value["endianness"] != expected_endianness
        or not isinstance(value["nbytes"], int)
        or isinstance(value["nbytes"], bool)
        or value["nbytes"] != expected_bytes
        or value["nbytes"] != len(body)
    ):
        raise ValueError("prepared numpy descriptor does not match the body")
    for digest_name in ("body_sha256", "input_sha256"):
        digest = value[digest_name]
        if not isinstance(digest, str) or len(digest) != 71 or not digest.startswith("sha256:") or any(character not in "0123456789abcdef" for character in digest[7:]):
            raise ValueError("prepared numpy digest is invalid")
    actual_body_sha256 = "sha256:" + hashlib.sha256(body).hexdigest()
    if value["body_sha256"] != actual_body_sha256:
        raise ValueError("prepared numpy body digest mismatch")
    return value


def _prepare_numpy_ndarray(descriptor_json: str, body: bytes | bytearray) -> None:
    global _prepared_globals, _prepared_numpy_installed
    if _prepared_numpy_installed:
        raise RuntimeError("prepared numpy input was already prepared")
    descriptor = _decode_prepared_numpy_descriptor(descriptor_json, body)
    numpy = _ORIGINAL_IMPORT("numpy")
    dtype = numpy.dtype(descriptor["dtype"])
    if dtype.str != descriptor["dtype"]:
        raise ValueError("prepared numpy dtype is not exact on this artifact")
    backing = body if isinstance(body, bytearray) else bytearray(body)
    array = numpy.frombuffer(backing, dtype=dtype).reshape(tuple(descriptor["shape"]), order="C")
    if not array.flags.c_contiguous or array.nbytes != descriptor["nbytes"]:
        raise ValueError("prepared numpy reconstruction is invalid")
    _prepared_globals = {"__builtins__": __builtins__, descriptor["name"]: array}
    _prepared_numpy_installed = True


def _prepare(source: str) -> None:
    if not isinstance(source, str):
        raise TypeError("source must be a string")
    namespace: dict[str, Any] = {"__builtins__": __builtins__}
    global _prepared_globals
    # Trusted preparation may intentionally build a streaming session while
    # definitions are still being installed into this private namespace.
    _prepared_globals = namespace
    exec(compile(source, "<trusted-prepare>", "exec", dont_inherit=True), namespace, namespace)



def _decode_request(request_json: str) -> tuple[dict[str, Any] | None, dict[str, Any] | None]:
    try:
        request = json.loads(request_json)
    except (TypeError, ValueError) as exc:
        return None, _error("invalid_request", f"invalid JSON: {exc}")
    if not isinstance(request, dict):
        return None, _error("invalid_request", "request must be an object")
    unknown = sorted(set(request) - _ALLOWED_REQUEST_FIELDS)
    missing = sorted({"run_id", "code", "inputs"} - set(request))
    if unknown:
        return None, _error("invalid_request", f"unknown fields: {', '.join(unknown)}")
    if missing:
        return None, _error("invalid_request", f"missing fields: {', '.join(missing)}")
    if not isinstance(request["run_id"], str) or not request["run_id"]:
        return None, _error("invalid_request", "run_id must be a non-empty string")
    if not isinstance(request["code"], str):
        return None, _error("invalid_request", "code must be a string")
    requirements = request.get("requirements", [])
    if not isinstance(requirements, list) or requirements:
        return None, _error("invalid_request", "non-empty requirements must be rejected by Host admission")
    compatibility = request.get("compatibility")
    if compatibility is not None:
        if (
            not isinstance(compatibility, dict)
            or set(compatibility) != {"profile", "imports"}
            or not isinstance(compatibility.get("profile"), str)
            or not isinstance(compatibility.get("imports"), list)
            or any(not isinstance(name, str) for name in compatibility.get("imports", []))
        ):
            return None, _error("invalid_request", "compatibility declaration must be admitted by Host")
    return request, None


def _module_docstring(node: ast.stmt) -> bool:
    return isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str)


def _code_objects(code: types.CodeType):
    yield code
    for value in code.co_consts:
        if isinstance(value, types.CodeType):
            yield from _code_objects(value)


class _TopLevelReturnTransformer(ast.NodeTransformer):
    def __init__(self, future_annotations: bool) -> None:
        self._future_annotations = future_annotations

    def visit_FunctionDef(self, node: ast.FunctionDef):
        return node

    def visit_AsyncFunctionDef(self, node: ast.AsyncFunctionDef):
        return node

    def visit_ClassDef(self, node: ast.ClassDef):
        return node

    def visit_Lambda(self, node: ast.Lambda):
        return node

    def visit_BinOp(self, node: ast.BinOp):
        return node

    def visit_Return(self, node: ast.Return):
        value = node.value if node.value is not None else ast.Constant(value=None)
        rewritten = ast.Return(value=ast.Tuple(elts=[ast.Name(id=_WRAPPER_RETURN, ctx=ast.Load()), value], ctx=ast.Load()))
        return ast.copy_location(rewritten, node)

    def visit_AnnAssign(self, node: ast.AnnAssign):
        if not isinstance(node.target, ast.Name) or not node.simple:
            return self.generic_visit(node)
        annotation: ast.expr = ast.Constant(value=ast.unparse(node.annotation)) if self._future_annotations else node.annotation
        statements: list[ast.stmt] = []
        if node.value is not None:
            statements.append(ast.Assign(targets=[node.target], value=self.visit(node.value)))
        statements.append(_module_annotation_store(node.target.id, annotation))
        return [ast.copy_location(statement, node) for statement in statements]


def _module_annotation_store(name: str, annotation: ast.expr) -> ast.Assign:
    annotations = ast.Call(
        func=ast.Attribute(value=ast.Name(id=_WRAPPER_GLOBALS, ctx=ast.Load()), attr="setdefault", ctx=ast.Load()),
        args=[ast.Constant(value="__annotations__"), ast.Dict(keys=[], values=[])], keywords=[])
    return ast.Assign(targets=[ast.Subscript(value=annotations, slice=ast.Constant(value=name), ctx=ast.Store())], value=annotation)


class _ModuleAssignedNameCollector(ast.NodeVisitor):
    """Collect names that module execution would bind in its global namespace."""

    def __init__(self) -> None:
        self.names: set[str] = set()

    def visit_Name(self, node: ast.Name) -> None:
        if isinstance(node.ctx, (ast.Store, ast.Del)):
            self.names.add(node.id)

    def visit_BinOp(self, node: ast.BinOp) -> None:
        stack: list[ast.AST] = [node]
        while stack:
            current = stack.pop()
            if isinstance(current, ast.BinOp):
                stack.append(current.right)
                stack.append(current.left)
            else:
                self.visit(current)

    def visit_Global(self, node: ast.Global) -> None:
        self.names.update(node.names)

    def visit_Import(self, node: ast.Import) -> None:
        for alias in node.names:
            self.names.add(alias.asname or alias.name.split(".", 1)[0])

    def visit_ImportFrom(self, node: ast.ImportFrom) -> None:
        for alias in node.names:
            if alias.name != "*":
                self.names.add(alias.asname or alias.name)

    def _visit_function_binding(self, node: ast.FunctionDef | ast.AsyncFunctionDef) -> None:
        self.names.add(node.name)
        arguments = (*node.args.posonlyargs, *node.args.args, *node.args.kwonlyargs)
        for argument in arguments:
            if argument.annotation is not None:
                self.visit(argument.annotation)
        for argument in (node.args.vararg, node.args.kwarg):
            if argument is not None and argument.annotation is not None:
                self.visit(argument.annotation)
        if node.returns is not None:
            self.visit(node.returns)
        for value in (*node.decorator_list, *node.args.defaults, *node.args.kw_defaults):
            if value is not None:
                self.visit(value)

    def visit_FunctionDef(self, node: ast.FunctionDef) -> None:
        self._visit_function_binding(node)

    def visit_AsyncFunctionDef(self, node: ast.AsyncFunctionDef) -> None:
        self._visit_function_binding(node)

    def visit_ClassDef(self, node: ast.ClassDef) -> None:
        self.names.add(node.name)
        for value in (*node.decorator_list, *node.bases):
            self.visit(value)
        for keyword in node.keywords:
            self.visit(keyword.value)

    def visit_Lambda(self, node: ast.Lambda) -> None:
        for value in (*node.args.defaults, *node.args.kw_defaults):
            if value is not None:
                self.visit(value)

    def visit_ExceptHandler(self, node: ast.ExceptHandler) -> None:
        if node.name:
            self.names.add(node.name)
        if node.type is not None:
            self.visit(node.type)
        for statement in node.body:
            self.visit(statement)

    def visit_MatchAs(self, node: ast.MatchAs) -> None:
        if node.name:
            self.names.add(node.name)
        if node.pattern is not None:
            self.visit(node.pattern)

    def visit_MatchStar(self, node: ast.MatchStar) -> None:
        if node.name:
            self.names.add(node.name)

    def visit_MatchMapping(self, node: ast.MatchMapping) -> None:
        if node.rest:
            self.names.add(node.rest)
        for pattern in node.patterns:
            self.visit(pattern)

    def _visit_comprehensions(self, generators: list[ast.comprehension]) -> None:
        for generator in generators:
            # The iteration target belongs to the comprehension's implicit scope.
            self.visit(generator.iter)
            for condition in generator.ifs:
                self.visit(condition)

    def visit_ListComp(self, node: ast.ListComp) -> None:
        self.visit(node.elt)
        self._visit_comprehensions(node.generators)

    def visit_SetComp(self, node: ast.SetComp) -> None:
        self.visit(node.elt)
        self._visit_comprehensions(node.generators)

    def visit_DictComp(self, node: ast.DictComp) -> None:
        self.visit(node.key)
        self.visit(node.value)
        self._visit_comprehensions(node.generators)

    def visit_GeneratorExp(self, node: ast.GeneratorExp) -> None:
        self.visit(node.elt)
        self._visit_comprehensions(node.generators)


def _validate_agent_wrapper_inputs(
    body: list[ast.stmt],
    preamble: list[ast.stmt],
    allowed_runtime_names: frozenset[str] = frozenset(),
) -> _ModuleAssignedNameCollector:
    validate_ast_recursion_shape_bounded(ast.Module(body=body, type_ignores=[]))
    collector = _ModuleAssignedNameCollector()
    for node in (*preamble, *body):
        collector.visit(node)
    if any(name.startswith("_pysolate_") for name in collector.names):
        raise SyntaxError("agent source binds a reserved runtime name")
    for node in walk_ast_bounded(ast.Module(body=body, type_ignores=[])):
        if isinstance(node, ast.Name) and node.id.startswith("_pysolate_"):
            if node.id not in allowed_runtime_names or (
                node.id in {_PLM_PREPARE_HELPER, _PLM_LINEARIZE_HELPER}
                and not getattr(node, "_pysolate_trusted_runtime", False)
            ):
                raise SyntaxError("agent source uses a reserved runtime name")
    return collector


def _compile_agent_wrapper(
    body: list[ast.stmt],
    preamble: list[ast.stmt],
    allowed_runtime_names: frozenset[str] = frozenset(),
    validated_collector: _ModuleAssignedNameCollector | None = None,
) -> tuple[types.CodeType, str]:
    collector = validated_collector or _validate_agent_wrapper_inputs(body, preamble, allowed_runtime_names)
    future_annotations = any(
        isinstance(node, ast.ImportFrom) and node.module == "__future__" and any(alias.name == "annotations" for alias in node.names)
        for node in preamble
    )
    transformer = _TopLevelReturnTransformer(future_annotations)
    transformed: list[ast.stmt] = []
    for node in body:
        if isinstance(node, ast.Global):
            continue
        updated = transformer.visit(node)
        if isinstance(updated, list):
            transformed.extend(updated)
        elif updated is not None:
            transformed.append(updated)
    fallthrough = ast.Return(
        value=ast.Tuple(
            elts=[
                ast.Name(id=_WRAPPER_FALLTHROUGH, ctx=ast.Load()),
                ast.Call(
                    func=ast.Attribute(value=ast.Name(id=_WRAPPER_GLOBALS, ctx=ast.Load()), attr="get", ctx=ast.Load()),
                    args=[ast.Constant(value="result"), ast.Name(id=_WRAPPER_MISSING, ctx=ast.Load())],
                    keywords=[],
                ),
            ],
            ctx=ast.Load(),
        )
    )
    wrapper_body: list[ast.stmt] = []
    if collector.names:
        wrapper_body.append(ast.Global(names=sorted(collector.names)))
    wrapper_body.extend(transformed)
    wrapper_body.append(fallthrough)
    function = ast.FunctionDef(
        name=_WRAPPER_MAIN,
        args=ast.arguments(posonlyargs=[], args=[], kwonlyargs=[], kw_defaults=[], defaults=[]),
        body=wrapper_body,
        decorator_list=[],
    )
    module = fix_missing_locations_bounded(ast.Module(body=[*preamble, function], type_ignores=[]))
    digest = ast_digest_bounded(module)
    code = compile(module, "<agent-run>", "exec", dont_inherit=True)
    wrapper = next((candidate for candidate in _code_objects(code) if candidate.co_name == _WRAPPER_MAIN), None)
    if wrapper is None or wrapper.co_flags & (inspect.CO_GENERATOR | inspect.CO_COROUTINE | inspect.CO_ASYNC_GENERATOR):
        raise SyntaxError("agent body cannot be a generator or coroutine")
    return code, digest


def _admit_agent_tree(
    tree: ast.Module,
    compatibility: dict[str, Any] | None,
    allowed_runtime_names: frozenset[str] = frozenset(),
) -> tuple[int, list[ast.stmt], list[ast.stmt], list[ast.stmt], _ModuleAssignedNameCollector | None]:
    import_nodes: list[ast.stmt] = []
    imported_roots: set[str] = set()
    preamble_open = True
    for index, node in enumerate(tree.body):
        if index == 0 and _module_docstring(node):
            continue
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            if not preamble_open:
                return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None
            import_nodes.append(node)
            if isinstance(node, ast.Import):
                for alias in node.names:
                    root = alias.name.partition(".")[0]
                    if root in _FORBIDDEN_IMPORT_ROOTS:
                        return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None
                    imported_roots.add(root)
            else:
                if node.level != 0 or node.module is None or any(alias.name == "*" for alias in node.names):
                    return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None
                root = node.module.partition(".")[0]
                if root in _FORBIDDEN_IMPORT_ROOTS:
                    return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None
                if root != "__future__":
                    imported_roots.add(root)
            continue
        preamble_open = False

    top_level_import_ids = {id(node) for node in import_nodes}
    for node in walk_ast_bounded(tree):
        if isinstance(node, (ast.Import, ast.ImportFrom)) and id(node) not in top_level_import_ids:
            return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None
        if isinstance(node, ast.Name) and node.id in _FORBIDDEN_DYNAMIC_NAMES:
            return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None
        if isinstance(node, ast.Attribute) and node.attr in {"__import__", "import_module"}:
            return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None
        if isinstance(node, ast.Constant) and isinstance(node.value, str) and node.value in _FORBIDDEN_DYNAMIC_NAMES:
            return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None

    if compatibility is not None:
        declarations = compatibility["imports"]
        if any("." in name for name in declarations) or set(declarations) != imported_roots:
            return _SOURCE_CONTRACT_UNSUPPORTED, [], [], [], None

    body = [node for node in tree.body if id(node) not in top_level_import_ids]
    future_nodes: list[ast.stmt] = [node for node in import_nodes if isinstance(node, ast.ImportFrom) and node.module == "__future__"]
    try:
        collector = _validate_agent_wrapper_inputs(body, future_nodes, allowed_runtime_names)
    except (SyntaxError, ValueError, TypeError, MemoryError, RecursionError):
        return _SOURCE_CONTRACT_INVALID, [], [], [], None
    return _SOURCE_CONTRACT_OK, body, future_nodes, import_nodes, collector


def _validate_agent_source(source: str, compatibility: dict[str, Any] | None) -> tuple[int, types.CodeType | None, list[ast.stmt]]:
    global _validated_effective_ast_sha256
    try:
        tree = ast.parse(source, filename="<agent-run>", mode="exec")
    except (SyntaxError, ValueError, TypeError, MemoryError, RecursionError):
        return _SOURCE_CONTRACT_INVALID, None, []
    status, body, future_nodes, import_nodes, collector = _admit_agent_tree(tree, compatibility)
    if status != _SOURCE_CONTRACT_OK or collector is None:
        return status, None, []
    try:
        code, effective_digest = _compile_agent_wrapper(body, future_nodes, validated_collector=collector)
        _validated_effective_ast_sha256 = effective_digest
    except (SyntaxError, ValueError, TypeError, MemoryError, RecursionError):
        return _SOURCE_CONTRACT_INVALID, None, []
    for candidate in _code_objects(code):
        if candidate is code:
            # The outer module contains only validated __future__ directives and
            # the trusted wrapper definition; CPython emits import opcodes for
            # those compiler directives.
            continue
        if any(instruction.opname in {"IMPORT_NAME", "IMPORT_FROM", "IMPORT_STAR"} for instruction in dis.get_instructions(candidate)):
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []
    return _SOURCE_CONTRACT_OK, code, import_nodes


def _preload_and_seal_imports(import_nodes: list[ast.stmt]) -> dict[str, Any] | None:
    try:
        import _agent_runtime_host  # type: ignore[import-not-found]
        seal = getattr(_agent_runtime_host, "seal_imports")
        namespace: dict[str, Any] = {"__builtins__": __builtins__}
        if import_nodes:
            preamble = ast.fix_missing_locations(ast.Module(body=import_nodes, type_ignores=[]))
            exec(compile(preamble, "<agent-import-preamble>", "exec", dont_inherit=True), namespace, namespace)
        seal(tuple(sorted(sys.modules)))
        namespace.pop("__builtins__", None)
        return namespace
    except BaseException:
        return None


def _admit_unrestricted_tree(
    tree: ast.Module,
    allowed_runtime_names: frozenset[str] = frozenset(),
) -> tuple[list[ast.stmt], list[ast.stmt], _ModuleAssignedNameCollector]:
    preamble: list[ast.stmt] = []
    body: list[ast.stmt] = []
    future_open = True
    for index, node in enumerate(tree.body):
        if index == 0 and _module_docstring(node):
            preamble.append(node)
            continue
        if future_open and isinstance(node, ast.ImportFrom) and node.module == "__future__":
            preamble.append(node)
            continue
        future_open = False
        if isinstance(node, ast.ImportFrom) and node.module == "__future__":
            raise SyntaxError("from __future__ imports must occur at the beginning of the file")
        body.append(node)
    return body, preamble, _validate_agent_wrapper_inputs(body, preamble, allowed_runtime_names)


def _validate_unrestricted_source(source: str) -> tuple[int, types.CodeType | None]:
    global _validated_effective_ast_sha256
    try:
        tree = ast.parse(source, filename="<agent-run>", mode="exec")
        body, preamble, collector = _admit_unrestricted_tree(tree)
        code, digest = _compile_agent_wrapper(body, preamble, validated_collector=collector)
        _validated_effective_ast_sha256 = digest
        return _SOURCE_CONTRACT_OK, code
    except (SyntaxError, ValueError, TypeError, MemoryError, RecursionError):
        return _SOURCE_CONTRACT_INVALID, None


def _validate_request_source(request_json: str) -> int:
    global _validated_request_json, _validated_code, _validated_source_tree, _validated_import_globals
    _validated_source_tree = None
    if not isinstance(request_json, str):
        return _SOURCE_CONTRACT_INVALID
    request, error = _decode_request(request_json)
    if error is not None or request is None:
        return _SOURCE_CONTRACT_INVALID
    if request.get("compatibility") is None:
        status, code = _validate_unrestricted_source(request["code"])
        if status != _SOURCE_CONTRACT_OK or code is None:
            return status
        _validated_request_json = request_json
        _validated_code = code
        _validated_import_globals = {}
        return _SOURCE_CONTRACT_OK
    status, code, import_nodes = _validate_agent_source(request["code"], request.get("compatibility"))
    if status != _SOURCE_CONTRACT_OK or code is None:
        return status
    preload = _preload_and_seal_imports(import_nodes)
    if preload is None:
        return _SOURCE_CONTRACT_UNSUPPORTED
    _validated_request_json = request_json
    _validated_code = code
    _validated_import_globals = preload
    return _SOURCE_CONTRACT_OK


def _validate_request_source_for_patch(request_json: str) -> int:
    """Parse and stage original source; selected-tree admission happens once after lowering."""
    global _validated_request_json, _validated_code, _validated_source_tree, _validated_import_globals
    if not isinstance(request_json, str):
        return _SOURCE_CONTRACT_INVALID
    request, error = _decode_request(request_json)
    if error is not None or request is None:
        return _SOURCE_CONTRACT_INVALID
    try:
        tree = ast.parse(request["code"], filename="<agent-run>", mode="exec")
    except (SyntaxError, ValueError, TypeError, MemoryError, RecursionError):
        return _SOURCE_CONTRACT_INVALID
    _validated_request_json = request_json
    _validated_code = None
    _validated_source_tree = tree
    _validated_import_globals = {}
    return _SOURCE_CONTRACT_OK


def _install_derived_tree(
    tree: ast.Module,
    request: dict[str, Any],
    allowed_runtime_names: frozenset[str] = frozenset(),
    preload_derived_imports: bool = False,
) -> None:
    global _validated_code, _validated_effective_ast_sha256, _validated_import_globals
    compatibility = request.get("compatibility")
    if compatibility is None:
        body, preamble, collector = _admit_unrestricted_tree(tree, allowed_runtime_names)
        import_nodes: list[ast.stmt] = []
    else:
        status, body, preamble, import_nodes, collector = _admit_agent_tree(
            tree, compatibility, allowed_runtime_names
        )
        if status != _SOURCE_CONTRACT_OK or collector is None:
            raise RuntimeError("derived source violates the source contract")
    code, digest = _compile_agent_wrapper(
        body, preamble, allowed_runtime_names, validated_collector=collector
    )
    if preload_derived_imports:
        preload = _preload_and_seal_imports(import_nodes)
        if preload is None:
            raise RuntimeError("derived source imports are unavailable")
        _validated_import_globals = preload
    _validated_code = code
    _validated_effective_ast_sha256 = digest


def _prepare_prepared_region_execution(selection_request_json: str) -> None:
    if _validated_request_json is None or _validated_code is None:
        raise RuntimeError("original agent source must be validated before derived selection")
    request, error = _decode_request(_validated_request_json)
    if error is not None or request is None:
        raise RuntimeError("validated original request is unavailable")
    from .prepared_region import validate_prepared_region_execution_request
    tree = validate_prepared_region_execution_request(request["code"], selection_request_json)
    _install_derived_tree(tree, request)


def _transform_source_pass(request_json: str) -> str:
    global _validated_source_tree
    try:
        transform_request = json.loads(request_json)
    except (TypeError, ValueError) as exc:
        raise ValueError("source pass request is invalid") from exc
    if not isinstance(transform_request, dict):
        raise ValueError("source pass request is invalid")
    plm_request = (
        transform_request.get("pass_name") == "plm_capability_calls"
        and transform_request.get("pass_version") == "pysolate.plm-capability-calls-pass.v1"
    )
    prepared_tree = _validated_source_tree
    admitted_request = None
    if prepared_tree is not None:
        if _validated_request_json is None:
            raise ValueError("source pass source was not admitted")
        admitted_request, error = _decode_request(_validated_request_json)
        if (
            error is not None
            or admitted_request is None
            or (not plm_request and transform_request.get("source") != admitted_request["code"])
        ):
            raise ValueError("source pass source does not match the admitted request")
        _validated_source_tree = None
    if plm_request:
        if prepared_tree is None or admitted_request is None:
            raise ValueError("PLM source pass source was not admitted")
        from .plm_source_pass import emit_patch

        return emit_patch(transform_request, admitted_request["code"], prepared_tree)
    from .source_pass import emit_source_pass_patch_request_json

    return emit_source_pass_patch_request_json(request_json, prepared_tree)


def _prepare_source_pass_execution(patch_json: str) -> None:
    if _validated_request_json is None:
        raise RuntimeError("original agent source must be admitted before source pass selection")
    request, error = _decode_request(_validated_request_json)
    if error is not None or request is None:
        raise RuntimeError("validated original request is unavailable")
    try:
        patch = json.loads(patch_json)
    except (TypeError, ValueError) as exc:
        raise RuntimeError("source pass patch is invalid") from exc
    if (
        isinstance(patch, dict)
        and patch.get("pass_name") == "plm_capability_calls"
        and patch.get("pass_version") == "pysolate.plm-capability-calls-pass.v1"
    ):
        from .plm_source_pass import select_tree

        tree = select_tree(request["code"], patch)
    else:
        from .source_pass import validate_source_pass_execution_request

        tree = validate_source_pass_execution_request(request["code"], patch_json)
    allowed_runtime_names = frozenset()
    if (
        patch.get("pass_name") == "plm_capability_calls"
        and patch.get("pass_version") == "pysolate.plm-capability-calls-pass.v1"
    ):
        allowed_runtime_names = frozenset({_PLM_PREPARE_HELPER, _PLM_LINEARIZE_HELPER})
    elif (
        patch.get("pass_name") == "data_local_numpy_sum"
        and patch.get("pass_version") == "pysolate.data-local-numpy-sum-pass.v2"
    ):
        allowed_runtime_names = frozenset({_VALUE_SLOT_HELPER})
    _install_derived_tree(tree, request, allowed_runtime_names, preload_derived_imports=True)


def _encode(response: dict[str, Any]) -> str:
    try:
        return json.dumps(response, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
    except (TypeError, ValueError) as exc:
        return json.dumps(
            _error("result_not_json", f"result is not JSON serializable: {exc}"),
            ensure_ascii=False,
            separators=(",", ":"),
            allow_nan=False,
        )


def _materialize_prepared_region(decision: str) -> bool | int:
    if (
        not isinstance(decision, str)
        or len(decision) != 71
        or not decision.startswith("sha256:")
        or any(character not in "0123456789abcdef" for character in decision[7:])
    ):
        raise RuntimeError("prepared region decision identity is invalid")
    import _agent_runtime_host  # type: ignore[import-not-found]
    raw = _agent_runtime_host.materialize_value(decision)
    if not isinstance(raw, str) or not raw or len(raw.encode("utf-8")) > _PREPARED_REGION_PAYLOAD_MAX:
        raise RuntimeError("prepared region payload is invalid")
    try:
        value = json.loads(raw)
    except (TypeError, ValueError) as exc:
        raise RuntimeError("prepared region payload is invalid") from exc
    if type(value) not in (bool, int) or (type(value) is int and not -(2**63) <= value < 2**63):
        raise RuntimeError("prepared region payload type is unsupported")
    if json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False) != raw:
        raise RuntimeError("prepared region payload is not canonical")
    return value


def _prepare_plm_capability(slot_id: str, call_id: str, capability: str, arguments: dict[str, Any]) -> None:
    if (
        not isinstance(slot_id, str)
        or not slot_id.startswith("slot-s")
        or len(slot_id) > 80
        or not isinstance(call_id, str)
        or not call_id.startswith("plm-s")
        or len(call_id) > 80
        or not isinstance(capability, str)
        or not capability
        or len(capability) > 128
        or not isinstance(arguments, dict)
    ):
        raise RuntimeError("PLM capability preparation is invalid")
    occurrence = _plm_occurrence_counts.get(slot_id, 0) + 1
    _plm_occurrence_counts[slot_id] = occurrence
    dynamic_slot = f"{slot_id}-{occurrence}"
    dynamic_call_id = f"{call_id}-{occurrence}"
    if len(dynamic_slot) > 96 or len(dynamic_call_id) > 96:
        raise RuntimeError("PLM capability occurrence is invalid")
    request = {"call_id": dynamic_call_id, "capability": capability, "arguments": arguments}
    try:
        dynamic_request = json.dumps(
            request, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False
        )
    except (TypeError, ValueError) as exc:
        raise RuntimeError("PLM capability arguments are not canonical JSON") from exc
    import _agent_runtime_host  # type: ignore[import-not-found]
    _agent_runtime_host.prepare_plm_call(dynamic_slot, dynamic_request)
    _plm_pending_slots.setdefault(slot_id, []).append((dynamic_slot, dynamic_call_id, capability))


def _linearize_plm_capability(slot_id: str, capability: str, arguments: dict[str, Any]) -> dict[str, Any]:
    if (
        not isinstance(slot_id, str)
        or not slot_id.startswith("slot-s")
        or len(slot_id) > 80
        or not isinstance(capability, str)
        or not capability
        or len(capability) > 128
        or not isinstance(arguments, dict)
    ):
        raise RuntimeError("PLM capability linearization is invalid")
    pending = _plm_pending_slots.get(slot_id)
    if not pending:
        raise RuntimeError("PLM capability occurrence is missing")
    dynamic_slot, dynamic_call_id, prepared_capability = pending.pop(0)
    if not pending:
        _plm_pending_slots.pop(slot_id, None)
    if prepared_capability != capability:
        raise RuntimeError("PLM capability identity changed before linearization")
    request = {"call_id": dynamic_call_id, "capability": capability, "arguments": arguments}
    try:
        dynamic_request = json.dumps(
            request, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False
        )
    except (TypeError, ValueError) as exc:
        raise RuntimeError("PLM capability arguments are not canonical JSON") from exc
    import _agent_runtime_host  # type: ignore[import-not-found]
    raw = _agent_runtime_host.linearize_plm_call(dynamic_slot, dynamic_request)
    try:
        response = json.loads(raw)
        if not isinstance(response, dict) or response.get("status") not in {"ok", "denied", "error"}:
            raise ValueError("invalid status")
        if response["status"] != "ok":
            error = response.get("error")
            if not isinstance(error, dict) or not isinstance(error.get("message"), str):
                raise ValueError("invalid error")
            raise RuntimeError(error["message"])
        result = response.get("result")
        if not isinstance(result, dict):
            raise ValueError("invalid result")
        return result
    except RuntimeError:
        raise
    except (TypeError, ValueError) as exc:
        raise RuntimeError("PLM Host response is invalid") from exc


def _materialize_value_slot(slot_id: str) -> Any:
    if not isinstance(slot_id, str) or not slot_id.startswith("slot-") or len(slot_id) > 128:
        raise RuntimeError("value-slot materialization is invalid")
    import _agent_runtime_host  # type: ignore[import-not-found]
    response = _agent_runtime_host.materialize_slot(slot_id)
    if not isinstance(response, bytes) or len(response) < 2:
        raise RuntimeError("value-slot response is invalid")
    tag, payload = response[0], response[1:]
    if tag == 2:
        return bytes(payload)
    if tag != 1:
        raise RuntimeError("value-slot strategy is invalid")
    try:
        raw = payload.decode("utf-8", "strict")
        value = json.loads(raw)
    except (UnicodeDecodeError, TypeError, ValueError) as exc:
        raise RuntimeError("value-slot scalar payload is invalid") from exc
    if isinstance(value, bool):
        canonical = "true" if value else "false"
    elif isinstance(value, int) and -(1 << 63) <= value <= (1 << 63) - 1:
        canonical = str(value)
    else:
        raise RuntimeError("value-slot scalar payload is invalid")
    if raw != canonical:
        raise RuntimeError("value-slot scalar payload is not canonical")
    return value


def _execute(request_json: str) -> str:
    if not isinstance(request_json, str):
        return _encode(_error("invalid_request", "request_json must be a string"))
    request, error = _decode_request(request_json)
    if error is not None:
        return _encode(error)
    assert request is not None

    global _validated_request_json, _validated_code, _validated_import_globals, _validated_effective_ast_sha256
    if _validated_request_json != request_json or _validated_code is None:
        contract_status = _validate_request_source(request_json)
        if contract_status == _SOURCE_CONTRACT_INVALID:
            return _encode(_error("source_invalid", "agent source is not valid for the exact Guest parser"))
        if contract_status != _SOURCE_CONTRACT_OK:
            return _encode(_error("source_contract_unsupported", "agent source must use one static absolute import preamble"))
    code = _validated_code
    if code is None:
        return _encode(_error("guest_internal", "validated code is unavailable"))

    _plm_occurrence_counts.clear()
    _plm_pending_slots.clear()

    started = time.monotonic()
    namespace = dict(_prepared_globals)
    namespace["inputs"] = request["inputs"]
    namespace.pop("result", None)
    namespace.update(_validated_import_globals)
    if request.get("compatibility") is not None:
        builtins_source = vars(__builtins__) if isinstance(__builtins__, types.ModuleType) else __builtins__
        restricted_builtins = dict(builtins_source)
        for forbidden in ("eval", "exec"):
            restricted_builtins.pop(forbidden, None)
        restricted_builtins["__import__"] = _declared_sealed_importer(request["compatibility"]["imports"])
        namespace["__builtins__"] = restricted_builtins
    explicit_return = object()
    fallthrough = object()
    missing = object()
    namespace[_WRAPPER_RETURN] = explicit_return
    namespace[_WRAPPER_FALLTHROUGH] = fallthrough
    namespace[_WRAPPER_MISSING] = missing
    namespace[_WRAPPER_GLOBALS] = namespace
    capture = _BoundedStdout()
    previous_stdout = sys.stdout
    previous_dunder_stdout = sys.__stdout__
    try:
        sys.stdout = capture
        sys.__stdout__ = capture
        exec(code, namespace, namespace)
        namespace[_PREPARED_REGION_HELPER] = _materialize_prepared_region
        namespace[_PLM_PREPARE_HELPER] = _prepare_plm_capability
        namespace[_PLM_LINEARIZE_HELPER] = _linearize_plm_capability
        namespace[_VALUE_SLOT_HELPER] = _materialize_value_slot
        main = namespace.get(_WRAPPER_MAIN)
        if not isinstance(main, types.FunctionType):
            raise RuntimeError("agent output wrapper is unavailable")
        tag, result = main()
        if capture.truncated:
            raise _OutputLimitExceeded("model logs exceed the output byte bound")
        logs = capture.logs()
        if tag is explicit_return:
            result_present = True
            result_source = "return"
        elif tag is fallthrough and result is not missing:
            result_present = True
            result_source = "legacy_result"
        elif tag is fallthrough:
            result = None
            result_present = False
            result_source = "missing"
        else:
            raise RuntimeError("agent output wrapper returned an invalid disposition")
        resolver = namespace.get("_resolve_capability_futures")
        if callable(resolver):
            resolved_result = resolver(result if result_present else None)
            if result_present:
                result = resolved_result
        effective_digest = _validated_effective_ast_sha256
        if effective_digest is None:
            raise RuntimeError("agent effective source identity is unavailable")
        response = {
            "status": "ok",
            "result": result,
            "logs": logs,
            "result_present": result_present,
            "result_source": result_source,
            "source_contract": {
                "schema_version": "pysolate.guest-source-contract.v1",
                "authority": "guest_reported_execution_fact",
                "model_source_sha256": "sha256:" + hashlib.sha256(request["code"].encode("utf-8")).hexdigest(),
                "effective_ast_sha256": effective_digest,
                "wrapper_contract_sha256": _WRAPPER_CONTRACT_SHA256,
            },
            "receipts": [],
            "metrics": {
                "guest_time_ms": max(0.0, (time.monotonic() - started) * 1000.0),
                "capability_calls": 0,
                "result_bytes": 0,
            },
            "error": None,
        }
        encoded = _encode(response)
        if json.loads(encoded)["status"] == "ok":
            response["metrics"]["result_bytes"] = len(
                json.dumps(result, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode("utf-8")
            )
            encoded = _encode(response)
        return encoded
    except BaseException as exc:
        try:
            logs = capture.logs()
        except BaseException:
            logs = []
        try:
            trace = traceback.format_exc()
        except BaseException:
            trace = f"{type(exc).__name__}: {exc}"
        code_name = "output_limit_exceeded" if isinstance(exc, _OutputLimitExceeded) else "python_exception"
        failure = _error(
            code_name,
            str(exc),
            error_type=type(exc).__name__,
            trace=trace,
        )
        failure["logs"] = logs
        return _encode(failure)
    finally:
        _plm_occurrence_counts.clear()
        sys.stdout = previous_stdout
        sys.__stdout__ = previous_dunder_stdout
