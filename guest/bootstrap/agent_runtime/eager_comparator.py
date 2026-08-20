"""Research-only EAGER-style complete-statement comparator.

This module is imported only by the matched comparator harness. It is not used by
normal agent execution or semantic pre-dispatch.
"""

import ast
import builtins
import codeop
import io
import tokenize
from typing import Any, Dict, Optional, Sequence, Set

_MAX_SOURCE_BYTES = 1 << 20
_LOW_YIELD_NODES = (ast.AsyncFunctionDef, ast.ClassDef, ast.FunctionDef)
_DENIED_MODULES = {
    "multiprocessing",
    "os",
    "shutil",
    "signal",
    "socket",
    "subprocess",
    "threading",
    "time",
}
_DYNAMIC_NAMES = {"__import__", "compile", "eval", "exec"}
_CONTINUATION_NAMES = {"case", "elif", "else", "except", "finally"}


class EagerStyleGateSession:
    """One comparator trial with one persistent interpreter namespace."""

    def __init__(
        self,
        inputs: Any,
        prepared_globals: Dict[str, Any],
        allowed_import_roots: Sequence[str] = (),
    ):
        if not isinstance(prepared_globals, dict):
            raise ValueError("prepared_globals must be a dictionary")
        roots = tuple(allowed_import_roots)
        if any(not isinstance(root, str) or not root.isidentifier() or root.startswith("_") for root in roots):
            raise ValueError("allowed import roots must be public identifiers")
        if tuple(sorted(set(roots))) != roots:
            raise ValueError("allowed import roots must be sorted and unique")

        original_import = builtins.__import__

        def admitted_import(name, globals=None, locals=None, fromlist=(), level=0):
            if not isinstance(name, str) or not name or level != 0:
                raise ImportError("invalid comparator import")
            root = name.partition(".")[0]
            if root != "__future__" and root not in roots:
                raise ImportError("module root is outside the comparator import set")
            return original_import(name, globals, locals, fromlist, level)

        restricted_builtins = dict(vars(builtins))
        restricted_builtins["__import__"] = admitted_import
        self.namespace = {
            key: value
            for key, value in prepared_globals.items()
            if isinstance(key, str) and key and not key.startswith("_") and key not in {"inputs", "__builtins__"}
        }
        self.namespace.update({"__builtins__": restricted_builtins, "inputs": inputs})
        self.compiler = codeop.CommandCompiler()
        self.source = ""
        self.pending = ""
        self.deferred = ""
        self.sealed_source = ""
        self.sealed = False
        self.ended = False
        self.runtime_error = None
        self.prefix_python_executions = 0
        self.python_executions = 0
        self.events = []

    def chunk(self, text: str) -> Dict[str, Any]:
        if self.ended or not isinstance(text, str) or not text:
            raise ValueError("comparator chunk must be non-empty before end")
        if len(self.source.encode("utf-8")) + len(text.encode("utf-8")) > _MAX_SOURCE_BYTES:
            self.ended = True
            raise ValueError("comparator source exceeds one MiB")

        if self.runtime_error is not None:
            self.source += text
            return self._event("runtime_error_suffix")

        if self.sealed:
            self.source += text
            self.sealed_source += text
            return self._event("sealed_suffix", sealed=True)

        status = None
        denied_name = None
        if self.pending and not _starts_continuation(text):
            code = self._compile_pending()
            if code is not None:
                batch = self.deferred + self.pending
                tree = ast.parse(batch, filename="<eager-style-gate>", mode="exec")
                if self.deferred == "" and tree.body and all(isinstance(node, _LOW_YIELD_NODES) for node in tree.body):
                    self.deferred = batch
                    self.pending = ""
                    status = "deferred_low_yield"
                else:
                    denied_name = _first_denied_name(tree)
                    if denied_name is not None:
                        self.sealed = True
                        self.sealed_source = batch + text
                        self.deferred = ""
                        self.pending = ""
                        self.source += text
                        return self._event("sealed", sealed=True, denied_name=denied_name)
                    batch_code = self.compiler(batch, "<eager-style-gate>", "exec")
                    if batch_code is None:
                        raise SyntaxError("admitted comparator batch became incomplete")
                    try:
                        self._execute(batch_code, prefix=True)
                    except BaseException as exc:
                        self.runtime_error = exc
                        self.deferred = ""
                        self.pending = ""
                        self.source += text
                        return self._event("runtime_error_frozen")
                    self.deferred = ""
                    self.pending = ""
                    status = "executed"

        self.source += text
        self.pending += text
        if status is not None:
            return self._event(status, denied_name=denied_name)
        return self._pending_event()

    def end(self) -> Dict[str, Any]:
        if self.ended:
            raise ValueError("comparator session already ended")
        self.ended = True

        # Final source validity is decided before any unexecuted suffix runs.
        ast.parse(self.source, filename="<eager-style-gate>", mode="exec")
        if self.runtime_error is not None:
            raise self.runtime_error
        remaining = self.sealed_source if self.sealed else self.deferred + self.pending
        if remaining:
            remaining_code = self.compiler(remaining, "<eager-style-gate>", "exec")
            if remaining_code is None:
                raise SyntaxError("final comparator batch is incomplete")
            self._execute(remaining_code, prefix=False)
        result_present = "result" in self.namespace
        return {
            "result": self.namespace.get("result"),
            "result_present": result_present,
            "prefix_python_executions": self.prefix_python_executions,
            "python_executions": self.python_executions,
            "sealed": self.sealed,
            "events": [dict(event) for event in self.events],
        }

    def finish(self) -> Dict[str, Any]:
        """Return a bounded terminal projection without exception messages or source."""
        try:
            final = self.end()
        except SyntaxError:
            return self._terminal_projection("syntax_error", "SyntaxError")
        except BaseException as exc:
            return self._terminal_projection("runtime_error", type(exc).__name__)
        final.update({"outcome": "success", "error_class": ""})
        return final

    def _terminal_projection(self, outcome: str, error_class: str) -> Dict[str, Any]:
        return {
            "outcome": outcome,
            "error_class": error_class,
            "prefix_python_executions": self.prefix_python_executions,
            "python_executions": self.python_executions,
            "sealed": self.sealed,
            "events": [dict(event) for event in self.events],
        }

    def cancel(self) -> Dict[str, Any]:
        if self.ended:
            raise ValueError("comparator session already ended")
        self.ended = True
        self.pending = ""
        self.deferred = ""
        self.sealed_source = ""
        return self._event("cancelled")

    def _compile_pending(self) -> Optional[Any]:
        try:
            return self.compiler(self.pending, "<eager-style-gate>", "exec")
        except (SyntaxError, ValueError, TypeError, MemoryError):
            return None

    def _pending_event(self) -> Dict[str, Any]:
        try:
            code = self.compiler(self.pending, "<eager-style-gate>", "exec")
        except (SyntaxError, ValueError, TypeError, MemoryError):
            return self._event("invalid_suffix_pending")
        return self._event("lookahead_pending" if code is not None else "incomplete")

    def _execute(self, code: Any, prefix: bool) -> None:
        self.python_executions += 1
        if prefix:
            self.prefix_python_executions += 1
        exec(code, self.namespace, self.namespace)

    def _event(self, status: str, sealed: bool = False, denied_name: Optional[str] = None) -> Dict[str, Any]:
        event = {
            "status": status,
            "sealed": sealed or self.sealed,
            "prefix_python_executions": self.prefix_python_executions,
        }
        if denied_name is not None:
            event["denied_name"] = denied_name
        self.events.append(event)
        return dict(event)


