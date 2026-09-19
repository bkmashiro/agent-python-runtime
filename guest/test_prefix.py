import json
import unittest
from prefix import Prefix


MANIFEST = [{"name": "lookup", "allow_early_read": True}]


class PrefixTests(unittest.TestCase):
    def make(self, inputs=None):
        calls = []
        def prepare(request):
            calls.append(request)
            return len(calls)
        return Prefix(inputs, MANIFEST, prepare), calls

    def test_split_statement_and_no_duplicate_prepare(self):
        p, calls = self.make({"item": "book"})
        p.feed("a=lookup(key=inputs['item']")
        self.assertEqual(calls, [])
        p.feed(")\n")
        p.feed("# another chunk\n")
        self.assertEqual(len(calls), 1)
        self.assertEqual(p.claim(calls[0]), 1)
        self.assertIsNone(p.claim(calls[0]))

    def test_no_branch_or_arbitrary_expression(self):
        for source in ("if True:\n a=lookup(key='book')\n",
                       "x='book'\na=lookup(key=x)\n",
                       "a=lookup(key=compute())\n",
                       "lookup=lookup(key='book')\n"):
            p, calls = self.make()
            p.feed(source)
            self.assertEqual(calls, [])

    def test_errors_are_not_raised_during_feed(self):
        p, calls = self.make({})
        p.feed("a=lookup(key=inputs['absent'])\n")
        self.assertEqual(calls, [])

    def test_match_does_not_search_ahead(self):
        p, calls = self.make()
        p.feed("a=lookup(key='book')\nb=lookup(key='shipping')\n")
        self.assertIsNone(p.claim(calls[1]))
        self.assertEqual(p.claim(calls[0]), 1)
        self.assertEqual(p.claim(calls[1]), 2)

    def test_no_handle_does_not_keep_request(self):
        p = Prefix(None, MANIFEST, lambda request: 0)
        p.feed("a=lookup(key='book')\n")
        self.assertEqual(p.ready, [])

    def test_non_early_tool_stops_prefix(self):
        manifest = MANIFEST + [{"name": "buy", "allow_early_read": False}]
        p = Prefix(None, manifest, lambda request: 1)
        p.feed("order=buy(symbol='ACME')\nprice=lookup(key='book')\n")
        self.assertEqual(p.ready, [])


if __name__ == "__main__":
    unittest.main()
