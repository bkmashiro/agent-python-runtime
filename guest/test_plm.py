"""Only tests the AST pass here; execution acceptance also uses real Wasm."""
import ast
import unittest
from plm import transform


class PassTests(unittest.TestCase):
    def execute(self, source, inputs=None):
        events = []
        values = {"book": 21, "shipping": 5}
        manifest = [{"name": "lookup", "allow_early_read": True}]
        def lookup(**args):
            events.append(("call", args["key"]))
            return values[args["key"]]
        def prepare(thunk):
            try:
                name, args = thunk()
            except Exception:
                return None
            events.append(("prepare", args["key"]))
            return name, args
        def resolve(token, name, **args):
            events.append(("resolve", args["key"]))
            return values[args["key"]]
        tree, helpers = transform(source, manifest)
        scope = {"inputs": inputs or {}, "lookup": lookup}
        if helpers:
            scope.update(zip(helpers, (prepare, resolve)))
        exec(compile(tree, "<test>", "exec"), scope)
        return scope, events, ast.unparse(tree), helpers

    def test_independent_calls_start_first(self):
        scope, events, _, _ = self.execute('a = lookup(key="book")\nb = lookup(key="shipping")\nresult = a+b')
        self.assertEqual(events, [("prepare", "book"), ("prepare", "shipping"), ("resolve", "book"), ("resolve", "shipping")])
        self.assertEqual(scope["result"], 26)

    def test_dependency_stays_after_definition(self):
        scope, events, _, _ = self.execute('key = "book"\na = lookup(key=key)\nb = lookup(key="shipping")')
        self.assertEqual(events[0], ("prepare", "book"))
        tree, _ = transform('a = lookup(key="book")\nb = lookup(key=a)', [{"name": "lookup", "allow_early_read": True}])
        self.assertEqual(tree.body[1].targets[0].id, "a")
        self.assertEqual(tree.body[2].value.args[0].body.elts[1].values[0].id, "a")

    def test_branch_not_hoisted(self):
        _, events, _, _ = self.execute('if inputs["yes"]:\n a = lookup(key="book")\nelse:\n b = lookup(key="shipping")', {"yes": False})
        self.assertEqual(events, [("prepare", "shipping"), ("resolve", "shipping")])

    def test_missing_argument_not_raised_before_first_call(self):
        _, events, _, _ = self.execute('try:\n a = lookup(key="book")\n b = lookup(key=inputs["missing"])\nexcept KeyError:\n result = a')
        self.assertEqual(events, [("prepare", "book"), ("resolve", "book")])

    def test_unsupported_is_unchanged(self):
        for source in ['for x in []:\n a=lookup(key=x)', 'f = lambda: 1\nresult=f()', 'inputs["x"]=1', 'lookup = 1', 'import os\nresult=1']:
            tree, helpers = transform(source, [{"name": "lookup", "allow_early_read": True}])
            self.assertIsNone(helpers)
            self.assertEqual(ast.dump(tree), ast.dump(ast.parse(source)))

    def test_no_helper_name_collision(self):
        scope, _, _, helpers = self.execute('_pysolate_prepare = 123\na = lookup(key="book")\nresult = _pysolate_prepare')
        self.assertEqual(scope["result"], 123)
        self.assertNotEqual(helpers[0], '_pysolate_prepare')


if __name__ == "__main__":
    unittest.main()
