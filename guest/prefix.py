"""Append-only source intake; only leading, complete, literal/input tool reads.

No eval/exec here. Final execution and Future ownership remain in the existing path.
"""
import ast
import json


class Prefix:
    def __init__(self, inputs, manifest, prepare):
        self.inputs = inputs
        self.manifest = manifest
        self.path_to_tool = {
            spec.get("python_path", spec["name"]): spec["name"]
            for spec in manifest if spec["allow_early_read"]
        }
        self.tool_roots = {path.split(".", 1)[0] for path in self.path_to_tool}
        self.prepare = prepare
        self.source = ""
        self.parsed = 0
        self.stopped = False
        self.ready = []
        self.claimed = 0

    def feed(self, chunk):
        self.source += chunk
        if self.stopped:
            return
        # A trailing partial line is not a complete received statement.
        end = self.source.rfind("\n") + 1
        complete = self.source[self.parsed:end]
        try:
            tree = ast.parse(complete, filename="<pysolate>")
        except SyntaxError:
            return  # Incomplete/invalid source is diagnosed at final compile.
        self.parsed = end
        for statement in tree.body:
            if not self.candidate(statement):
                self.stopped = True
                break  # Never pass a branch, dependency, mutation, import, or other code.
            call = statement.value
            try:
                args = {k.arg: self.argument(k.value) for k in call.keywords}
                request = json.dumps({"tool": self.tool_name(call), "args": args})
            except Exception:
                continue  # Argument errors are still raised at the actual Python call.
            handle = self.prepare(request)
            if handle:
                self.ready.append((request, handle))

    def candidate(self, statement):
        if not (isinstance(statement, ast.Assign) and len(statement.targets) == 1
                and isinstance(statement.targets[0], ast.Name)
                and statement.targets[0].id != "inputs"
                and statement.targets[0].id not in self.tool_roots):
            return False
        call = statement.value
        return (isinstance(call, ast.Call) and self.tool_name(call) is not None and not call.args
                and all(k.arg is not None and self.literal_or_input(k.value) for k in call.keywords))

    @staticmethod
    def function_path(node):
        if isinstance(node, ast.Name):
            return node.id
        if isinstance(node, ast.Attribute):
            prefix = Prefix.function_path(node.value)
            return prefix + "." + node.attr if prefix else None
        return None

    def tool_name(self, call):
        if not isinstance(call, ast.Call):
            return None
        return self.path_to_tool.get(self.function_path(call.func))

    @staticmethod
    def literal_or_input(node):
        if isinstance(node, ast.Constant):
            return isinstance(node.value, (str, int, float, bool, type(None)))
        return (isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name)
                and node.value.id == "inputs" and isinstance(node.slice, ast.Constant)
                and isinstance(node.slice.value, (str, int)))

    def argument(self, node):
        if isinstance(node, ast.Constant):
            return node.value
        return self.inputs[node.slice.value]

    def claim(self, request):
        # Leading calls execute in source order even if the full program is not optimized.
        # Match the actual request too; never search ahead or reuse a different read.
        if self.claimed < len(self.ready) and self.ready[self.claimed][0] == request:
            handle = self.ready[self.claimed][1]
            self.claimed += 1
            return handle
        return None
