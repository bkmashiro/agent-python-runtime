"""Conservative target-Guest AST effect analysis for Experimental semantic plans."""

import ast
import hashlib
import json
import re


ANALYSIS_SCHEMA_VERSION = "pysolate.semantic-analysis.v2"
ANALYZER_IDENTITY_SHA256 = "sha256:" + hashlib.sha256(b"pysolate.semantic-analyzer.v4").hexdigest()
MAX_SOURCE_BYTES = 1 << 20
MAX_CAPABILITIES = 128
MAX_FUNCTIONS = 256
MAX_BARRIERS = 256
MAX_CALL_SITES = 256
_DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
_BINDING_KEYS = (
    "artifact_sha256",
    "execution_profile_sha256",
    "import_closure_sha256",
    "capability_plan_sha256",
)
_EFFECT_KEYS = ("may_publish", "may_observe_live", "may_suspend", "may_be_unknown")
_KNOWN_BUILTINS = {
    "abs", "all", "any", "bool", "bytes", "dict", "enumerate", "filter",
    "float", "int", "len", "list", "map", "max", "min", "range", "reversed",
    "round", "set", "sorted", "str", "sum", "tuple", "zip",
}
_HIGHER_ORDER_BUILTINS = {"filter", "map"}
_KEY_CALLBACK_BUILTINS = {"max", "min", "sorted"}
_LOCAL_MUTATIONS = {"add", "append", "clear", "discard", "extend", "insert", "pop", "remove", "reverse", "sort", "update"}
_CLOCK_RANDOM_ROOTS = {"datetime", "random", "secrets", "time"}
_SAFE_IMPORT_ROOTS = _CLOCK_RANDOM_ROOTS | {"math", "os", "sys"}


def _digest(value):
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _effects():
    return {key: False for key in _EFFECT_KEYS}


def _merge(target, source):
    changed = False
    for key in _EFFECT_KEYS:
        if source[key] and not target[key]:
            target[key] = True
            changed = True
    return changed


def _span(node):
    start_line = max(1, int(getattr(node, "lineno", 1)))
    start_column = max(0, int(getattr(node, "col_offset", 0)))
    end_line = max(start_line, int(getattr(node, "end_lineno", start_line)))
    end_column = max(0, int(getattr(node, "end_col_offset", start_column)))
    if end_line == start_line and end_column < start_column:
        end_column = start_column
    return {
        "start_line": start_line,
        "start_column": start_column,
        "end_line": end_line,
        "end_column": end_column,
    }


def _module_span(tree):
    if not tree.body:
        return {"start_line": 1, "start_column": 0, "end_line": 1, "end_column": 0}
    first = _span(tree.body[0])
    last = _span(tree.body[-1])
    return {
        "start_line": first["start_line"],
        "start_column": first["start_column"],
        "end_line": last["end_line"],
        "end_column": last["end_column"],
    }


def _barrier_key(row):
    return "%s\x00%s\x00%s" % (
        row["function_id"], row["code"],
        json.dumps(row["span"], separators=(",", ":")),
    )


def _function_identity(source_sha256, node):
    span = _span(node)
    descriptor = "pysolate.semantic-function.v0\x00%s\x00%s\x00%d:%d:%d:%d" % (
        source_sha256, node.name, span["start_line"], span["start_column"],
        span["end_line"], span["end_column"],
    )
    return _digest(descriptor.encode("utf-8"))


def _scc_identity(members):
    return _digest(("pysolate.semantic-scc.v0\x00" + "\x00".join(sorted(members))).encode("ascii"))


