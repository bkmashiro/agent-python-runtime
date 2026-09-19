import ast
import json
import unittest
from unittest.mock import patch
from prefix import Prefix

MANIFEST = [{'name': 'read', 'allow_early_read': True}]

class Reference(Prefix):
    def __init__(self, *args):
        super().__init__(*args)
        self.seen = 0
    def feed(self, chunk):
        self.source += chunk
        try:
            body = ast.parse(self.source[:self.source.rfind('\n') + 1]).body
        except SyntaxError:
            return
        for index, statement in enumerate(body):
            if not self.candidate(statement):
                break
            if index < self.seen:
                continue
            self.seen = index + 1
            call = statement.value
            try:
                args = {k.arg: self.argument(k.value) for k in call.keywords}
                request = json.dumps({'tool': call.func.id, 'args': args})
            except Exception:
                continue
            self.prepare(request)

class IncrementalPrefixTests(unittest.TestCase):
    def test_every_split_keeps_admission(self):
        sources = [
            'a = read(value=1)\nb = read(value=inputs["x"])\n',
            'a = read(\n value=1\n)\nb = read(value=2)\n',
            'a = read(value=1); b = read(value=2)\n',
            '# comment\n\na = read(value=1)\nresult = a\nb = read(value=2)\n',
            'a = read(value=inputs["missing"])\nb = read(value=2)\n',
            'a = read(value=1)\nif inputs["x"]:\n b = read(value=2)\n',
            'a = read(value=1)\nb = (\n',
            'a = read(value=1)\ninputs = {}\nb = read(value=2)\n',
            'a = read(value="""one\ntwo""")\nb = read(value=2)\n',
            'a = read(value=1)\r\nb = read(value=2)\r\n',
        ]
        for source in sources:
            for cut in range(len(source) + 1):
                actual, expected = [], []
                left = Prefix({'x': 2}, MANIFEST, lambda r: actual.append(r) or len(actual))
                right = Reference({'x': 2}, MANIFEST, lambda r: expected.append(r) or len(expected))
                for chunk in (source[:cut], source[cut:]):
                    left.feed(chunk); right.feed(chunk)
                    self.assertEqual(actual, expected, (source, cut))
                self.assertEqual(left.source, source)

    def test_complete_statements_are_parsed_once(self):
        chunks = [f'x{i} = read(value={i})\n' for i in range(128)]
        prefix = Prefix({}, MANIFEST, lambda request: 1)
        parsed = []
        original = ast.parse
        def record(source, *args, **kwargs):
            parsed.append(len(source))
            return original(source, *args, **kwargs)
        with patch('prefix.ast.parse', record):
            for chunk in chunks:
                prefix.feed(chunk)
        self.assertEqual(sum(parsed), sum(map(len, chunks)))
        self.assertEqual(len(prefix.ready), 128)
