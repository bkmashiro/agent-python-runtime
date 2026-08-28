"""Narrow final-Guest compiler pass for logical-time-preserving capability calls."""

import ast
import copy
import hashlib
import json

from .semantic import MAX_SOURCE_BYTES


PATCH_SCHEMA_VERSION = "pysolate.source-pass-patch.v3"
PASS_NAME = "plm_capability_calls"
PASS_VERSION = "pysolate.plm-capability-calls-pass.v1"
_pending_selection = None


def reset_state():
    global _pending_selection
    _pending_selection = None


def _canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def _digest(value):
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _simple_assignment(statement):
    if not isinstance(statement, ast.Assign) or len(statement.targets) != 1:
        return None
    if not isinstance(statement.targets[0], ast.Name) or statement.type_comment is not None:
        return None
    return statement.targets[0].id, statement.value


def _projection_index(projections):
    if not isinstance(projections, list) or not projections:
        raise ValueError("invalid PLM capability projections")
    index = {}
    try:
        for projection in projections:
            key = projection["module"], projection["method"]
            arguments = tuple(projection["arguments"])
            if key in index:
                raise ValueError("duplicate PLM capability projection")
            index[key] = {
                "capability": projection["capability"],
                "module": key[0],
                "method": key[1],
                "arguments": arguments,
                "result_field": projection["result_field"],
            }
    except (KeyError, TypeError, ValueError):
            raise ValueError("invalid PLM capability projection")
    return index


def _projected_call(node, projections):
    if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Attribute):
        return None
    if not isinstance(node.func.value, ast.Name):
        return None
    return projections.get((node.func.value.id, node.func.attr))


def _observes_transformed_code(node):
    if isinstance(node, ast.Attribute):
        return node.attr.startswith("co_") or node.attr in {
            "__code__", "__traceback__", "tb_frame", "tb_next", "tb_lineno",
            "f_code", "f_back", "gi_code", "cr_code", "ag_code", "_getframe",
            "settrace", "gettrace", "setprofile", "getprofile", "call_tracing", "monitoring",
        }
    if isinstance(node, ast.Name):
        return node.id in {
            "__import__", "breakpoint", "compile", "dir", "eval", "exec", "getattr", "globals",
            "hasattr", "locals", "vars",
        }
    if isinstance(node, ast.Import):
        return any(alias.name.partition(".")[0] in {"dis", "inspect", "traceback"} for alias in node.names)
    if isinstance(node, ast.ImportFrom) and node.module is not None:
        return node.module.partition(".")[0] in {"dis", "inspect", "traceback"}
    return False


def _mutates_projection_receiver(node, modules):
    if isinstance(node, ast.Name) and isinstance(node.ctx, (ast.Store, ast.Del)):
        return node.id in modules
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
        return node.name in modules
    if isinstance(node, ast.arg):
        return node.arg in modules
    if isinstance(node, ast.Attribute) and isinstance(node.ctx, (ast.Store, ast.Del)):
        return isinstance(node.value, ast.Name) and node.value.id in modules
    if isinstance(node, ast.Attribute) and node.attr == "__dict__":
        return isinstance(node.value, ast.Name) and node.value.id in modules
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id in {"setattr", "delattr"}:
        return bool(node.args) and isinstance(node.args[0], ast.Name) and node.args[0].id in modules
    if isinstance(node, ast.alias) and node.asname in modules:
        return node.name.partition(".")[0] != node.asname
    if isinstance(node, ast.ImportFrom):
        return any((alias.asname or alias.name) in modules for alias in node.names)
    return False


def _isolated_physical_line(node, lines):
    if node.lineno != node.end_lineno or node.lineno < 1 or node.lineno > len(lines):
        return False
    line = lines[node.lineno - 1]
    return not line[:node.col_offset].strip() and (
        not line[node.end_col_offset:].strip()
        or line[node.end_col_offset:].strip().startswith("#")
    )