class _ScopeAnalyzer(ast.NodeVisitor):
    def __init__(self, function_id, function_ids, capabilities, parameters=()):
        self.function_id = function_id
        self.function_ids = function_ids
        self.capabilities = capabilities
        self.effects = _effects()
        self.calls = set()
        self.direct_capabilities = set()
        self.barriers = []
        self.local_containers = set()
        self.shadowed = set(parameters)

    def barrier(self, code, node):
        if len(self.barriers) >= MAX_BARRIERS:
            raise ValueError("semantic barrier bound exceeded")
        self.effects["may_be_unknown"] = True
        self.barriers.append({"code": code, "function_id": self.function_id, "span": _span(node)})

    def _assigned_names(self, target):
        if isinstance(target, ast.Name):
            return {target.id}
        if isinstance(target, (ast.Tuple, ast.List)):
            names = set()
            for item in target.elts:
                names.update(self._assigned_names(item))
            return names
        return set()

    def _track_assignment(self, target, value):
        if isinstance(target, (ast.Attribute, ast.Subscript)):
            self.barrier("unsupported_control_flow", target)
            return
        names = self._assigned_names(target)
        tool_roots = self.capabilities["tool_roots"]
        for name in names:
            if name in tool_roots:
                self.barrier("tool_rebinding", target)
            self.shadowed.add(name)
            if isinstance(value, (ast.List, ast.Dict, ast.Set)):
                self.local_containers.add(name)
            elif name in self.local_containers:
                self.local_containers.remove(name)

    def visit_Assign(self, node):
        for target in node.targets:
            self._track_assignment(target, node.value)
            self.visit(target)
        self.visit(node.value)

    def visit_AnnAssign(self, node):
        if node.value is not None:
            self._track_assignment(node.target, node.value)
            self.visit(node.value)
        self.visit(node.target)

    def visit_AugAssign(self, node):
        self._track_assignment(node.target, node.value)
        self.generic_visit(node)

    def visit_NamedExpr(self, node):
        self._track_assignment(node.target, node.value)
        self.generic_visit(node)

    def visit_Delete(self, node):
        if any(isinstance(target, (ast.Attribute, ast.Subscript)) for target in node.targets):
            self.barrier("unsupported_control_flow", node)
        self.generic_visit(node)

    def visit_Import(self, node):
        for alias in node.names:
            root = alias.name.split(".", 1)[0]
            bound = alias.asname or root
            if root not in _SAFE_IMPORT_ROOTS:
                self.barrier("dynamic_import", node)
            if bound in self.capabilities["tool_roots"]:
                self.barrier("tool_rebinding", node)
            if bound in self.capabilities["tool_roots"] or bound in self.function_ids or bound in _KNOWN_BUILTINS or bound == "open":
                self.shadowed.add(bound)

    def visit_ImportFrom(self, node):
        root = (node.module or "").split(".", 1)[0]
        if root not in _SAFE_IMPORT_ROOTS or any(alias.name == "*" for alias in node.names):
            self.barrier("dynamic_import", node)
        for alias in node.names:
            bound = alias.asname or alias.name
            if bound in self.capabilities["tool_roots"]:
                self.barrier("tool_rebinding", node)
            if bound in self.capabilities["tool_roots"] or bound in self.function_ids or bound in _KNOWN_BUILTINS or bound == "open":
                self.shadowed.add(bound)

    def visit_Attribute(self, node):
        if isinstance(node.value, ast.Name) and (
            (node.value.id == "os" and node.attr == "environ") or
            (node.value.id == "sys" and node.attr in {"argv", "path"})
        ):
            self.effects["may_observe_live"] = True
        self.generic_visit(node)

    def _track_definition_name(self, node):
        if node.name in self.capabilities["tool_roots"]:
            self.barrier("tool_rebinding", node)
        self.shadowed.add(node.name)

    def visit_ClassDef(self, node):
        self._track_definition_name(node)
        self.barrier("unsupported_control_flow", node)
        self.generic_visit(node)

    def _visit_opaque_control(self, node):
        self.barrier("unsupported_control_flow", node)
        self.generic_visit(node)

    visit_For = _visit_opaque_control
    visit_AsyncFor = _visit_opaque_control
    visit_While = _visit_opaque_control
    visit_Try = _visit_opaque_control
    visit_TryStar = _visit_opaque_control
    visit_With = _visit_opaque_control
    visit_AsyncWith = _visit_opaque_control
    visit_Match = _visit_opaque_control
    visit_ListComp = _visit_opaque_control
    visit_SetComp = _visit_opaque_control
    visit_DictComp = _visit_opaque_control
    visit_GeneratorExp = _visit_opaque_control
    visit_Yield = _visit_opaque_control
    visit_YieldFrom = _visit_opaque_control

    def visit_FunctionDef(self, node):
        self._track_definition_name(node)
        self.barrier("unsupported_control_flow", node)

    def visit_AsyncFunctionDef(self, node):
        self._track_definition_name(node)
        self.barrier("unsupported_control_flow", node)

    def visit_Lambda(self, node):
        self.barrier("dynamic_call", node)

    def visit_Await(self, node):
        self.barrier("unsupported_control_flow", node)
        self.generic_visit(node)

    def visit_Yield(self, node):
        self.barrier("unsupported_control_flow", node)
        self.generic_visit(node)

    def visit_YieldFrom(self, node):
        self.barrier("unsupported_control_flow", node)
        self.generic_visit(node)

    def visit_Global(self, node):
        self.barrier("dynamic_call", node)

    def visit_Nonlocal(self, node):
        self.barrier("dynamic_call", node)

    def _call_name(self, node):
        if isinstance(node, ast.Name):
            return node.id
        if isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name):
            return node.value.id + "." + node.attr
        return ""

    def _visit_arguments(self, node):
        for argument in node.args:
            self.visit(argument)
        for keyword in node.keywords:
            self.visit(keyword.value)

    def _capability(self, node):
        if isinstance(node, ast.Name):
            if node.id in self.shadowed:
                return None
            return self.capabilities["calls"].get(node.id)
        if isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name):
            if node.value.id in self.shadowed:
                return None
            return self.capabilities["calls"].get(node.value.id + "." + node.attr)
        return None

    def visit_Call(self, node):
        capability = self._capability(node.func)
        if capability is not None:
            self.direct_capabilities.add(capability["name"])
            effect_class = capability["effect_class"]
            if effect_class in ("workspace_write", "external_write"):
                self.effects["may_publish"] = True
            if effect_class in ("workspace_read", "external_read"):
                self.effects["may_observe_live"] = True
            self.effects["may_suspend"] = True
            self._visit_arguments(node)
            return
        name = self._call_name(node.func)
        if name in self.function_ids and name not in self.shadowed:
            self.calls.add(self.function_ids[name])
            self._visit_arguments(node)
            return
        if name in ("eval", "exec", "compile"):
            self.barrier("eval_exec", node)
        elif name == "__import__":
            self.barrier("dynamic_import", node)
        elif name in _HIGHER_ORDER_BUILTINS and name not in self.shadowed:
            self.barrier("dynamic_call", node)
        elif name in _KEY_CALLBACK_BUILTINS and name not in self.shadowed and any(keyword.arg == "key" for keyword in node.keywords):
            self.barrier("dynamic_call", node)
        elif name in _KNOWN_BUILTINS and name not in self.shadowed:
            pass
        elif name == "open" and name not in self.shadowed:
            self.effects["may_suspend"] = True
            mode = "r"
            if len(node.args) > 1 and isinstance(node.args[1], ast.Constant) and isinstance(node.args[1].value, str):
                mode = node.args[1].value
            if any(character in mode for character in "wax+"):
                self.effects["may_publish"] = True
            else:
                self.effects["may_observe_live"] = True
        elif isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Name) and node.func.value.id in self.local_containers and node.func.attr in _LOCAL_MUTATIONS:
            pass
        elif isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Name) and node.func.value.id not in self.shadowed and node.func.value.id in _CLOCK_RANDOM_ROOTS:
            self.effects["may_observe_live"] = True
        else:
            self.barrier("dynamic_call", node)
        self._visit_arguments(node)


