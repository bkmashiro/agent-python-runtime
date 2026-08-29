import importlib.util
import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
LAUNCHER = ROOT / "scripts/run-workstation-evaluation-sweeps.py"


def load_launcher():
    spec = importlib.util.spec_from_file_location("run_workstation_evaluation_sweeps", LAUNCHER)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class EvaluationSweepContractTests(unittest.TestCase):
    def test_linux_runner_binds_source_artifacts_host_and_order(self):
        text = (ROOT / "scripts/run-linux-evaluation-sweeps.sh").read_text()
        for token in (
            "verify-source-identity.sh",
            "AGENT_RUNTIME_ARTIFACT_PROFILE=base",
            "AGENT_RUNTIME_ARTIFACT_PROFILE=numpy-core",
            'scripts/plm-crossover-economics-gate.sh "$output/plm-crossover.json"',
            'scripts/cow-fanout-economics-gate.sh "$output/cow-fanout.json"',
            "EVALUATION_HOST_ID",
            "EVALUATION_ORDER_OFFSET",
            "PLM_CROSSOVER_RUNS",
            "COW_FANOUT_RUNS",
            "base.wasm",
            "numpy-core.wasm",
            "SHA256SUMS",
        ):
            self.assertIn(token, text)

    def test_long_sweeps_have_explicit_bounded_go_timeouts(self):
        plm_gate = (ROOT / "scripts/plm-crossover-economics-gate.sh").read_text()
        cow_gate = (ROOT / "scripts/cow-fanout-economics-gate.sh").read_text()
        base_gate = (ROOT / "scripts/prepared-family-economics-gate.sh").read_text()
        self.assertIn("PLM_CROSSOVER_GO_TEST_TIMEOUT", plm_gate)
        self.assertIn("COW_FANOUT_GO_TEST_TIMEOUT", cow_gate)
        self.assertIn("PYSOLATE_PREPARED_FAMILY_GO_TEST_TIMEOUT", base_gate)

    def test_launcher_probes_all_hosts_before_parallel_runs(self):
        text = (ROOT / "scripts/run-workstation-evaluation-sweeps.py").read_text()
        for host in ("gpu31", "gpu32", "gpu33", "gpu34", "gpu35"):
            self.assertIn(host, text)
        for token in ("normalized_load", "MemAvailable", "free_disk", "selected_hosts", "ThreadPoolExecutor", "probe-manifest.json"):
            self.assertIn(token, text)
        self.assertIn("smallest_even_at_least", text)
        self.assertIn("ceil_div", text)
        self.assertIn("--suite", text)
        self.assertIn("evaluation-sweeps", text)

    def test_launcher_deduplicates_aliases_of_one_physical_host(self):
        module = load_launcher()
        probes = [
            {
                "host_id": "gpu31",
                "eligible": True,
                "machine_identity_sha256": "machine",
                "boot_identity_sha256": "boot",
            },
            {
                "host_id": "gpu32",
                "eligible": True,
                "machine_identity_sha256": "machine",
                "boot_identity_sha256": "boot",
            },
        ]
        self.assertEqual(["gpu31"], module.select_eligible_hosts(probes))
        self.assertEqual("gpu31", probes[1]["alias_of"])

    def test_host_target_and_worker_hostname_are_allowlisted(self):
        wrapper = (ROOT / "scripts/test-host-workstation.sh").read_text()
        worker = (ROOT / "scripts/internal/test-host-workstation-worker.sh").read_text()
        verifier = (ROOT / "scripts/verify-workstation-host-test.py").read_text()
        self.assertIn("gpu31|gpu32|gpu33|gpu34|gpu35", wrapper)
        self.assertIn('case "$suite" in', wrapper)
        self.assertIn("evaluation-sweeps", wrapper)
        self.assertIn("expected_hostname", worker)
        self.assertIn("hostname", worker)
        self.assertIn("evaluation-sweeps", worker)
        self.assertIn("gpu31", verifier)
        self.assertIn("gpu35", verifier)

    def test_merge_is_a_separate_source_bound_stage(self):
        text = (ROOT / "scripts/merge-linux-evaluation-sweeps.py").read_text()
        self.assertIn("selected_hosts", text)
        self.assertIn("host_blocks", text)
        self.assertIn("artifact drift", text)
        self.assertIn("schema drift", text)
        self.assertIn("config drift", text)


if __name__ == "__main__":
    unittest.main()
