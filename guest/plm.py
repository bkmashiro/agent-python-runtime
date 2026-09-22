"""Small whole-program early-read pass. Unsupported programs keep their original AST.

Only adjacent tool assignments are reordered: prepare after argument definitions,
resolve at the original statement. Other statements and control regions are barriers.
"""
import ast


def transform(source, manifest):
    tree = ast.parse(source, filename="<pysolate>")
    path_to_tool = {spec.get("python_path", spec["name"]): spec["name"] for spec in manifest}
    early_names = {spec["name"] for spec in manifest if spec["allow_early_read"]}
    tool_roots = {path.split(".", 1)[0] for path in path_to_tool}
    names = {n.id for n in ast.walk(tree) if isinstance(n, ast.Name)}
    names |= {n.name for n in ast.walk(tree) if isinstance(n, ast.ExceptHandler) and n.name}
    data_names = {"inputs"} | {n.id for n in ast.walk(tree) if isinstance(n, ast.Name) and isinstance(n.ctx, ast.Store)}

    def data(node):
        if isinstance(node, ast.Constant):
            return isinstance(node.value, (str, int, float, bool, type(None)))
        if isinstance(node, ast.Name):
            return node.id in data_names
        if isinstance(node, (ast.List, ast.Tuple)):
            return all(data(n) for n in node.elts)
        if isinstance(node, ast.Dict):
            return all(k is not None and data(k) and data(v) for k, v in zip(node.keys, node.values))
        if isinstance(node, ast.Subscript):
            return data(node.value) and data(node.slice)
        if isinstance(node, ast.BinOp):
            return data(node.left) and data(node.right)
        if isinstance(node, ast.UnaryOp):
            return data(node.operand)
        if isinstance(node, ast.BoolOp):
            return all(data(n) for n in node.values)
        if isinstance(node, ast.Compare):
            return data(node.left) and all(data(n) for n in node.comparators)
        return False

    def argument(node):
        if isinstance(node, (ast.Constant, ast.Name)):
            return data(node)
        return (isinstance(node, ast.Subscript) and argument(node.value)
                and isinstance(node.slice, ast.Constant) and isinstance(node.slice.value, (str, int)))

    def function_path(node):
        if isinstance(node, ast.Name):
            return node.id
        if isinstance(node, ast.Attribute):
            prefix = function_path(node.value)
            return prefix + "." + node.attr if prefix else None
        return None

    def tool_name(node):
        if not isinstance(node, ast.Call):
            return None
        return path_to_tool.get(function_path(node.func))

    def tool_call(node):
        return (tool_name(node) is not None and not node.args
                and all(k.arg is not None and argument(k.value) for k in node.keywords))

    def early_call(node):
        return tool_call(node) and tool_name(node) in early_names

    def block_ok(body):
        for s in body:
            if isinstance(s, ast.Assign):
                if (len(s.targets) != 1 or not isinstance(s.targets[0], ast.Name)
                        or s.targets[0].id == "inputs" or s.targets[0].id in tool_roots
                        or not (tool_call(s.value) or data(s.value))):
                    return False
            elif isinstance(s, ast.If):
                if not (data(s.test) and block_ok(s.body) and block_ok(s.orelse)):
                    return False
            elif isinstance(s, ast.Try):
                if not all(block_ok(b) for b in (s.body, s.orelse, s.finalbody)):
                    return False
                for h in s.handlers:
                    if h.name == "inputs" or h.name in tool_roots or not block_ok(h.body):
                        return False
                    if h.type is not None and not isinstance(h.type, ast.Name):
                        return False
            elif not (isinstance(s, ast.Pass) or isinstance(s, ast.Expr) and isinstance(s.value, ast.Constant)):
                return False
        return True

    # No arbitrary calls, alias mutation, loops or user objects in an optimized program.
    if not block_ok(tree.body):
        return tree, None

    def fresh(base):
        while base in names:
            base += "_"
        names.add(base)
        return base

    prepare, resolve = fresh("_pysolate_prepare"), fresh("_pysolate_resolve")
    changed = False

    def rewrite(body):
        nonlocal changed
        result, i = [], 0
        while i < len(body):
            s = body[i]
            if isinstance(s, ast.Assign) and early_call(s.value):
                group = []
                while i < len(body) and isinstance(body[i], ast.Assign) and early_call(body[i].value):
                    group.append(body[i])
                    i += 1
                before = [[] for _ in group]
                definitions = {}
                replacements = []
                for index, statement in enumerate(group):
                    call = statement.value
                    loaded = {n.id for k in call.keywords for n in ast.walk(k.value) if isinstance(n, ast.Name)}
                    start = 1 + max((definitions.get(n, -1) for n in loaded), default=-1)
                    slot = fresh("_pysolate_future")
                    args = ast.Dict(keys=[ast.Constant(k.arg) for k in call.keywords], values=[k.value for k in call.keywords])
                    canonical = tool_name(call)
                    canonical_node = ast.Constant(canonical)
                    thunk = ast.Lambda(args=ast.arguments(posonlyargs=[], args=[], kwonlyargs=[], kw_defaults=[], defaults=[]),
                                       body=ast.Tuple(elts=[canonical_node, args], ctx=ast.Load()))
                    early = ast.Assign(targets=[ast.Name(slot, ast.Store())],
                                       value=ast.Call(ast.Name(prepare, ast.Load()), [thunk], []))
                    before[start].append(ast.copy_location(early, statement))
                    statement.value = ast.copy_location(ast.Call(ast.Name(resolve, ast.Load()),
                        [ast.Name(slot, ast.Load()), ast.Constant(canonical)], call.keywords), call)
                    replacements.append(statement)
                    definitions[statement.targets[0].id] = index
                for early, statement in zip(before, replacements):
                    result.extend(early)
                    result.append(statement)
                changed = True
                continue
            if isinstance(s, ast.If):
                s.body, s.orelse = rewrite(s.body), rewrite(s.orelse)
            elif isinstance(s, ast.Try):
                s.body, s.orelse, s.finalbody = rewrite(s.body), rewrite(s.orelse), rewrite(s.finalbody)
                for h in s.handlers:
                    h.body = rewrite(h.body)
            result.append(s)
            i += 1
        return result

    tree.body = rewrite(tree.body)
    return ast.fix_missing_locations(tree), (prepare, resolve) if changed else None
