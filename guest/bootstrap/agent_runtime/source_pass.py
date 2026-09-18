import ast
import hashlib
import json
import re

from .semantic import MAX_SCALAR_OPERATORS, MAX_SOURCE_BYTES


PATCH_SCHEMA_VERSION = "pysolate.source-pass-patch.v3"
PURE_SCALAR_CSE = "pure_scalar_cse"
PURE_SCALAR_CSE_VERSION = "pysolate.pure-scalar-cse-pass.v1"
PURE_SCALAR_FOLD = "pure_scalar_fold"
PURE_SCALAR_FOLD_VERSION = "pysolate.pure-scalar-fold-pass.v1"
_REQUEST_KEYS = {"pass_name", "pass_version", "registration_sha256", "source"}
_PATCH_COMMON_KEYS = {
    "schema_version", "status", "pass_name", "pass_version", "registration_sha256",
    "original_source_sha256", "replacement_count",
}
_PATCH_KEYS = _PATCH_COMMON_KEYS | {"derived_source", "derived_source_sha256"}

_DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
def _digest(value):
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def _contract(value):
    return _canonical(value) + "\n"


def _decode(raw, keys):
    if not isinstance(raw, str):
        raise ValueError("invalid source pass contract")
    value = json.loads(raw)
    if not isinstance(value, dict) or set(value) != keys:
        raise ValueError("invalid source pass contract")
    return value


def _span_offsets(source, node):
    if node.lineno != node.end_lineno:
        raise ValueError("scalar CSE only supports one-line expressions")
    lines = source.encode("utf-8").splitlines(keepends=True)
    row = lines[node.lineno - 1]
    start = sum(len(line) for line in lines[: node.lineno - 1]) + node.col_offset
    end = sum(len(line) for line in lines[: node.end_lineno - 1]) + node.end_col_offset
    if start >= end or end > len(source.encode("utf-8")) or any(value >= 128 for value in row[node.col_offset:node.end_col_offset]):
        raise ValueError("scalar CSE expression span is unsupported")
    return start, end


def _simple_assignment(statement):
    if not isinstance(statement, ast.Assign) or len(statement.targets) != 1 or not isinstance(statement.targets[0], ast.Name):
        return None
    if statement.type_comment is not None:
        return None
    return statement.targets[0].id, statement.value


_MISSING = object()


def _int64_value(node, scalar_values, operators=None):
    if operators is None:
        operators = [0]
    if isinstance(node, ast.Constant) and type(node.value) in (bool, int):
        value = node.value
    elif isinstance(node, ast.Name):
        return scalar_values.get(node.id, _MISSING)
    elif isinstance(node, ast.BinOp) and isinstance(node.op, (ast.Add, ast.Sub, ast.Mult)):
        operators[0] += 1
        if operators[0] > MAX_SCALAR_OPERATORS:
            return _MISSING
        left = _int64_value(node.left, scalar_values, operators)
        right = _int64_value(node.right, scalar_values, operators)
        if left is _MISSING or right is _MISSING:
            return _MISSING
        if isinstance(node.op, ast.Add):
            value = left + right
        elif isinstance(node.op, ast.Sub):
            value = left - right
        else:
            value = left * right
    else:
        return _MISSING
    if type(value) not in (bool, int) or value < -(1 << 63) or value >= (1 << 63):
        return _MISSING
    return value


def _safe_output_value(node, scalar_names):
    if isinstance(node, ast.Name):
        return node.id in scalar_names
    if isinstance(node, ast.Constant):
        return node.value is None or type(node.value) in (bool, int, str)
    if isinstance(node, (ast.List, ast.Tuple)):
        return all(_safe_output_value(value, scalar_names) for value in node.elts)
    if isinstance(node, ast.Dict):
        return all(
            key is not None
            and _safe_output_value(key, scalar_names)
            and _safe_output_value(value, scalar_names)
            for key, value in zip(node.keys, node.values)
        )
    if isinstance(node, ast.Compare) and len(node.ops) == 1:
        return (
            isinstance(node.ops[0], (ast.Eq, ast.NotEq, ast.Lt, ast.LtE, ast.Gt, ast.GtE))
            and _safe_output_value(node.left, scalar_names)
            and _safe_output_value(node.comparators[0], scalar_names)
        )
    return False


def _closed_scalar_program(tree):
    if not tree.body:
        return False
    scalar_values = {}
    last_name = None
    for index, statement in enumerate(tree.body):
        assignment = _simple_assignment(statement)
        if assignment is None:
            return False
        name, value = assignment
        last_name = name
        result = _int64_value(value, scalar_values)
        if result is _MISSING:
            return (
                index == len(tree.body) - 1
                and name == "result"
                and _safe_output_value(value, frozenset(scalar_values))
            )
        scalar_values[name] = result
    return last_name == "result"


