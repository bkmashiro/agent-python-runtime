import copy
import importlib.util
from pathlib import Path
import unittest
from typing import Any

spec = importlib.util.spec_from_file_location('agent_eval_summary', Path(__file__).with_name('summarize-agent-eval.py'))
assert spec and spec.loader
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class EvaluationSummaryTests(unittest.TestCase):
    def row(self) -> dict[str, Any]:
        return dict(task='lookup', arm='direct', repeat=1,
                    completion_status='completed', correctness=True,
                    answer={'price_cents': 3199}, trace=[{'tool':'submit_answer','arguments':'{"price_cents":3199}'}], model_requests=2,
                    domain_tool_calls=1, usage_complete=True,
                    provider_usage={'total_tokens': 100}, total_ns=1000000000)

    def test_regrades_actual_answer(self):
        row = self.row()
        self.assertEqual(module.summarize([row], 1)['groups'][0]['correct'], 1)
        row['answer']['price_cents'] = 0
        row['trace'][0]['arguments'] = '{"price_cents":0}'
        with self.assertRaisesRegex(ValueError, 'oracle disagreement'):
            module.summarize([row], 1)
        row.update(completion_status='wrong_answer', correctness=False)
        self.assertEqual(module.summarize([row], 1)['groups'][0]['correct'], 0)

    def test_counts_duplicates_and_missing_usage(self):
        row = self.row()
        with self.assertRaises(ValueError):
            module.summarize([row], 16)
        with self.assertRaises(ValueError):
            module.summarize([row, copy.deepcopy(row)], 2)
        row.update(usage_complete=False, provider_usage=None)
        self.assertIsNone(module.summarize([row], 1)['groups'][0]['median_tokens_per_attempt'])
