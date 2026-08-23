import ast
import hashlib
import json
import re

from .ast_support import ast_digest_bounded
from .semantic import MAX_SOURCE_BYTES, _safe_scalar_expression


PATCH_SCHEMA_VERSION = "pysolate.source-pass-patch.v1"
PURE_SCALAR_CSE = "pure_scalar_cse"
PURE_SCALAR_CSE_VERSION = "pysolate.pure-scalar-cse-pass.v1"
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


def _pure_scalar_cse(source):
    if not isinstance(source, str) or not source or len(source.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("invalid source pass input")
    tree = ast.parse(source, filename="<agent-run>", mode="exec")
    scalar_names = {}
    replacements = []
    index = 0
    while index < len(tree.body):
        first = _simple_assignment(tree.body[index])
        if first is None:
            scalar_names.clear()
            index += 1
            continue
        first_name, first_value = first
        first_type = _safe_scalar_expression(first_value, scalar_names)
        if first_type is None:
            scalar_names.pop(first_name, None)
            index += 1
            continue
        after_first = dict(scalar_names)
        after_first[first_name] = first_type
        if index + 1 < len(tree.body):
            second = _simple_assignment(tree.body[index + 1])
            if second is not None:
                second_name, second_value = second
                second_type = _safe_scalar_expression(second_value, after_first)
                if (
                    second_name != first_name
                    and second_type == first_type
                    and ast.dump(first_value, include_attributes=False) == ast.dump(second_value, include_attributes=False)
                ):
                    start, end = _span_offsets(source, second_value)
                    encoded_name = first_name.encode("ascii")
                    if len(encoded_name) <= end - start:
                        replacements.append((start, end, encoded_name + b" " * (end - start - len(encoded_name))))
                        scalar_names = after_first
                        scalar_names[second_name] = second_type
                        index += 2
                        continue
        scalar_names = after_first
        index += 1

    if not replacements:
        return tree, "", 0
    derived = bytearray(source.encode("utf-8"))
    for start, end, replacement in reversed(replacements):
        derived[start:end] = replacement
    derived_source = derived.decode("utf-8")
    derived_tree = ast.parse(derived_source, filename="<agent-run>", mode="exec")
    return tree, derived_source, len(replacements)


def emit_source_pass_patch_request_json(request_json):
    request = _decode(request_json, _REQUEST_KEYS)
    if (
        request["pass_name"] != PURE_SCALAR_CSE
        or request["pass_version"] != PURE_SCALAR_CSE_VERSION
        or not isinstance(request["registration_sha256"], str)
        or _DIGEST.fullmatch(request["registration_sha256"]) is None
        or not isinstance(request["source"], str)
    ):
        raise ValueError("unsupported source pass")
    original_tree, derived_source, replacement_count = _pure_scalar_cse(request["source"])
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
    expected = emit_source_pass_patch_request_json(request)
    if expected != patch_json:
        raise ValueError("source pass patch does not match the original source")
    return ast.parse(patch["derived_source"], filename="<agent-run>", mode="exec")
