import ast
import copy
import hashlib
import json
import re

from .semantic import MAX_SOURCE_BYTES, _uses_reserved_helper_binding


DECISION_SCHEMA_VERSION = "pysolate.prepared-region-decision.v1"
PATCH_SCHEMA_VERSION = "pysolate.prepared-region-execution-patch.v1"
PASS_SCHEMA_VERSION = "pysolate.prepared-pure-region-pass.v1"
CONSUMER = "prepared_pure_region"
CODEC = "canonical_json_bool_or_int64.v1"
HELPER_BINDING = "__pysolate_materialize_value__"
_DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
_IDENTIFIER = re.compile(r"^[A-Za-z_][A-Za-z0-9_]{0,127}$")
_SPAN_KEYS = {"start_line", "start_column", "end_line", "end_column"}
_DECISION_KEYS = {
    "schema_version", "consumer", "pass_schema", "max_payload_bytes",
    "source_sha256", "ast_sha256", "analysis_sha256", "region_id", "region_span",
    "region_source_sha256", "live_ins_sha256", "environment_sha256",
    "execution_profile_sha256", "import_closure_sha256", "capability_plan_sha256",
    "pass_config_sha256", "codec", "output_name", "identity_sha256",
}
_PATCH_KEYS = {
    "schema_version", "pass_schema", "helper_binding", "decision_sha256",
    "final_source_sha256", "final_ast_sha256", "derived_ast_sha256", "region_id",
    "region_span", "output_name", "identity_sha256",
}
_EMIT_REQUEST_KEYS = {"decision", "final_source"}


def _digest(value):
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True)


def _contract_canonical(value):
    return _canonical(value) + "\n"


def _reject_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate prepared region field")
        value[key] = item
    return value


def _decode(raw, keys, contract=False):
    if not isinstance(raw, str) or len(raw.encode("utf-8")) > 16 * 1024:
        raise ValueError("invalid prepared region contract")
    try:
        value = json.loads(raw, object_pairs_hook=_reject_duplicates)
    except (TypeError, ValueError, json.JSONDecodeError) as exc:
        raise ValueError("invalid prepared region contract") from exc
    expected = _contract_canonical(value) if contract else _canonical(value)
    if not isinstance(value, dict) or set(value) != keys or expected != raw:
        raise ValueError("invalid prepared region contract")
    return value


def _valid_digest(value):
    return isinstance(value, str) and _DIGEST.fullmatch(value) is not None


def _valid_span(value):
    if not isinstance(value, dict) or set(value) != _SPAN_KEYS:
        return False
    if any(type(value[key]) is not int or value[key] < 0 for key in _SPAN_KEYS):
        return False
    return value["start_line"] > 0 and value["end_line"] >= value["start_line"] and (
        value["end_line"] != value["start_line"] or value["end_column"] >= value["start_column"]
    )


def _validate_identity(value):
    claimed = value.get("identity_sha256")
    identity = dict(value)
    identity.pop("identity_sha256", None)
    return _valid_digest(claimed) and claimed == _digest(_contract_canonical(identity).encode("utf-8"))


def _decode_decision(raw):
    value = _decode(raw, _DECISION_KEYS, contract=True)
    digests = (
        "source_sha256", "ast_sha256", "analysis_sha256", "region_id",
        "region_source_sha256", "live_ins_sha256", "environment_sha256",
        "execution_profile_sha256", "import_closure_sha256", "capability_plan_sha256",
        "pass_config_sha256",
    )
    if (
        value["schema_version"] != DECISION_SCHEMA_VERSION
        or value["consumer"] != CONSUMER
        or value["pass_schema"] != PASS_SCHEMA_VERSION
        or value["max_payload_bytes"] != 256
        or value["codec"] != CODEC
        or not isinstance(value["output_name"], str)
        or _IDENTIFIER.fullmatch(value["output_name"]) is None
        or value["output_name"] == HELPER_BINDING
        or not _valid_span(value["region_span"])
        or not all(_valid_digest(value[key]) for key in digests)
        or not _validate_identity(value)
    ):
        raise ValueError("invalid prepared region decision")
    return value


def _decode_patch(raw):
    value = _decode(raw, _PATCH_KEYS, contract=True)
    if (
        value["schema_version"] != PATCH_SCHEMA_VERSION
        or value["pass_schema"] != PASS_SCHEMA_VERSION
        or value["helper_binding"] != HELPER_BINDING
        or not _valid_span(value["region_span"])
        or not isinstance(value["output_name"], str)
        or _IDENTIFIER.fullmatch(value["output_name"]) is None
        or value["output_name"] == HELPER_BINDING
        or not all(_valid_digest(value[key]) for key in (
            "decision_sha256", "final_source_sha256", "final_ast_sha256",
            "derived_ast_sha256", "region_id",
        ))
        or not _validate_identity(value)
    ):
        raise ValueError("invalid prepared region patch")
    return value