def _capability_assignment(statement, lines, projections):
    assignment = _simple_assignment(statement)
    if assignment is None or not _isolated_physical_line(statement, lines):
        return None
    name, call = assignment
    if not isinstance(call, ast.Call):
        return None
    projection = _projected_call(call, projections)
    if (
        projection is None
        or call.lineno != call.end_lineno
        or any(isinstance(value, ast.Starred) for value in call.args)
    ):
        return None
    argument_names = projection["arguments"]
    if len(call.args) > len(argument_names) or any(keyword.arg is None for keyword in call.keywords):
        return None
    values = {argument_names[index]: value for index, value in enumerate(call.args)}
    for keyword in call.keywords:
        if keyword.arg not in argument_names or keyword.arg in values:
            return None
        values[keyword.arg] = keyword.value
    if set(values) != set(argument_names):
        return None
    loaded_names = set()
    for argument_name in argument_names:
        value = values[argument_name]
        if isinstance(value, ast.Name):
            loaded_names.add(value.id)
        elif not (
            isinstance(value, ast.Constant)
            and (value.value is None or type(value.value) in (bool, int, float, str))
        ):
            return None
    return name, call, projection, loaded_names, values


def _transparent_expression(node):
    if isinstance(node, ast.Constant):
        return node.value is None or type(node.value) in (bool, int, float, str)
    if isinstance(node, ast.Name):
        return True
    if isinstance(node, ast.BinOp) and isinstance(node.op, (ast.Add, ast.Sub, ast.Mult)):
        return _transparent_expression(node.left) and _transparent_expression(node.right)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.UAdd, ast.USub, ast.Not)):
        return _transparent_expression(node.operand)
    return False


def _transparent_statement(statement):
    assignment = _simple_assignment(statement)
    return (
        assignment is not None
        and statement.lineno == statement.end_lineno
        and _transparent_expression(assignment[1])
    )


def _site(call):
    return "s%dc%d-e%dc%d" % (
        call.lineno, call.col_offset, call.end_lineno, call.end_col_offset,
    )


def _arguments_tree(argument_names, values):
    return ast.Dict(
        keys=[ast.Constant(value=name) for name in argument_names],
        values=[copy.deepcopy(values[name]) for name in argument_names],
    )


def _runtime_name(name):
    node = ast.Name(id=name, ctx=ast.Load())
    setattr(node, "_pysolate_trusted_runtime", True)
    return node


def _prepare_tree(base_slot, base_call, projection, argument_names, values, location):
    node = ast.Expr(value=ast.Call(
        func=_runtime_name("_pysolate_plm_prepare"),
        args=[
            ast.Constant(value=base_slot),
            ast.Constant(value=base_call),
            ast.Constant(value=projection["capability"]),
            _arguments_tree(argument_names, values),
        ],
        keywords=[],
    ))
    return ast.fix_missing_locations(ast.copy_location(node, location))


def _materialize_tree(name, base_slot, projection, argument_names, values, location):
    value = ast.Call(
        func=_runtime_name("_pysolate_plm_linearize"),
        args=[
            ast.Constant(value=base_slot),
            ast.Constant(value=projection["capability"]),
            _arguments_tree(argument_names, values),
        ],
        keywords=[],
    )
    if projection["result_field"]:
        value = ast.Subscript(
            value=value,
            slice=ast.Constant(value=projection["result_field"]),
            ctx=ast.Load(),
        )
    node = ast.Assign(targets=[ast.Name(id=name, ctx=ast.Store())], value=value)
    return ast.fix_missing_locations(ast.copy_location(node, location))