def _capability_index(capabilities):
    if not isinstance(capabilities, list) or len(capabilities) > MAX_CAPABILITIES:
        raise ValueError("semantic capability bound exceeded")
    calls = {}
    roots = set()
    for row in capabilities:
        required = {"name", "effect_class", "playback", "module", "method", "global_alias", "arguments"}
        if not isinstance(row, dict) or set(row) != required:
            raise ValueError("invalid semantic capability projection")
        if (not row["name"] or not row["module"] or not row["method"] or
                not isinstance(row["arguments"], list) or
                len(row["arguments"]) > 64 or
                any(not isinstance(argument, str) or not argument for argument in row["arguments"]) or
                len(set(row["arguments"])) != len(row["arguments"])):
            raise ValueError("invalid semantic capability projection")
        keys = [row["module"] + "." + row["method"]]
        roots.add(row["module"])
        if row["global_alias"]:
            keys.append(row["global_alias"])
            roots.add(row["global_alias"])
        for key in keys:
            if key in calls:
                raise ValueError("duplicate semantic capability projection")
            calls[key] = dict(row)
    return {"calls": calls, "tool_roots": roots}


def _literal_json(node):
    if not isinstance(node, ast.Constant) or not isinstance(node.value, (type(None), bool, int, float, str)):
        raise ValueError("non-canonical semantic argument")
    value = node.value
    if isinstance(value, float) and (value != value or value in (float("inf"), float("-inf"))):
        raise ValueError("non-finite semantic argument")
    encoded = json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
    if len(encoded.encode("utf-8")) > 4096:
        raise ValueError("semantic argument bound exceeded")
    return value