def _pure_scalar_cse(source, prepared_tree=None):
    if not isinstance(source, str) or not source or len(source.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("invalid source pass input")
    tree = _tree_for_source(source, prepared_tree)
    if not _closed_scalar_program(tree):
        return "", None, 0
    scalar_values = {}
    replacements = []
    index = 0
    while index < len(tree.body):
        first = _simple_assignment(tree.body[index])
        if first is None:
            scalar_values.clear()
            index += 1
            continue
        first_name, first_value = first
        first_result = _int64_value(first_value, scalar_values)
        if first_result is _MISSING:
            scalar_values.clear()
            index += 1
            continue
        after_first = dict(scalar_values)
        after_first[first_name] = first_result
        if index + 1 < len(tree.body):
            second = _simple_assignment(tree.body[index + 1])
            if second is not None:
                second_name, second_value = second
                second_result = _int64_value(second_value, after_first)
                if (
                    second_name != first_name
                    and second_result is not _MISSING
                    and second_result == first_result
                    and ast.dump(first_value, include_attributes=False) == ast.dump(second_value, include_attributes=False)
                ):
                    start, end = _span_offsets(source, second_value)
                    encoded_name = first_name.encode("utf-8")
                    if len(encoded_name) <= end - start:
                        replacements.append((start, end, encoded_name + b" " * (end - start - len(encoded_name))))
                        scalar_values = after_first
                        scalar_values[second_name] = second_result
                        index += 2
                        continue
        scalar_values = after_first
        index += 1

    if not replacements:
        return "", None, 0
    derived = bytearray(source.encode("utf-8"))
    for start, end, replacement in reversed(replacements):
        derived[start:end] = replacement
    derived_source = derived.decode("utf-8")
    derived_tree = ast.parse(derived_source, filename="<agent-run>", mode="exec")
    return derived_source, derived_tree, len(replacements)


def _pure_scalar_fold(source, prepared_tree=None):
    if not isinstance(source, str) or not source or len(source.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("invalid source pass input")
    tree = _tree_for_source(source, prepared_tree)
    if not _closed_scalar_program(tree):
        return "", None, 0
    scalar_values = {}
    replacements = []
    for statement in tree.body:
        assignment = _simple_assignment(statement)
        if assignment is None:
            scalar_values.clear()
            continue
        name, value = assignment
        result = _int64_value(value, scalar_values)
        if result is _MISSING:
            scalar_values.clear()
            continue
        scalar_values[name] = result
        if not isinstance(value, ast.BinOp):
            continue
        start, end = _span_offsets(source, value)
        replacement = repr(result).encode("ascii")
        if len(replacement) <= end - start:
            replacements.append((start, end, replacement + b" " * (end - start - len(replacement))))

    if not replacements:
        return "", None, 0
    derived = bytearray(source.encode("utf-8"))
    for start, end, replacement in reversed(replacements):
        derived[start:end] = replacement
    derived_source = derived.decode("utf-8")
    derived_tree = ast.parse(derived_source, filename="<agent-run>", mode="exec")
    return derived_source, derived_tree, len(replacements)


_TRANSFORMS = {
    (PURE_SCALAR_CSE, PURE_SCALAR_CSE_VERSION): _pure_scalar_cse,
    (PURE_SCALAR_FOLD, PURE_SCALAR_FOLD_VERSION): _pure_scalar_fold,
}


_pending_selection = None


def reset_state():
    global _pending_selection
    _pending_selection = None


def _tree_for_source(source, prepared_tree):
    if prepared_tree is None:
        return ast.parse(source, filename="<agent-run>", mode="exec")
    if not isinstance(prepared_tree, ast.Module):
        raise ValueError("invalid prepared source tree")
    return prepared_tree


def emit_source_pass_patch_request_json(request_json, prepared_tree=None):
    reset_state()
    request = _decode(request_json, _REQUEST_KEYS)
    transform = _TRANSFORMS.get((request["pass_name"], request["pass_version"]))
    if (
        transform is None
        or not isinstance(request["registration_sha256"], str)
        or _DIGEST.fullmatch(request["registration_sha256"]) is None
        or not isinstance(request["source"], str)
    ):
        raise ValueError("unsupported source pass")
    derived_source, derived_tree, replacement_count = transform(request["source"], prepared_tree)
    applied = replacement_count > 0
    patch = {
        "schema_version": PATCH_SCHEMA_VERSION,
        "status": "applied" if applied else "not_applicable",
        "pass_name": request["pass_name"],
        "pass_version": request["pass_version"],
        "registration_sha256": request["registration_sha256"],
        "original_source_sha256": _digest(request["source"].encode("utf-8")),
        "replacement_count": replacement_count,
    }
    patch["derived_source"] = derived_source
    patch["derived_source_sha256"] = _digest(derived_source.encode("utf-8")) if applied else ""
    if applied:
        global _pending_selection
        _pending_selection = (request["source"], patch, derived_tree)
    return _contract(patch)


def validate_source_pass_execution_request(final_source, patch_json):
    global _pending_selection
    patch = _decode(patch_json, _PATCH_KEYS)
    if patch["status"] != "applied" or patch["replacement_count"] <= 0:
        raise ValueError("source pass patch is not applicable")
    pending = _pending_selection
    _pending_selection = None
    if pending is not None:
        pending_source, pending_patch, pending_tree = pending
        if final_source != pending_source or patch != pending_patch:
            raise ValueError("source pass patch does not match the original source")
        return pending_tree
    request = _canonical({
        "pass_name": patch["pass_name"],
        "pass_version": patch["pass_version"],
        "registration_sha256": patch["registration_sha256"],
        "source": final_source,
    })
    expected = _decode(emit_source_pass_patch_request_json(request), _PATCH_KEYS)
    local_selection = _pending_selection
    _pending_selection = None
    if expected != patch:
        raise ValueError("source pass patch does not match the original source")
    if local_selection is None:
        raise ValueError("source pass derived tree is unavailable")
    _, _, derived_tree = local_selection
    return derived_tree
