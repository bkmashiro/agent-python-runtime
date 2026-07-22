import importlib.util
import pathlib
import unittest
from unittest import mock


ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "source_date_epoch", ROOT / "tools" / "source_date_epoch.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class SourceDateEpochTests(unittest.TestCase):
    @mock.patch.object(MODULE.subprocess, "check_output", return_value="1784729528\n")
    def test_returns_exact_commit_timestamp(self, check_output):
        self.assertEqual(MODULE.resolve(ROOT, "a" * 40), "1784729528")
        check_output.assert_called_once_with(
            ["git", "-C", str(ROOT), "show", "-s", "--format=%ct", "a" * 40],
            text=True,
        )

    @mock.patch.object(MODULE.subprocess, "check_output", return_value="unknown\n")
    def test_rejects_non_numeric_epoch(self, _check_output):
        with self.assertRaisesRegex(ValueError, "SOURCE_DATE_EPOCH"):
            MODULE.resolve(ROOT, "HEAD")


if __name__ == "__main__":
    unittest.main()
