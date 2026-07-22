import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]


class GuestStageEvidenceWiringTests(unittest.TestCase):
    def test_build_script_emits_opt_in_stage_evidence(self):
        script = (ROOT / "guest" / "build" / "build-guest.sh").read_text()
        self.assertIn('if [[ -n ${AGENT_RUNTIME_REPRO_EVIDENCE_DIR:-} ]]; then', script)
        self.assertIn('tools/write_guest_stage_evidence.py" manifest', script)
        self.assertIn('tools/write_guest_stage_evidence.py" evidence', script)
        self.assertLess(
            script.index('tools/write_guest_stage_evidence.py" manifest'),
            script.index('"${WASI_VFS}" pack'),
        )
        for argument in (
            '--raw-wasm "${RAW_GUEST}"',
            '--final-wasm "${FINAL_GUEST}"',
            '--patched-wasi-vfs-archive "${WASI_VFS_LIB}"',
            '--linked-storage-object "${WASI_VFS_STORAGE_OBJECT}"',
            '--wasi-vfs-cli "${WASI_VFS}"',
            '--vfs-root "${VFS_PYTHON_DIR}"',
            '--vfs-manifest "${REPRO_VFS_MANIFEST}"',
        ):
            self.assertIn(argument, script)

    def test_workflow_uploads_and_compares_stage_evidence(self):
        workflow = (ROOT / ".github" / "workflows" / "reproducibility.yml").read_text()
        self.assertIn("python3 -m unittest discover -s tests -v", workflow)
        self.assertIn("AGENT_RUNTIME_REPRO_EVIDENCE_DIR: ${{ runner.temp }}/agent-runtime-repro-evidence", workflow)
        self.assertIn("AGENT_RUNTIME_REPRO_REPLICA: ${{ matrix.replica }}", workflow)
        self.assertIn("name: reproducibility-stage-evidence-${{ matrix.replica }}-${{ github.sha }}", workflow)
        self.assertIn("tools/compare_guest_stage_evidence.py", workflow)
        self.assertIn("name: reproducibility-stage-report-${{ github.sha }}", workflow)


if __name__ == "__main__":
    unittest.main()
