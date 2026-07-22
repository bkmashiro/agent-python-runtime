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
            script.index('pack_guest "${FINAL_GUEST}"'),
        )
        self.assertIn('pack_guest "${FINAL_GUEST}"', script)
        self.assertIn('pack_guest "${REPEAT_PACKED_GUEST}"', script)
        self.assertIn('"${WASM_TOOLS}" validate "${REPEAT_PACKED_GUEST}"', script)
        for argument in (
            '--raw-wasm "${RAW_GUEST}"',
            '--final-wasm "${FINAL_GUEST}"',
            '--repeat-packed-wasm "${REPEAT_PACKED_GUEST}"',
            '--patched-wasi-vfs-archive "${WASI_VFS_LIB}"',
            '--linked-storage-object "${WASI_VFS_STORAGE_OBJECT}"',
            '--wasi-vfs-cli "${WASI_VFS}"',
            '--vfs-root "${VFS_PYTHON_DIR}"',
            '--vfs-manifest "${REPRO_VFS_MANIFEST}"',
        ):
            self.assertIn(argument, script)

    def test_experiment_rebuilds_guest_archive_with_explicit_treatments(self):
        script = (ROOT / "guest" / "build" / "build-guest.sh").read_text()
        workflow = (ROOT / ".github" / "workflows" / "reproducibility.yml").read_text()
        self.assertIn('AGENT_RUNTIME_REPRO_DETERMINISTIC_HASHER: "1"', workflow)
        self.assertIn('AGENT_RUNTIME_REPRO_AVOID_FILESTAT_METADATA: "1"', workflow)
        self.assertIn("REBUILD_WASI_VFS_FROM_SOURCE", script)
        self.assertIn("tools/patch_wasi_vfs_pack_file_size.py", script)
        self.assertIn(
            "rustup toolchain install 1.92.0 --profile minimal --target wasm32-unknown-unknown",
            workflow,
        )
        self.assertIn("fetch wasi-vfs-source wasi-vfs-source.tar.gz", script)
        self.assertIn("fetch wasi-vfs-wasi-submodule-source wasi-spec-source.tar.gz", script)
        self.assertIn("tools/patch_wasi_vfs_deterministic_hasher.py", script)
        self.assertIn(
            'CFLAGS_wasm32_unknown_unknown="--target=wasm32-wasip1 --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot"',
            script,
        )
        self.assertIn(
            "cargo +1.92.0 build --locked --release --target wasm32-unknown-unknown -p wasi-vfs",
            script,
        )
        self.assertIn(
            'WASI_VFS_LIB="${WASI_VFS_SOURCE_DIR}/target/wasm32-unknown-unknown/release/libwasi_vfs.a"',
            script,
        )

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
