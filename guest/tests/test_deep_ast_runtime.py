import unittest
import ast

from agent_runtime import _ModuleAssignedNameCollector, _SOURCE_CONTRACT_INVALID, _SOURCE_CONTRACT_OK, _validate_unrestricted_source


class DeepASTRuntimeTests(unittest.TestCase):
    def test_frozen_512_add_chain_validates_and_compiles_unchanged_source(self):
        source = "seed = 1\nvalue = seed" + " + 1" * 512 + "\nresult = value\n"
        status, code = _validate_unrestricted_source(source)
        self.assertEqual(_SOURCE_CONTRACT_OK, status)
        self.assertIsNotNone(code)

    def test_iterative_binop_collection_preserves_nested_named_expression_store(self):
        tree = ast.parse("value = 1 + (nested := 2)\n")
        collector = _ModuleAssignedNameCollector()
        collector.visit(tree)
        self.assertIn("nested", collector.names)

    def test_source_beyond_the_bounded_ast_is_invalid_not_an_unhandled_recursion(self):
        source = "seed = 1\nvalue = seed" + " + 1" * 4096 + "\nresult = value\n"
        status, code = _validate_unrestricted_source(source)
        self.assertEqual(_SOURCE_CONTRACT_INVALID, status)
        self.assertIsNone(code)


if __name__ == "__main__":
    unittest.main()