import importlib.util
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "verify-guest-cache-pair.py"
SPEC = importlib.util.spec_from_file_location("guest_cache_pair_verifier", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
pair = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(pair)


class GuestCachePairVerifierTests(unittest.TestCase):
    def reports(self) -> tuple[dict[str, object], dict[str, object]]:
        common: dict[str, object] = {
            "source_commit": "a" * 40,
            "source_tree": "b" * 40,
            "artifact_sha256": "sha256:" + "c" * 64,
            "cache_key": "sha256:" + "d" * 64,
            "cache_layer_sha256": "sha256:" + "e" * 64,
            "final_cache_key": "sha256:" + "f" * 64,
        }
        cold: dict[str, object] = dict(
            common, requested_cache_mode="refresh", cache_disposition="miss", final_cache_disposition="miss", build_millis=400
        )
        warm: dict[str, object] = dict(
            common, requested_cache_mode="auto", cache_disposition="hit", final_cache_disposition="hit", build_millis=100
        )
        return cold, warm

    def test_accepts_identical_faster_pair(self) -> None:
        cold, warm = self.reports()
        report = pair.verify_pair_reports(cold, warm)
        self.assertEqual(4.0, report["speedup"])

    def test_rejects_identity_artifact_disposition_and_speed_drift(self) -> None:
        mutations = (
            ("artifact_sha256", "sha256:" + "f" * 64),
            ("cache_key", "sha256:" + "f" * 64),
            ("cache_disposition", "miss"),
            ("build_millis", 350),
        )
        for field, value in mutations:
            with self.subTest(field=field):
                cold, warm = self.reports()
                warm[field] = value
                with self.assertRaises(ValueError):
                    pair.verify_pair_reports(cold, warm)


if __name__ == "__main__":
    unittest.main()
