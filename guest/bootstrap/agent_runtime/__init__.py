"""Trusted bootstrap for the neutral Agent Python Runtime guest."""

from __future__ import annotations

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

_ALLOWED_REQUEST_FIELDS = {"run_id", "code", "inputs", "output_schema", "compatibility", "requirements"}
_TRACEBACK_MAX = 16_384
_prepared_globals: dict[str, Any] = {}
_runtime_config: dict[str, Any] = {}
_validated_request_json: str | None = None
_validated_code: types.CodeType | None = None
_validated_import_globals: dict[str, Any] = {}
_validated_effective_ast_sha256: str | None = None
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
_STDOUT_MAX_BYTES = 64 * 1024
_STDOUT_MAX_LINES = 256


class _OutputLimitExceeded(Exception):
    pass


class _BoundedStdout:
    def __init__(self) -> None:
        self._parts: list[str] = []
        self._bytes = 0

    def write(self, value: str) -> int:
        if not isinstance(value, str):
            raise TypeError("stdout writes must be strings")
        encoded = value.encode("utf-8")
        if self._bytes + len(encoded) > _STDOUT_MAX_BYTES:
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
        if len(lines) > _STDOUT_MAX_LINES:
            raise _OutputLimitExceeded("model logs exceed the line bound")
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
        self.speculation_max_calls = speculation_max_calls
        self.consumed_occurrences: set[str] = set()
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
            exec(compile(rewritten, "<agent-stream>", "exec"), self.namespace, self.namespace)
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
        return {
            "result": self.namespace.get("result"),
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
    global _runtime_config, _prepared_globals, _validated_request_json, _validated_code, _validated_import_globals, _validated_effective_ast_sha256
    _runtime_config = dict(value)
    _prepared_globals = {}
    _validated_request_json = None
    _validated_code = None
    _validated_import_globals = {}
    _validated_effective_ast_sha256 = None



def _prepare(source: str) -> None:
    if not isinstance(source, str):
        raise TypeError("source must be a string")
    namespace: dict[str, Any] = {"__builtins__": __builtins__}
    global _prepared_globals
    # Trusted preparation may intentionally build a streaming session while
    # definitions are still being installed into this private namespace.
    _prepared_globals = namespace
    exec(compile(source, "<trusted-prepare>", "exec"), namespace, namespace)



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
    def visit_FunctionDef(self, node: ast.FunctionDef):
        return node

    def visit_AsyncFunctionDef(self, node: ast.AsyncFunctionDef):
        return node

    def visit_ClassDef(self, node: ast.ClassDef):
        return node

    def visit_Lambda(self, node: ast.Lambda):
        return node

    def visit_Return(self, node: ast.Return):
        value = node.value if node.value is not None else ast.Constant(value=None)
        rewritten = ast.Return(value=ast.Tuple(elts=[ast.Name(id=_WRAPPER_RETURN, ctx=ast.Load()), value], ctx=ast.Load()))
        return ast.copy_location(rewritten, node)


class _ModuleAssignedNameCollector(ast.NodeVisitor):
    """Collect names that module execution would bind in its global namespace."""

    def __init__(self) -> None:
        self.names: set[str] = set()

    def visit_Name(self, node: ast.Name) -> None:
        if isinstance(node.ctx, (ast.Store, ast.Del)):
            self.names.add(node.id)

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

    def visit_ListComp(self, node: ast.ListComp) -> None:
        return

    def visit_SetComp(self, node: ast.SetComp) -> None:
        return

    def visit_DictComp(self, node: ast.DictComp) -> None:
        return

    def visit_GeneratorExp(self, node: ast.GeneratorExp) -> None:
        return


def _compile_agent_wrapper(body: list[ast.stmt], preamble: list[ast.stmt]) -> tuple[types.CodeType, str]:
    for node in ast.walk(ast.Module(body=body, type_ignores=[])):
        if isinstance(node, ast.Name) and node.id.startswith("_pysolate_"):
            raise SyntaxError("agent source uses a reserved runtime name")
    collector = _ModuleAssignedNameCollector()
    for node in body:
        collector.visit(node)
    transformer = _TopLevelReturnTransformer()
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
                    func=ast.Attribute(value=ast.Call(func=ast.Name(id="globals", ctx=ast.Load()), args=[], keywords=[]), attr="get", ctx=ast.Load()),
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
    module = ast.fix_missing_locations(ast.Module(body=[*preamble, function], type_ignores=[]))
    effective = ast.dump(module, annotate_fields=True, include_attributes=False)
    digest = "sha256:" + hashlib.sha256(effective.encode("utf-8")).hexdigest()
    code = compile(module, "<agent-run>", "exec")
    wrapper = next((candidate for candidate in _code_objects(code) if candidate.co_name == _WRAPPER_MAIN), None)
    if wrapper is None or wrapper.co_flags & (inspect.CO_GENERATOR | inspect.CO_COROUTINE | inspect.CO_ASYNC_GENERATOR):
        raise SyntaxError("agent body cannot be a generator or coroutine")
    return code, digest


def _validate_agent_source(source: str, compatibility: dict[str, Any] | None) -> tuple[int, types.CodeType | None, list[ast.stmt]]:
    global _validated_effective_ast_sha256
    try:
        tree = ast.parse(source, filename="<agent-run>", mode="exec")
    except (SyntaxError, ValueError, TypeError, MemoryError):
        return _SOURCE_CONTRACT_INVALID, None, []

    import_nodes: list[ast.stmt] = []
    imported_roots: set[str] = set()
    preamble_open = True
    for index, node in enumerate(tree.body):
        if index == 0 and _module_docstring(node):
            continue
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            if not preamble_open:
                return _SOURCE_CONTRACT_UNSUPPORTED, None, []
            import_nodes.append(node)
            if isinstance(node, ast.Import):
                for alias in node.names:
                    root = alias.name.partition(".")[0]
                    if root in _FORBIDDEN_IMPORT_ROOTS:
                        return _SOURCE_CONTRACT_UNSUPPORTED, None, []
                    imported_roots.add(root)
            else:
                if node.level != 0 or node.module is None or any(alias.name == "*" for alias in node.names):
                    return _SOURCE_CONTRACT_UNSUPPORTED, None, []
                root = node.module.partition(".")[0]
                if root in _FORBIDDEN_IMPORT_ROOTS:
                    return _SOURCE_CONTRACT_UNSUPPORTED, None, []
                if root != "__future__":
                    imported_roots.add(root)
            continue
        preamble_open = False

    top_level_import_ids = {id(node) for node in import_nodes}
    for node in ast.walk(tree):
        if isinstance(node, (ast.Import, ast.ImportFrom)) and id(node) not in top_level_import_ids:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []
        if isinstance(node, ast.Name) and node.id in _FORBIDDEN_DYNAMIC_NAMES:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []
        if isinstance(node, ast.Attribute) and node.attr in {"__import__", "import_module"}:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []
        if isinstance(node, ast.Constant) and isinstance(node.value, str) and node.value in _FORBIDDEN_DYNAMIC_NAMES:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []

    if compatibility is not None:
        declarations = compatibility["imports"]
        if any("." in name for name in declarations) or set(declarations) != imported_roots:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []

    body = [node for node in tree.body if id(node) not in top_level_import_ids]
    future_nodes: list[ast.stmt] = [node for node in import_nodes if isinstance(node, ast.ImportFrom) and node.module == "__future__"]
    try:
        code, effective_digest = _compile_agent_wrapper(body, future_nodes)
        _validated_effective_ast_sha256 = effective_digest
    except (SyntaxError, ValueError, TypeError, MemoryError):
        return _SOURCE_CONTRACT_INVALID, None, []
    for candidate in _code_objects(code):
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
            exec(compile(preamble, "<agent-import-preamble>", "exec"), namespace, namespace)
        seal(tuple(sorted(sys.modules)))
        namespace.pop("__builtins__", None)
        return namespace
    except BaseException:
        return None


def _validate_unrestricted_source(source: str) -> tuple[int, types.CodeType | None]:
    global _validated_effective_ast_sha256
    try:
        tree = ast.parse(source, filename="<agent-run>", mode="exec")
        preamble: list[ast.stmt] = []
        body: list[ast.stmt] = []
        for index, node in enumerate(tree.body):
            if (index == 0 and _module_docstring(node)) or (isinstance(node, ast.ImportFrom) and node.module == "__future__"):
                preamble.append(node)
            else:
                body.append(node)
        code, digest = _compile_agent_wrapper(body, preamble)
        _validated_effective_ast_sha256 = digest
        return _SOURCE_CONTRACT_OK, code
    except (SyntaxError, ValueError, TypeError, MemoryError):
        return _SOURCE_CONTRACT_INVALID, None


def _validate_request_source(request_json: str) -> int:
    global _validated_request_json, _validated_code, _validated_import_globals
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
    capture = _BoundedStdout()
    previous_stdout = sys.stdout
    try:
        sys.stdout = capture
        exec(code, namespace, namespace)
        main = namespace.get(_WRAPPER_MAIN)
        if not isinstance(main, types.FunctionType):
            raise RuntimeError("agent output wrapper is unavailable")
        tag, result = main()
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
        sys.stdout = previous_stdout
