import importlib.util
import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "eval" / "agentic" / "scripts" / "digest_provider_catalog.py"


def load_module():
    spec = importlib.util.spec_from_file_location("digest_provider_catalog", SCRIPT)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


MODULE = load_module()


class ProviderCatalogDigestTests(unittest.TestCase):
    def test_order_and_whitespace_do_not_change_digest_input(self):
        first = json.dumps({"object": "list", "data": [
            {"id": "z-model", "ratio": 2},
            {"id": "gpt-5.4", "ratio": 1.25},
        ]}).encode()
        second = b'{"data":[{"ratio":1.25,"id":"gpt-5.4"},{"ratio":2,"id":"z-model"}],"object":"list"}'
        left, left_count = MODULE.canonical_catalog(first)
        right, right_count = MODULE.canonical_catalog(second)
        self.assertEqual(left, right)
        self.assertEqual((left_count, right_count), (2, 2))

    def test_rejects_duplicate_sensitive_and_missing_target(self):
        invalid = [
            b'{"data":[{"id":"gpt-5.4","id":"other"}]}',
            b'{"data":[{"id":"gpt-5.4","api_key":"secret"}]}',
            b'{"data":[{"id":"other"}]}',
        ]
        for raw in invalid:
            with self.subTest(raw=raw):
                with self.assertRaises(MODULE.CatalogError):
                    MODULE.canonical_catalog(raw)


if __name__ == "__main__":
    unittest.main()
