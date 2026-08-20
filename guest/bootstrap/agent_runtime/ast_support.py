"""Bounded iterative helpers for deep but finite Python ASTs."""

import ast
import hashlib
from typing import TypeVar


MAX_AST_NODES = 8192
MAX_AST_DEPTH = 1024
MAX_RECURSIVE_VISITOR_DEPTH = 256
_AST_DIGEST_DOMAIN = b"pysolate.ast-structure.v1\x00"
_AST = TypeVar("_AST", bound=ast.AST)


def _token(hasher, tag, value):
    encoded = value.encode("utf-8")
    hasher.update(tag)
    hasher.update(str(len(encoded)).encode("ascii"))
    hasher.update(b":")
    hasher.update(encoded)
    hasher.update(b";")


def _walk_ast_with_depth(root, max_nodes=MAX_AST_NODES, max_depth=MAX_AST_DEPTH):
    if not isinstance(root, ast.AST) or type(max_nodes) is not int or max_nodes <= 0 or type(max_depth) is not int or max_depth <= 0:
        raise ValueError("invalid AST traversal")
    stack = [(iter((root,)), 1)]
    seen = 0
    while stack:
        children, depth = stack[-1]
        try:
            node = next(children)
        except StopIteration:
            stack.pop()
            continue
        seen += 1
        if seen > max_nodes:
            raise ValueError("AST node bound exceeded")
        if depth > max_depth:
            raise ValueError("AST depth bound exceeded")
        yield node, depth
        stack.append((ast.iter_child_nodes(node), depth + 1))


def walk_ast_bounded(root, max_nodes=MAX_AST_NODES, max_depth=MAX_AST_DEPTH):
    """Yield AST nodes depth-first with iterator frames bounded by AST depth."""
    for node, _ in _walk_ast_with_depth(root, max_nodes=max_nodes, max_depth=max_depth):
        yield node


def validate_ast_recursion_shape_bounded(root, max_recursive_depth=MAX_RECURSIVE_VISITOR_DEPTH):
    """Reject deep shapes unless they are the narrow iterative scalar BinOp lane."""
    deep_scalar_nodes = (ast.BinOp, ast.operator, ast.Name, ast.Constant, ast.expr_context)
    for node, depth in _walk_ast_with_depth(root):
        if depth > max_recursive_depth and not isinstance(node, deep_scalar_nodes):
            raise ValueError("AST recursive visitor depth bound exceeded")


def _structural_ast_digest(root, max_nodes):
    hasher = hashlib.sha256()
    hasher.update(_AST_DIGEST_DOMAIN)
    stack = [("value", root)]
    nodes = 0
    while stack:
        kind, value = stack.pop()
        if kind == "token":
            hasher.update(value)
            continue
        if kind == "field":
            _token(hasher, b"F", value)
            continue
        if kind == "fields":
            try:
                name, field_value = next(value)
            except StopIteration:
                continue
            stack.append(("fields", value))
            stack.append(("value", field_value))
            stack.append(("field", name))
            continue
        if kind == "items":
            try:
                item = next(value)
            except StopIteration:
                continue
            stack.append(("items", value))
            stack.append(("value", item))
            continue
        if isinstance(value, ast.AST):
            nodes += 1
            if nodes > max_nodes:
                raise ValueError("AST node bound exceeded")
            _token(hasher, b"N", type(value).__name__)
            hasher.update(b"(")
            stack.append(("token", b")"))
            stack.append(("fields", iter(ast.iter_fields(value))))
        elif isinstance(value, list):
            hasher.update(b"L" + str(len(value)).encode("ascii") + b"[")
            stack.append(("token", b"]"))
            stack.append(("items", iter(value)))
        elif isinstance(value, tuple):
            hasher.update(b"T" + str(len(value)).encode("ascii") + b"[")
            stack.append(("token", b"]"))
            stack.append(("items", iter(value)))
        else:
            _token(hasher, b"P", type(value).__name__ + ":" + repr(value))
    return "sha256:" + hasher.hexdigest()


def ast_digest_bounded(root, max_nodes=MAX_AST_NODES, max_depth=MAX_AST_DEPTH):
    """Return one platform-independent bounded structural AST identity."""
    for _ in walk_ast_bounded(root, max_nodes=max_nodes, max_depth=max_depth):
        pass
    return _structural_ast_digest(root, max_nodes)


def fix_missing_locations_bounded(root: _AST, max_nodes=MAX_AST_NODES, max_depth=MAX_AST_DEPTH) -> _AST:
    """Fill compiler-required locations with iterator frames bounded by AST depth."""
    if not isinstance(root, ast.AST):
        raise ValueError("invalid AST location root")
    stack: list = [("node", (root, 1, 1, 0, 1, 0))]
    seen = 0
    while stack:
        kind, state = stack.pop()
        if kind == "children":
            children, depth, line, column, end_line, end_column = state
            try:
                child = next(children)
            except StopIteration:
                continue
            stack.append(("children", state))
            stack.append(("node", (child, depth, line, column, end_line, end_column)))
            continue
        node, depth, parent_line, parent_column, parent_end_line, parent_end_column = state
        seen += 1
        if seen > max_nodes:
            raise ValueError("AST node bound exceeded")
        if depth > max_depth:
            raise ValueError("AST depth bound exceeded")
        attributes = getattr(node, "_attributes", ())
        if "lineno" in attributes and not hasattr(node, "lineno"):
            setattr(node, "lineno", parent_line)
        if "col_offset" in attributes and not hasattr(node, "col_offset"):
            setattr(node, "col_offset", parent_column)
        if "end_lineno" in attributes and getattr(node, "end_lineno", None) is None:
            setattr(node, "end_lineno", parent_end_line)
        if "end_col_offset" in attributes and getattr(node, "end_col_offset", None) is None:
            setattr(node, "end_col_offset", parent_end_column)
        line = getattr(node, "lineno", parent_line)
        column = getattr(node, "col_offset", parent_column)
        end_line = getattr(node, "end_lineno", None) or line
        end_column = getattr(node, "end_col_offset", None)
        if end_column is None:
            end_column = column
        stack.append(("children", (ast.iter_child_nodes(node), depth + 1, line, column, end_line, end_column)))
    return root