def _starts_continuation(text: str) -> bool:
    try:
        tokens = tokenize.generate_tokens(io.StringIO(text).readline)
        for token in tokens:
            if token.type in (tokenize.ENCODING, tokenize.NL, tokenize.NEWLINE, tokenize.INDENT, tokenize.DEDENT):
                continue
            return token.type == tokenize.NAME and token.string in _CONTINUATION_NAMES
    except (IndentationError, SyntaxError, tokenize.TokenError):
        return False
    return False


def _attribute_root(node: ast.Attribute) -> Optional[str]:
    value = node.value
    while isinstance(value, ast.Attribute):
        value = value.value
    return value.id if isinstance(value, ast.Name) else None


def _first_denied_name(tree: ast.AST) -> Optional[str]:
    found: Set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Name) and node.id in _DENIED_MODULES | _DYNAMIC_NAMES:
            found.add(node.id)
        elif isinstance(node, ast.Attribute):
            root = _attribute_root(node)
            if root in _DENIED_MODULES:
                found.add(root)
        elif isinstance(node, ast.Import):
            for alias in node.names:
                root = alias.name.partition(".")[0]
                if root in _DENIED_MODULES:
                    found.add(root)
        elif isinstance(node, ast.ImportFrom):
            root = (node.module or "").partition(".")[0]
            if root in _DENIED_MODULES:
                found.add(root)
    return sorted(found)[0] if found else None
