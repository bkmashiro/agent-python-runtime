import ast
import hashlib
import json
import re

from .ast_support import ast_digest_bounded
from .semantic import MAX_SCALAR_OPERATORS, MAX_SOURCE_BYTES


PATCH_SCHEMA_VERSION = "pysolate.source-pass-patch.v1"
PURE_SCALAR_CSE = "pure_scalar_cse"
PURE_SCALAR_CSE_VERSION = "pysolate.pure-scalar-cse-pass.v1"
PURE_SCALAR_FOLD = "pure_scalar_fold"
PURE_SCALAR_FOLD_VERSION = "pysolate.pure-scalar-fold-pass.v1"
SPLIT_PHASE_SOURCES_READ = "split_phase_sources_read"
SPLIT_PHASE_SOURCES_READ_VERSION = "pysolate.split-phase-sources-read-pass.v1"
DATA_LOCAL_NUMPY_SUM = "data_local_numpy_sum"
DATA_LOCAL_NUMPY_SUM_VERSION = "pysolate.data-local-numpy-sum-pass.v1"
_REQUEST_KEYS = {"pass_name", "pass_version", "registration_sha256", "source"}
_PATCH_KEYS = {
    "schema_version", "status", "pass_name", "pass_version", "registration_sha256",
    "original_source_sha256", "original_ast_sha256", "derived_source",
    "derived_source_sha256", "derived_ast_sha256", "replacement_count",
}
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