def _canonical_call_arguments(node, capability):
    names = capability["arguments"]
    if any(isinstance(argument, ast.Starred) for argument in node.args) or any(keyword.arg is None for keyword in node.keywords):
        return None
    if len(node.args) > len(names):
        return None
    values = {}
    try:
        for index, argument in enumerate(node.args):
            values[names[index]] = _literal_json(argument)
        for keyword in node.keywords:
            if keyword.arg not in names or keyword.arg in values:
                return None
            values[keyword.arg] = _literal_json(keyword.value)
    except ValueError:
        return None
    if set(values) != set(names):
        return None
    return {name: values[name] for name in names}


def _direct_statement_call(statement):
    value = None
    if isinstance(statement, ast.Expr):
        value = statement.value
    elif isinstance(statement, ast.Assign) and all(isinstance(target, ast.Name) for target in statement.targets):
        value = statement.value
    elif isinstance(statement, ast.AnnAssign) and isinstance(statement.target, ast.Name):
        value = statement.value
    return value if isinstance(value, ast.Call) else None


def _module_call_sites(tree, source_sha256, capability_index):
    sites = []
    necessarily_reached = True
    state = _ScopeAnalyzer("", {}, capability_index)
    control_region = _digest(("pysolate.semantic-control-region.v0\x00" + source_sha256 + "\x00module-entry").encode("ascii"))
    for position, statement in enumerate(tree.body):
        if (position == 0 and isinstance(statement, ast.Expr) and
                isinstance(statement.value, ast.Constant) and isinstance(statement.value.value, str)):
            continue
        call = _direct_statement_call(statement)
        if call is not None:
            capability = state._capability(call.func)
            arguments = _canonical_call_arguments(call, capability) if capability is not None else None
            if capability is not None and arguments is not None:
                span = _span(call)
                descriptor = "pysolate.semantic-call-site.v0\x00%s\x00%s\x00%d:%d:%d:%d" % (
                    source_sha256, capability["name"], span["start_line"], span["start_column"],
                    span["end_line"], span["end_column"],
                )
                sites.append({
                    "id": _digest(descriptor.encode("utf-8")),
                    "span": span,
                    "capability": capability["name"],
                    "control_region_id": control_region,
                    "necessarily_reached": necessarily_reached,
                    "arguments_canonical": True,
                    "canonical_arguments": arguments,
                    "dynamic_occurrence": 1,
                })
        state.visit(statement)
        necessarily_reached = False
    if len(sites) > MAX_CALL_SITES:
        raise ValueError("semantic call-site bound exceeded")
    return sorted(sites, key=lambda row: row["id"])


def _parameters(node):
    arguments = list(node.args.posonlyargs) + list(node.args.args) + list(node.args.kwonlyargs)
    names = [argument.arg for argument in arguments]
    if node.args.vararg is not None:
        names.append(node.args.vararg.arg)
    if node.args.kwarg is not None:
        names.append(node.args.kwarg.arg)
    return names


def _strong_components(edges):
    index = 0
    stack = []
    indices = {}
    lowlinks = {}
    on_stack = set()
    components = []

    def visit(node):
        nonlocal index
        indices[node] = index
        lowlinks[node] = index
        index += 1
        stack.append(node)
        on_stack.add(node)
        for target in edges.get(node, ()):
            if target not in indices:
                visit(target)
                lowlinks[node] = min(lowlinks[node], lowlinks[target])
            elif target in on_stack:
                lowlinks[node] = min(lowlinks[node], indices[target])
        if lowlinks[node] == indices[node]:
            component = []
            while True:
                member = stack.pop()
                on_stack.remove(member)
                component.append(member)
                if member == node:
                    break
            components.append(sorted(component))

    for node in sorted(edges):
        if node not in indices:
            visit(node)
    return components


