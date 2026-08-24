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
SPLIT_PHASE_SOURCES_READ_VERSION = "pysolate.split-phase-sources-read-pass.v2"
DATA_LOCAL_NUMPY_SUM = "data_local_numpy_sum"
DATA_LOCAL_NUMPY_SUM_VERSION = "pysolate.data-local-numpy-sum-pass.v2"
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


def _split_phase_read_assignment(statement):
    assignment = _simple_assignment(statement)
    if assignment is None:
        return None
    name, value = assignment
    if (
        not isinstance(value, ast.Call)
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
        return None
    return name, value.args[0].value


def _safe_activation_value(node):
    if isinstance(node, ast.Constant):
        return type(node.value) in (bool, int, str, type(None))
    if isinstance(node, ast.Name):
        return node.id == "inputs"
    if isinstance(node, ast.Subscript):
        return _safe_activation_value(node.value) and _safe_activation_value(node.slice)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
        return _safe_activation_value(node.operand)
    if isinstance(node, ast.BoolOp) and isinstance(node.op, (ast.And, ast.Or)):
        return all(_safe_activation_value(value) for value in node.values)
    if isinstance(node, ast.Compare):
        return _safe_activation_value(node.left) and all(
            isinstance(operator, (ast.Eq, ast.NotEq, ast.Is, ast.IsNot, ast.Lt, ast.LtE, ast.Gt, ast.GtE))
            for operator in node.ops
        ) and all(_safe_activation_value(value) for value in node.comparators)
    return False


def _split_phase_parts(source_identity, static_index, name, path):
    slot_id = "slot-" + source_identity[:16] + "-" + str(static_index)
    call_id = "split-" + source_identity[:16] + "-" + str(static_index)
    request = _canonical({
        "call_id": call_id,
        "capability": "sources.read",
        "arguments": {"path": path},
    })
    submit = "_pysolate_call_submit(" + repr(slot_id) + ", " + repr(request) + ")"
    materialize = name + " = _pysolate_call_materialize(" + repr(slot_id) + ")[\"body\"]"
    return submit, materialize


def _rewrite_split_phase_read_block(statements, lines, source_identity, static_index):
    if len(statements) < 2:
        return None
    statement_lines = [statement.lineno for statement in statements]
    if len(set(statement_lines)) != len(statement_lines) or statement_lines != sorted(statement_lines):
        return None
    reads = []
    names = set()
    for statement in statements[:-1]:
        parsed = _split_phase_read_assignment(statement)
        if parsed is None or parsed[0] in names or statement.lineno != statement.end_lineno:
            return None
        names.add(parsed[0])
        reads.append((statement, parsed[0], parsed[1]))
    result = _simple_assignment(statements[-1])
    if not reads or result is None or result[0] != "result" or statements[-1].lineno != statements[-1].end_lineno or not _safe_output_value(result[1], frozenset(names)):
        return None
    submits = []
    materializations = []
    for offset, (_, name, path) in enumerate(reads):
        submit, materialize = _split_phase_parts(source_identity, static_index + offset, name, path)
        submits.append(submit)
        materializations.append(materialize)
    first_line = reads[0][0].lineno - 1
    indent = lines[first_line][: len(lines[first_line]) - len(lines[first_line].lstrip(" "))]
    newline = "\n" if lines[first_line].endswith("\n") else ""
    lines[first_line] = indent + "; ".join(submits + [materializations[0]]) + newline
    for offset, (statement, _, _) in enumerate(reads[1:], start=1):
        line_index = statement.lineno - 1
        indent = lines[line_index][: len(lines[line_index]) - len(lines[line_index].lstrip(" "))]
        newline = "\n" if lines[line_index].endswith("\n") else ""
        lines[line_index] = indent + materializations[offset] + newline
    return len(reads)


def _split_phase_structured(tree, lines, source_identity):
    derived = list(lines)
    if len(tree.body) == 1 and isinstance(tree.body[0], ast.If):
        statement = tree.body[0]
        if not _safe_activation_value(statement.test) or not statement.body or statement.body[0].lineno == statement.lineno:
            return None
        count = _rewrite_split_phase_read_block(statement.body, derived, source_identity, 1)
        if count is None:
            return None
        if statement.orelse:
            second = _rewrite_split_phase_read_block(statement.orelse, derived, source_identity, count + 1)
            if second is None:
                lone = _simple_assignment(statement.orelse[0]) if len(statement.orelse) == 1 else None
                if lone is None or lone[0] != "result" or not _safe_output_value(lone[1], frozenset()):
                    return None
            else:
                count += second
        return "".join(derived), count
    if len(tree.body) == 2 and isinstance(tree.body[1], ast.For):
        initial = _simple_assignment(tree.body[0])
        loop = tree.body[1]
        if (
            initial is None or initial[0] != "result" or not isinstance(initial[1], ast.List) or initial[1].elts
            or not isinstance(loop.target, ast.Name) or not _safe_activation_value(loop.iter) or loop.orelse
            or len(loop.body) != 2
        ):
            return None
        read = _split_phase_read_assignment(loop.body[0])
        append = loop.body[1]
        if not (
            read is not None and isinstance(append, ast.Expr) and isinstance(append.value, ast.Call)
            and loop.body[0].lineno != loop.lineno and loop.body[0].lineno == loop.body[0].end_lineno
            and append.lineno == append.end_lineno and append.lineno != loop.body[0].lineno
            and isinstance(append.value.func, ast.Attribute) and append.value.func.attr == "append"
            and isinstance(append.value.func.value, ast.Name) and append.value.func.value.id == "result"
            and len(append.value.args) == 1 and isinstance(append.value.args[0], ast.Name) and append.value.args[0].id == read[0]
            and not append.value.keywords
        ):
            return None
        submit, materialize = _split_phase_parts(source_identity, 1, read[0], read[1])
        line_index = loop.body[0].lineno - 1
        indent = derived[line_index][: len(derived[line_index]) - len(derived[line_index].lstrip(" "))]
        newline = "\n" if derived[line_index].endswith("\n") else ""
        derived[line_index] = indent + submit + "; " + materialize + newline
        return "".join(derived), 1
    return None


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
    source_identity = _digest(source.encode("utf-8"))[7:]
    structured = _split_phase_structured(tree, lines, source_identity)
    if structured is not None:
        derived_source, replacement_count = structured
        ast.parse(derived_source, filename="<agent-run>", mode="exec")
        return tree, derived_source, replacement_count
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
    if len(tree.body) != 4 or len(lines) != 4:
        return tree, "", 0
    io_import, numpy_import, load_statement, result_statement = tree.body
    if (
        not isinstance(io_import, ast.Import)
        or len(io_import.names) != 1
        or io_import.names[0].name != "io"
        or io_import.names[0].asname is not None
        or io_import.lineno != 1
        or io_import.end_lineno != 1
        or not isinstance(numpy_import, ast.Import)
        or len(numpy_import.names) != 1
        or numpy_import.names[0].name != "numpy"
        or numpy_import.names[0].asname != "np"
        or numpy_import.lineno != 2
        or numpy_import.end_lineno != 2
    ):
        return tree, "", 0
    load_assignment = _simple_assignment(load_statement)
    if load_assignment is None or load_assignment[0] != "dataset" or load_statement.lineno != 3 or load_statement.end_lineno != 3:
        return tree, "", 0
    load_call = load_assignment[1]
    if (
        not isinstance(load_call, ast.Call)
        or not isinstance(load_call.func, ast.Attribute)
        or load_call.func.attr != "load"
        or not isinstance(load_call.func.value, ast.Name)
        or load_call.func.value.id != "np"
        or len(load_call.args) != 1
        or len(load_call.keywords) != 1
        or load_call.keywords[0].arg != "allow_pickle"
        or not isinstance(load_call.keywords[0].value, ast.Constant)
        or load_call.keywords[0].value.value is not False
    ):
        return tree, "", 0
    bytes_io = load_call.args[0]
    if (
        not isinstance(bytes_io, ast.Call)
        or not isinstance(bytes_io.func, ast.Attribute)
        or bytes_io.func.attr != "BytesIO"
        or not isinstance(bytes_io.func.value, ast.Name)
        or bytes_io.func.value.id != "io"
        or len(bytes_io.args) != 1
        or bytes_io.keywords
    ):
        return tree, "", 0
    read_call = bytes_io.args[0]
    if (
        not isinstance(read_call, ast.Call)
        or read_call.args
        or read_call.keywords
        or not isinstance(read_call.func, ast.Attribute)
        or read_call.func.attr != "read"
    ):
        return tree, "", 0
    open_call = read_call.func.value
    if (
        not isinstance(open_call, ast.Call)
        or not isinstance(open_call.func, ast.Name)
        or open_call.func.id != "open"
        or len(open_call.args) != 2
        or open_call.keywords
        or not isinstance(open_call.args[0], ast.Constant)
        or open_call.args[0].value != "/workspace/input.npy"
        or not isinstance(open_call.args[1], ast.Constant)
        or open_call.args[1].value != "rb"
    ):
        return tree, "", 0
    result_assignment = _simple_assignment(result_statement)
    if result_assignment is None or result_assignment[0] != "result" or result_statement.lineno != 4 or result_statement.end_lineno != 4:
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
    derived_source = "pass\npass\npass\nresult = _pysolate_materialize_slot('slot-numpy-sum-v1')" + trailing_newline
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