def _pure_scalar_cse(source):
    if not isinstance(source, str) or not source or len(source.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("invalid source pass input")
    tree = ast.parse(source, filename="<agent-run>", mode="exec")
    if not _closed_scalar_program(tree):
        return tree, "", 0
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
        return tree, "", 0
    derived = bytearray(source.encode("utf-8"))
    for start, end, replacement in reversed(replacements):
        derived[start:end] = replacement
    derived_source = derived.decode("utf-8")
    derived_tree = ast.parse(derived_source, filename="<agent-run>", mode="exec")
    return tree, derived_source, len(replacements)


def _pure_scalar_fold(source):
    if not isinstance(source, str) or not source or len(source.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("invalid source pass input")
    tree = ast.parse(source, filename="<agent-run>", mode="exec")
    if not _closed_scalar_program(tree):
        return tree, "", 0
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
        return tree, "", 0
    derived = bytearray(source.encode("utf-8"))
    for start, end, replacement in reversed(replacements):
        derived[start:end] = replacement
    derived_source = derived.decode("utf-8")
    derived_tree = ast.parse(derived_source, filename="<agent-run>", mode="exec")
    return tree, derived_source, len(replacements)


def _split_phase_sources_read(source):
    if (
        not isinstance(source, str)
        or not source
        or "\r" in source
        or len(source.encode("utf-8")) > MAX_SOURCE_BYTES
    ):
        raise ValueError("invalid source pass input")
    tree = ast.parse(source, filename="<agent-run>", mode="exec")
    lines = source.splitlines(keepends=True)
    if not 2 <= len(tree.body) <= 5 or len(lines) != len(tree.body):
        return tree, "", 0

    reads = []
    names = set()
    for index, statement in enumerate(tree.body[:-1]):
        assignment = _simple_assignment(statement)
        if assignment is None or statement.lineno != index + 1 or statement.end_lineno != statement.lineno:
            return tree, "", 0
        name, value = assignment
        if (
            name in names
            or not isinstance(value, ast.Call)
            or not isinstance(value.func, ast.Attribute)
            or value.func.attr != "read"
            or not isinstance(value.func.value, ast.Name)
            or value.func.value.id != "sources"
            or len(value.args) != 1
            or value.keywords
            or not isinstance(value.args[0], ast.Constant)
            or type(value.args[0].value) is not str
            or not value.args[0].value
            or len(value.args[0].value.encode("utf-8")) > 256
        ):
            return tree, "", 0
        names.add(name)
        reads.append((name, value.args[0].value))

    result = _simple_assignment(tree.body[-1])
    if (
        not reads
        or result is None
        or result[0] != "result"
        or tree.body[-1].lineno != len(tree.body)
        or tree.body[-1].end_lineno != tree.body[-1].lineno
        or not _safe_output_value(result[1], frozenset(names))
    ):
        return tree, "", 0

    source_identity = _digest(source.encode("utf-8"))[7:]
    submit_parts = []
    materialize_lines = []
    for index, (name, path) in enumerate(reads, start=1):
        slot_id = "slot-" + source_identity[:16] + "-" + str(index)
        call_id = "split-" + source_identity[:16] + "-" + str(index)
        request = _canonical({
            "call_id": call_id,
            "capability": "sources.read",
            "arguments": {"path": path},
        })
        submit_parts.append("_pysolate_call_submit(" + repr(slot_id) + ", " + repr(request) + ")")
        materialize_lines.append(name + " = _pysolate_call_materialize(" + repr(slot_id) + ")[\"body\"]")

    derived_lines = list(lines)
    newline = "\n" if lines[0].endswith("\n") else ""
    derived_lines[0] = "; ".join(submit_parts + [materialize_lines[0]]) + newline
    for index in range(1, len(materialize_lines)):
        newline = "\n" if lines[index].endswith("\n") else ""
        derived_lines[index] = materialize_lines[index] + newline
    derived_source = "".join(derived_lines)
    ast.parse(derived_source, filename="<agent-run>", mode="exec")
    return tree, derived_source, len(reads)


def _data_local_numpy_sum(source):
    if (
        not isinstance(source, str)
        or not source
        or "\r" in source
        or len(source.encode("utf-8")) > MAX_SOURCE_BYTES
    ):
        raise ValueError("invalid source pass input")
    tree = ast.parse(source, filename="<agent-run>", mode="exec")
    lines = source.splitlines(keepends=True)
    if len(tree.body) != 3 or len(lines) != 3:
        return tree, "", 0
    import_statement, load_statement, result_statement = tree.body
    if (
        not isinstance(import_statement, ast.Import)
        or len(import_statement.names) != 1
        or import_statement.names[0].name != "numpy"
        or import_statement.names[0].asname != "np"
        or import_statement.lineno != 1
        or import_statement.end_lineno != 1
    ):
        return tree, "", 0
    load_assignment = _simple_assignment(load_statement)
    if load_assignment is None or load_assignment[0] != "dataset" or load_statement.lineno != 2 or load_statement.end_lineno != 2:
        return tree, "", 0
    load_call = load_assignment[1]
    if (
        not isinstance(load_call, ast.Call)
        or not isinstance(load_call.func, ast.Attribute)
        or load_call.func.attr != "load"
        or not isinstance(load_call.func.value, ast.Name)
        or load_call.func.value.id != "np"
        or len(load_call.args) != 1
        or not isinstance(load_call.args[0], ast.Constant)
        or load_call.args[0].value != "/workspace/input.npy"
        or len(load_call.keywords) != 1
        or load_call.keywords[0].arg != "allow_pickle"
        or not isinstance(load_call.keywords[0].value, ast.Constant)
        or load_call.keywords[0].value.value is not False
    ):
        return tree, "", 0
    result_assignment = _simple_assignment(result_statement)
    if result_assignment is None or result_assignment[0] != "result" or result_statement.lineno != 3 or result_statement.end_lineno != 3:
        return tree, "", 0
    outer_call = result_assignment[1]
    if (
        not isinstance(outer_call, ast.Call)
        or not isinstance(outer_call.func, ast.Name)
        or outer_call.func.id != "int"
        or len(outer_call.args) != 1
        or outer_call.keywords
    ):
        return tree, "", 0
    sum_call = outer_call.args[0]
    if (
        not isinstance(sum_call, ast.Call)
        or not isinstance(sum_call.func, ast.Attribute)
        or sum_call.func.attr != "sum"
        or not isinstance(sum_call.func.value, ast.Name)
        or sum_call.func.value.id != "dataset"
        or sum_call.args
        or sum_call.keywords
    ):
        return tree, "", 0
    trailing_newline = "\n" if source.endswith("\n") else ""
    derived_source = "pass\npass\nresult = _pysolate_materialize_slot('slot-numpy-sum-v1')" + trailing_newline
    ast.parse(derived_source, filename="<agent-run>", mode="exec")
    return tree, derived_source, 1


_TRANSFORMS = {
    (PURE_SCALAR_CSE, PURE_SCALAR_CSE_VERSION): _pure_scalar_cse,
    (PURE_SCALAR_FOLD, PURE_SCALAR_FOLD_VERSION): _pure_scalar_fold,
    (SPLIT_PHASE_SOURCES_READ, SPLIT_PHASE_SOURCES_READ_VERSION): _split_phase_sources_read,
    (DATA_LOCAL_NUMPY_SUM, DATA_LOCAL_NUMPY_SUM_VERSION): _data_local_numpy_sum,
}


def emit_source_pass_patch_request_json(request_json):
    request = _decode(request_json, _REQUEST_KEYS)
    transform = _TRANSFORMS.get((request["pass_name"], request["pass_version"]))
    if (
        transform is None
        or not isinstance(request["registration_sha256"], str)
        or _DIGEST.fullmatch(request["registration_sha256"]) is None
        or not isinstance(request["source"], str)
    ):
        raise ValueError("unsupported source pass")
    original_tree, derived_source, replacement_count = transform(request["source"])
    applied = replacement_count > 0
    derived_tree = ast.parse(derived_source, filename="<agent-run>", mode="exec") if applied else None
    patch = {
        "schema_version": PATCH_SCHEMA_VERSION,
        "status": "applied" if applied else "not_applicable",
        "pass_name": request["pass_name"],
        "pass_version": request["pass_version"],
        "registration_sha256": request["registration_sha256"],
        "original_source_sha256": _digest(request["source"].encode("utf-8")),
        "original_ast_sha256": ast_digest_bounded(original_tree),
        "derived_source": derived_source,
        "derived_source_sha256": _digest(derived_source.encode("utf-8")) if applied else "",
        "derived_ast_sha256": ast_digest_bounded(derived_tree) if derived_tree is not None else "",
        "replacement_count": replacement_count,
    }
    return _contract(patch)


def validate_source_pass_execution_request(final_source, patch_json):
    patch = _decode(patch_json, _PATCH_KEYS)
    if patch["status"] != "applied" or patch["replacement_count"] <= 0:
        raise ValueError("source pass patch is not applicable")
    request = _canonical({
        "pass_name": patch["pass_name"],
        "pass_version": patch["pass_version"],
        "registration_sha256": patch["registration_sha256"],
        "source": final_source,
    })
    expected = _decode(emit_source_pass_patch_request_json(request), _PATCH_KEYS)
    if expected != patch:
        raise ValueError("source pass patch does not match the original source")
    return ast.parse(patch["derived_source"], filename="<agent-run>", mode="exec")