def _transform(source, projections, prepared_tree=None):
    if (
        not isinstance(source, str)
        or not source
        or "\r" in source
        or len(source.encode("utf-8")) > MAX_SOURCE_BYTES
    ):
        raise ValueError("invalid source pass input")
    projection_index = _projection_index(projections)
    projection_modules = {module for module, _ in projection_index}
    tree = prepared_tree if isinstance(prepared_tree, ast.Module) else ast.parse(
        source, filename="<agent-run>", mode="exec"
    )
    lines = source.splitlines()
    projected_calls = set()
    for node in ast.walk(tree):
        if _observes_transformed_code(node) or _mutates_projection_receiver(node, projection_modules):
            return tree, 0, None
        if _projected_call(node, projection_index) is not None:
            projected_calls.add(id(node))
    if not projected_calls:
        return tree, 0, None

    edits = {}
    supported_calls = set()

    def edit(statement):
        return edits.setdefault(id(statement), {"before": [], "after": [], "replacement": None})

    def process_block(statements, inherited_names):
        if not statements:
            return
        visible_names = set(inherited_names)
        definitions = {}
        barrier = -1

        for index, statement in enumerate(statements):
            assignment = _simple_assignment(statement)
            direct_projection = (
                assignment is not None
                and _projected_call(assignment[1], projection_index) is not None
            )
            parsed = _capability_assignment(statement, lines, projection_index)
            if parsed is not None:
                name, call, projection, loaded_names, values = parsed
                if loaded_names.issubset(visible_names):
                    supported_calls.add(id(call))
                    issue_after = max(
                        barrier,
                        max((definitions.get(loaded_name, -1) for loaded_name in loaded_names), default=-1),
                    )
                    site = _site(call)
                    base_slot = "slot-" + site
                    argument_names = projection["arguments"]
                    target = statements[0] if issue_after < 0 else statements[issue_after]
                    position = "before" if issue_after < 0 else "after"
                    edit(target)[position].append(_prepare_tree(
                        base_slot, "plm-" + site, projection, argument_names, values, target
                    ))
                    edit(statement)["replacement"] = _materialize_tree(
                        name, base_slot, projection, argument_names, values, statement
                    )
            if isinstance(statement, ast.If):
                process_block(statement.body, visible_names)
                process_block(statement.orelse, visible_names)
            elif isinstance(statement, (ast.For, ast.AsyncFor)) and isinstance(statement.target, ast.Name):
                process_block(statement.body, visible_names | {statement.target.id})
                process_block(statement.orelse, visible_names)
            if assignment is not None:
                visible_names.add(assignment[0])
                definitions[assignment[0]] = index
            if not direct_projection and not _transparent_statement(statement):
                barrier = index

        rewritten = []
        for statement in statements:
            operations = edits.get(id(statement))
            if operations is None:
                rewritten.append(statement)
                continue
            rewritten.extend(operations["before"])
            rewritten.append(operations["replacement"] or statement)
            rewritten.extend(operations["after"])
        statements[:] = rewritten

    process_block(tree.body, {"inputs"})
    if supported_calls != projected_calls:
        return tree, 0, None
    return tree, len(supported_calls), tree


def emit_patch(request_json, prepared_tree=None):
    global _pending_selection
    reset_state()
    try:
        request = json.loads(request_json)
        source = request["source"]
        projections = request["capability_projections"]
        registration = request["registration_sha256"]
    except (KeyError, TypeError, ValueError) as exc:
        raise ValueError("invalid PLM source pass request") from exc
    if not isinstance(request, dict) or request.get("pass_name") != PASS_NAME or request.get("pass_version") != PASS_VERSION:
        raise ValueError("unsupported PLM source pass")
    _, replacement_count, selected_tree = _transform(source, projections, prepared_tree)
    patch = {
        "schema_version": PATCH_SCHEMA_VERSION,
        "status": "applied" if replacement_count else "not_applicable",
        "pass_name": PASS_NAME,
        "pass_version": PASS_VERSION,
        "registration_sha256": registration,
        "original_source_sha256": _digest(source.encode("utf-8")),
        "replacement_count": replacement_count,
        "capability_projections": projections,
    }
    if replacement_count:
        _pending_selection = (source, patch, selected_tree)
    return _canonical(patch) + "\n"


def select_tree(final_source, patch_json):
    global _pending_selection
    try:
        patch = json.loads(patch_json)
    except (TypeError, ValueError) as exc:
        raise ValueError("invalid PLM source pass patch") from exc
    pending = _pending_selection
    _pending_selection = None
    if pending is None:
        raise ValueError("PLM source pass selection is unavailable")
    pending_source, pending_patch, pending_tree = pending
    if final_source != pending_source or patch != pending_patch:
        raise ValueError("PLM source pass patch does not match the admitted source")
    return pending_tree