def _ast_digest(tree):
    return _digest(ast.dump(tree, annotate_fields=True, include_attributes=False).encode("utf-8"))


def _source_slice(source, span):
    encoded = source.encode("utf-8")
    lines = encoded.splitlines(keepends=True)
    if span["start_line"] > len(lines) or span["end_line"] > len(lines):
        raise ValueError("prepared region span is outside final source")
    start_line = lines[span["start_line"] - 1]
    end_line = lines[span["end_line"] - 1]
    if span["start_column"] > len(start_line) or span["end_column"] > len(end_line):
        raise ValueError("prepared region column is outside final source")
    start = sum(len(row) for row in lines[:span["start_line"] - 1]) + span["start_column"]
    end = sum(len(row) for row in lines[:span["end_line"] - 1]) + span["end_column"]
    return encoded[start:end]


def _span(node):
    return {
        "start_line": node.lineno,
        "start_column": node.col_offset,
        "end_line": node.end_lineno,
        "end_column": node.end_col_offset,
    }


def _region_identity(source_sha256, index, span):
    descriptor = "pysolate.semantic-candidate-region.v0\x00%s\x00%d\x00%d:%d:%d:%d" % (
        source_sha256, index, span["start_line"], span["start_column"],
        span["end_line"], span["end_column"],
    )
    return _digest(descriptor.encode("utf-8"))


def _derive(final_source, decision):
    if not isinstance(final_source, str) or len(final_source.encode("utf-8")) > MAX_SOURCE_BYTES:
        raise ValueError("invalid final source")
    try:
        tree = ast.parse(final_source, filename="<agent-run>", mode="exec")
    except (SyntaxError, ValueError, TypeError, MemoryError) as exc:
        raise ValueError("invalid final source") from exc
    if _uses_reserved_helper_binding(tree):
        raise ValueError("final source uses the reserved prepared region helper")
    span = decision["region_span"]
    matches = [(index, node) for index, node in enumerate(tree.body) if _span(node) == span]
    if len(matches) != 1:
        raise ValueError("prepared region is not one exact top-level statement")
    index, statement = matches[0]
    if (
        not isinstance(statement, ast.Assign)
        or len(statement.targets) != 1
        or not isinstance(statement.targets[0], ast.Name)
        or statement.targets[0].id != decision["output_name"]
        or statement.type_comment is not None
        or _region_identity(decision["source_sha256"], index, span) != decision["region_id"]
        or _digest(_source_slice(final_source, span)) != decision["region_source_sha256"]
    ):
        raise ValueError("prepared region does not match its admitted assignment")
    derived = copy.deepcopy(tree)
    replacement_statement = derived.body[index]
    if not isinstance(replacement_statement, ast.Assign):
        raise ValueError("derived prepared region lost assignment shape")
    call = ast.Call(
        func=ast.Name(id=HELPER_BINDING, ctx=ast.Load()),
        args=[ast.Constant(value=decision["identity_sha256"])],
        keywords=[],
    )
    replacement_statement.value = ast.copy_location(call, replacement_statement.value)
    derived = ast.fix_missing_locations(derived)
    binding = {
        "decision_sha256": decision["identity_sha256"],
        "final_source_sha256": _digest(final_source.encode("utf-8")),
        "final_ast_sha256": _ast_digest(tree),
        "derived_ast_sha256": _ast_digest(derived),
        "region_id": decision["region_id"],
        "region_span": span,
        "output_name": decision["output_name"],
    }
    return derived, binding


def emit_prepared_region_patch_binding(final_source, decision_json):
    decision = _decode_decision(decision_json)
    _, binding = _derive(final_source, decision)
    return binding


def emit_prepared_region_patch_request_json(request_json):
    request = _decode(request_json, _EMIT_REQUEST_KEYS)
    if not isinstance(request["decision"], str) or not isinstance(request["final_source"], str):
        raise ValueError("invalid prepared region patch request")
    binding = emit_prepared_region_patch_binding(request["final_source"], request["decision"])
    return _canonical(binding)


def derive_prepared_region_tree(final_source, decision_json, patch_json):
    decision = _decode_decision(decision_json)
    patch = _decode_patch(patch_json)
    derived, binding = _derive(final_source, decision)
    expected = {key: patch[key] for key in binding}
    if expected != binding or patch["decision_sha256"] != decision["identity_sha256"]:
        raise ValueError("prepared region patch does not match final source and decision")
    return derived, binding
