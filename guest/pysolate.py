"""Tiny dynamic facade over Host-owned Pysolate tools.

The Host supplies the catalog for each execution. This module owns no
credentials, transports, authority, schema validation, or retry policy.
"""
import copy
import json
from _pysolate import call as _host_call


_specs = {}
_invoke = None


def _decode(response):
    response = json.loads(response)
    if "error" in response:
        raise RuntimeError(response["error"])
    return response["value"]


def _default_invoke(name, args):
    request = json.dumps({"tool": name, "args": args})
    return _decode(_host_call(request))


def configure(manifest, invoke=None):
    """Replace the run-local catalog with Host-supplied tool metadata."""
    global _specs, _invoke
    catalog = {}
    for source in manifest:
        name = source["name"]
        if not isinstance(name, str) or not name or name in catalog:
            raise ValueError("invalid or duplicate Host tool name")
        catalog[name] = copy.deepcopy(source)
    _specs = catalog
    _invoke = invoke or _default_invoke


class Tool:
    __slots__ = ("_name",)

    def __init__(self, name):
        self._name = name

    @property
    def name(self):
        return self._name

    @property
    def description(self):
        return _specs[self._name].get("description", "")

    @property
    def input_schema(self):
        return copy.deepcopy(_specs[self._name].get("input_schema"))

    @property
    def annotations(self):
        return copy.deepcopy(_specs[self._name].get("annotations", {}))

    def __call__(self, **args):
        return _invoke(self._name, args)

    def __repr__(self):
        return f"<pysolate.Tool {self._name!r}>"


class ToolRegistry:
    def __getitem__(self, name):
        if name not in _specs:
            raise KeyError(f"unknown tool: {name}")
        return Tool(name)

    def __getattr__(self, name):
        if name.startswith("_"):
            raise AttributeError(name)
        try:
            return self[name]
        except KeyError as error:
            raise AttributeError(name) from error

    def call(self, name, **args):
        return self[name](**args)

    def names(self):
        return tuple(_specs)

    def describe(self, name):
        if name not in _specs:
            raise KeyError(f"unknown tool: {name}")
        return copy.deepcopy(_specs[name])

    def __iter__(self):
        return iter(_specs)

    def __len__(self):
        return len(_specs)


tools = ToolRegistry()