def canonical_analysis_json(report):
    encoded = json.dumps(report, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return encoded.replace("\u2028", "\\u2028").replace("\u2029", "\\u2029")


def analyze_source(source, bindings, capabilities):
    if not isinstance(source, str) or not source or len(source.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("invalid semantic source")
    if not isinstance(bindings, dict) or set(bindings) != set(_BINDING_KEYS):
        raise ValueError("invalid semantic bindings")
    if any(not isinstance(bindings[key], str) or _DIGEST.fullmatch(bindings[key]) is None for key in _BINDING_KEYS):
        raise ValueError("invalid semantic binding identity")
    tree = ast.parse(source, filename="<agent-semantic-analysis>", mode="exec")
    source_sha256 = _digest(source.encode("utf-8"))
    ast_dump = ast.dump(tree, annotate_fields=True, include_attributes=False)
    ast_sha256 = _digest(ast_dump.encode("utf-8"))
    capability_index = _capability_index(capabilities)
    call_sites = _module_call_sites(tree, source_sha256, capability_index)
    function_nodes = {}
    function_ids = {}
    module_barriers = []
    for node in tree.body:
        if isinstance(node, ast.FunctionDef):
            if node.name in function_nodes:
                module_barriers.append({"code": "dynamic_call", "function_id": "", "span": _span(node)})
            function_nodes[node.name] = node
            function_ids[node.name] = _function_identity(source_sha256, node)
        elif isinstance(node, ast.AsyncFunctionDef):
            module_barriers.append({"code": "unsupported_control_flow", "function_id": "", "span": _span(node)})

    if len(function_nodes) > MAX_FUNCTIONS:
        raise ValueError("semantic function bound exceeded")

    rows = {}
    all_barriers = list(module_barriers)
    for name, node in function_nodes.items():
        analyzer = _ScopeAnalyzer(function_ids[name], function_ids, capability_index, _parameters(node))
        if node.decorator_list:
            analyzer.barrier("unsupported_decorator", node)
        for statement in node.body:
            analyzer.visit(statement)
        rows[function_ids[name]] = {
            "id": function_ids[name],
            "name": name,
            "span": _span(node),
            "scc_id": "",
            "direct_capabilities": sorted(analyzer.direct_capabilities),
            "calls": sorted(analyzer.calls),
            "effects": analyzer.effects,
        }
        all_barriers.extend(analyzer.barriers)

    module_analyzer = _ScopeAnalyzer("", function_ids, capability_index)
    for node in tree.body:
        if isinstance(node, ast.FunctionDef):
            for expression in list(node.decorator_list) + list(node.args.defaults) + [value for value in node.args.kw_defaults if value is not None]:
                module_analyzer.visit(expression)
            continue
        if isinstance(node, ast.AsyncFunctionDef):
            continue
        module_analyzer.visit(node)
    all_barriers.extend(module_analyzer.barriers)
    edges = {function_id: set(row["calls"]) for function_id, row in rows.items()}
    for component in _strong_components(edges):
        scc_id = _scc_identity(component)
        for member in component:
            rows[member]["scc_id"] = scc_id

    changed = True
    while changed:
        changed = False
        for function_id in sorted(rows):
            for target in rows[function_id]["calls"]:
                changed = _merge(rows[function_id]["effects"], rows[target]["effects"]) or changed
    module_effects = module_analyzer.effects
    for target in module_analyzer.calls:
        _merge(module_effects, rows[target]["effects"])
    if any(barrier["function_id"] == "" for barrier in all_barriers):
        module_effects["may_be_unknown"] = True
    all_barriers = sorted(
        {_barrier_key(barrier): barrier for barrier in all_barriers}.values(),
        key=_barrier_key,
    )
    if len(all_barriers) > MAX_BARRIERS:
        raise ValueError("semantic barrier bound exceeded")
    return {
        "schema_version": ANALYSIS_SCHEMA_VERSION,
        "source_sha256": source_sha256,
        "ast_sha256": ast_sha256,
        "analyzer_sha256": ANALYZER_IDENTITY_SHA256,
        "artifact_sha256": bindings["artifact_sha256"],
        "execution_profile_sha256": bindings["execution_profile_sha256"],
        "import_closure_sha256": bindings["import_closure_sha256"],
        "capability_plan_sha256": bindings["capability_plan_sha256"],
        "module_span": _module_span(tree),
        "module_effects": module_effects,
        "functions": sorted(rows.values(), key=lambda row: row["id"]),
        "barriers": all_barriers,
        "call_site_coverage": "positive_only",
        "call_sites": call_sites,
    }


def analyze_request_json(payload):
    if not isinstance(payload, str) or len(payload.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("invalid semantic analysis request")
    request = json.loads(payload)
    if not isinstance(request, dict) or set(request) != {"source", "bindings", "capabilities"}:
        raise ValueError("invalid semantic analysis request")
    encoded = canonical_analysis_json(
        analyze_source(request["source"], request["bindings"], request["capabilities"])
    )
    if len(encoded.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("semantic analysis response bound exceeded")
    return encoded
