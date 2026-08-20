import ast
import unittest

from agent_runtime.ast_support import MAX_AST_DEPTH, MAX_AST_NODES, ast_digest_bounded, fix_missing_locations_bounded, walk_ast_bounded


class BoundedASTSupportTests(unittest.TestCase):
    def test_structural_digest_is_deterministic_and_shape_sensitive(self):
        first = ast.parse("value = seed + 1\n")
        repeated = ast.parse("value = seed + 1\n")
        changed = ast.parse("value = seed + 2\n")
        self.assertEqual(ast_digest_bounded(first), ast_digest_bounded(repeated))
        self.assertNotEqual(ast_digest_bounded(first), ast_digest_bounded(changed))

    def test_deep_locations_are_filled_without_recursion(self):
        tree = ast.parse("value = seed" + " + 1" * 512 + "\n")
        replacement = ast.Call(func=ast.Name(id="helper", ctx=ast.Load()), args=[], keywords=[])
        tree.body[0].value = replacement
        self.assertIs(tree, fix_missing_locations_bounded(tree))
        compile(tree, "<bounded-ast-test>", "exec")

    def test_node_bound_fails_closed(self):
        tree = ast.Module(body=[ast.Pass() for _ in range(MAX_AST_NODES)], type_ignores=[])
        with self.assertRaisesRegex(ValueError, "AST node bound exceeded"):
            list(walk_ast_bounded(tree))
        with self.assertRaisesRegex(ValueError, "AST node bound exceeded"):
            ast_digest_bounded(tree)

    def test_depth_bound_fails_closed(self):
        tree = ast.parse("value = seed" + " + 1" * 32 + "\n")
        with self.assertRaisesRegex(ValueError, "AST depth bound exceeded"):
            list(walk_ast_bounded(tree, max_depth=16))
        with self.assertRaisesRegex(ValueError, "AST depth bound exceeded"):
            ast_digest_bounded(tree, max_depth=16)
        with self.assertRaisesRegex(ValueError, "AST depth bound exceeded"):
            fix_missing_locations_bounded(tree, max_depth=16)
        self.assertGreater(MAX_AST_DEPTH, 512)


if __name__ == "__main__":
    unittest.main()
