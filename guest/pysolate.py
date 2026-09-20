"""Tiny dynamic facade over Host-owned Pysolate tools.

The Host supplies canonical identities and Python paths for each execution.
This module owns no credentials, transports, authority, validation, or retry
policy; every generated function dispatches through the same `_pysolate.call`
ABI.
"""
import json
from _pysolate import call as _host_call


_dynamic_roots = {}


def _decode(response):
    response = json.loads(response)
    if "error" in response:
        raise RuntimeError(response["error"])
    return response["value"]


def _default_invoke(name, args):
    request = json.dumps({"tool": name, "args": args})
    return _decode(_host_call(request))


_invoke = _default_invoke


class _Tool:
    __slots__ = ("_canonical", "__name__")

    def __init__(self, canonical, python_name):
        self._canonical = canonical
        self.__name__ = python_name

    def __call__(self, **args):
        return _invoke(self._canonical, args)

    def __repr__(self):
        return f"<pysolate tool {self._canonical!r}>"


class _Namespace:
    __slots__ = ("_path", "_children")

    def __init__(self, path):
        self._path = path
        self._children = {}

    def __getattr__(self, name):
        try:
            return self._children[name]
        except KeyError:
            raise AttributeError(name) from None

    def __dir__(self):
        return sorted(self._children)

    def __repr__(self):
        return f"<pysolate namespace {self._path!r}>"


def _insert(exports, canonical, python_path):
    parts = python_path.split(".")
    current = exports
    namespace_path = []
    for part in parts[:-1]:
        namespace_path.append(part)
        existing = current.get(part)
        if existing is None:
            existing = _Namespace(".".join(namespace_path))
            current[part] = existing
        elif not isinstance(existing, _Namespace):
            raise ValueError(f"namespace collision at {'.'.join(namespace_path)}")
        current = existing._children
    leaf = parts[-1]
    if leaf in current:
        raise ValueError(f"namespace collision at {python_path}")
    current[leaf] = _Tool(canonical, leaf)


def configure(manifest, invoke=None):
    """Build one execution's named functions and return its global exports."""
    global _invoke, _dynamic_roots
    _invoke = invoke or _default_invoke
    exports = {}
    for spec in manifest:
        canonical = spec["name"]
        python_path = spec.get("python_path", canonical)
        _insert(exports, canonical, python_path)

    module_globals = globals()
    for name, previous in _dynamic_roots.items():
        if module_globals.get(name) is previous:
            del module_globals[name]
    _dynamic_roots = exports
    module_globals.update(exports)
    return dict(exports)
