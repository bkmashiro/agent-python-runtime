import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
WRAPPER = ROOT / "scripts" / "build-guest-workstation.sh"
WORKER = ROOT / "scripts" / "internal" / "build-guest-workstation-worker.sh"
BUILD = ROOT / "guest" / "build" / "build-guest.sh"


class WorkstationScriptContractTests(unittest.TestCase):
    def test_staging_is_fresh_and_worker_paths_are_canonical(self) -> None:
        wrapper = WRAPPER.read_text()
        worker = WORKER.read_text()
        self.assertIn('stage=$(ssh shell2 mktemp -d', wrapper)
        self.assertIn('remote_output=$(ssh shell2 mktemp -d', wrapper)
        self.assertNotIn('ssh shell2 mkdir -p "$stage" "$remote_output"', wrapper)
        self.assertIn('stage_real=$(realpath -e "$stage")', worker)
        self.assertIn('output_real=$(realpath -e "$output")', worker)
        self.assertIn('$stage_real != "$stage"', worker)
        self.assertIn('$output_real != "$output"', worker)

    def test_final_cache_binds_commit_and_default_probe_binary(self) -> None:
        build = BUILD.read_text()
        self.assertIn('--source-commit "${GITHUB_SHA}"', build)
        self.assertIn('--probe-runner-sha256 "${PROBE_RUNNER_SHA256}"', build)
        self.assertIn('go build -trimpath -buildvcs=false', build)
        self.assertIn('FINAL_CACHE_ELIGIBLE=0', build)
        self.assertIn('cache_entry_is_regular "${BUILD_CACHE_ENTRY}"', build)
        self.assertIn('cache_entry_is_regular "${FINAL_CACHE_ENTRY}"', build)


if __name__ == "__main__":
    unittest.main()
