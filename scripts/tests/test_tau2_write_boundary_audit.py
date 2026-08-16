import importlib.util
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-write-boundary-audit.py"
SPEC = importlib.util.spec_from_file_location("tau2_write_boundary_audit", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
audit = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(audit)


class Tau2WriteBoundaryAuditTests(unittest.TestCase):
    def checks(self) -> dict[str, object]:
        return {
            "upstream_world_is_fresh_per_constructor": True,
            "reference_is_a_real_write": True,
            "official_oracle_uses_independent_fresh_worlds": True,
            "attempt_world_bound_to_workspace_disposition": True,
            "existing_effect_contract_matches_handler_behavior": True,
            "existing_adapter_has_attempt_persistent_write_state": True,
            "final_state_disposition_join_exists": True,
            "cancel_or_failure_discards_attempt_world": True,
        }

    def test_all_checks_are_required_for_discussion(self):
        self.assertEqual(audit.decide(self.checks()), "qualified_for_discussion")
        for key in self.checks():
            checks = self.checks()
            checks[key] = False
            self.assertEqual(audit.decide(checks), "unsupported_effect_class", key)

    def test_rejects_missing_or_non_boolean_checks(self):
        checks = self.checks()
        checks.pop("final_state_disposition_join_exists")
        with self.assertRaises(ValueError):
            audit.decide(checks)
        checks = self.checks()
        checks["final_state_disposition_join_exists"] = 1
        with self.assertRaises(ValueError):
            audit.decide(checks)


if __name__ == "__main__":
    unittest.main()
