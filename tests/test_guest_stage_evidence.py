import hashlib
import importlib.util
import json
import os
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "write_guest_stage_evidence", ROOT / "tools" / "write_guest_stage_evidence.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class GuestStageEvidenceTests(unittest.TestCase):
    def test_writes_retained_stage_files_and_secret_free_report(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            inputs = root / "inputs"
            vfs = root / "vfs"
            evidence = root / "evidence"
            inputs.mkdir()
            (vfs / "pkg").mkdir(parents=True)
            (vfs / "empty").mkdir()

            payloads = {
                "raw.wasm": b"raw-wasm",
                "final.wasm": b"final-wasm",
                "repeat.wasm": b"repeat-wasm",
                "libwasi_vfs.a": b"patched-archive",
                "linked_storage.o": b"linked-object",
                "wasi-vfs": b"locked-cli",
                "sources.lock.json": b'{"schema_version": 1, "target": "wasm32-wasip1", "sources": []}\n',
            }
            for name, data in payloads.items():
                (inputs / name).write_bytes(data)
            (vfs / "z.py").write_bytes(b"z")
            (vfs / "pkg" / "a.py").write_bytes(b"a")
            prepack_manifest = root / "prepack-vfs-manifest.json"
            MODULE.write_vfs_manifest(vfs, prepack_manifest)
            (vfs / "z.py").write_bytes(b"changed-after-prepack-capture")

            previous_unrelated = os.environ.get("UNRELATED_ENV")
            os.environ["UNRELATED_ENV"] = "excluded-value"
            try:
                report = MODULE.write_evidence(
                    evidence_dir=evidence,
                    raw_wasm=inputs / "raw.wasm",
                    final_wasm=inputs / "final.wasm",
                    repeat_packed_wasm=inputs / "repeat.wasm",
                    patched_wasi_vfs_archive=inputs / "libwasi_vfs.a",
                    linked_storage_object=inputs / "linked_storage.o",
                    wasi_vfs_cli=inputs / "wasi-vfs",
                    source_lock=inputs / "sources.lock.json",
                    vfs_manifest=prepack_manifest,
                    repository_commit="a" * 40,
                    source_date_epoch="1784734890",
                    run_id="29934585110",
                    run_attempt="1",
                    job="build",
                    replica="one",
                    runner_os="Linux",
                    runner_arch="X64",
                    build_dir="/runner/build",
                    dist_dir="/runner/dist",
                    configured_vfs_root="/dev/shm/agent-runtime-vfs",
                )
            finally:
                if previous_unrelated is None:
                    os.environ.pop("UNRELATED_ENV", None)
                else:
                    os.environ["UNRELATED_ENV"] = previous_unrelated

            report_path = evidence / "stage-evidence.json"
            manifest_path = evidence / "vfs-manifest.json"
            self.assertEqual(report, json.loads(report_path.read_text()))
            self.assertEqual(report["schema_version"], 2)
            serialized = report_path.read_text()
            self.assertNotIn("excluded-value", serialized)
            self.assertNotIn("UNRELATED_ENV", serialized)

            retained = {
                "raw_wasm": ("agent-python-runtime.raw.wasm", payloads["raw.wasm"]),
                "final_wasm": ("agent-python-runtime.wasm", payloads["final.wasm"]),
                "repeat_packed_wasm": ("agent-python-runtime.pack-b.wasm", payloads["repeat.wasm"]),
                "patched_wasi_vfs_archive": ("libwasi_vfs.patched.a", payloads["libwasi_vfs.a"]),
                "linked_storage_object": ("linked_storage.o", payloads["linked_storage.o"]),
                "source_lock": ("sources.lock.json", payloads["sources.lock.json"]),
            }
            for role, (name, data) in retained.items():
                self.assertEqual((evidence / name).read_bytes(), data)
                self.assertEqual(report["files"][role]["path"], name)
                self.assertEqual(report["files"][role]["size_bytes"], len(data))
                self.assertEqual(report["files"][role]["sha256"], sha256(data))
                self.assertTrue(report["files"][role]["retained"])

            self.assertFalse(report["files"]["wasi_vfs_cli"]["retained"])
            self.assertEqual(report["files"]["wasi_vfs_cli"]["sha256"], sha256(payloads["wasi-vfs"]))
            self.assertEqual(report["files"]["source_lock"]["sha256"], sha256(payloads["sources.lock.json"]))
            self.assertEqual(report["files"]["vfs_manifest"]["sha256"], hashlib.sha256(manifest_path.read_bytes()).hexdigest())

            manifest = json.loads(manifest_path.read_text())
            self.assertEqual(
                [(row["path"], row["kind"]) for row in manifest["entries"]],
                [("empty", "directory"), ("pkg", "directory"), ("pkg/a.py", "file"), ("z.py", "file")],
            )
            file_rows = {row["path"]: row for row in manifest["entries"] if row["kind"] == "file"}
            self.assertEqual(file_rows["pkg/a.py"]["sha256"], sha256(b"a"))
            self.assertEqual(file_rows["z.py"]["sha256"], sha256(b"z"))
            self.assertEqual(manifest["directory_count"], 2)
            self.assertEqual(manifest["file_count"], 2)

            self.assertEqual(
                set(report["environment_allowlist"]),
                {"AGENT_RUNTIME_BUILD_DIR", "AGENT_RUNTIME_DIST_DIR", "AGENT_RUNTIME_VFS_ROOT", "SOURCE_DATE_EPOCH"},
            )
            self.assertEqual(report["build_identity"]["github"]["run_id"], "29934585110")
            self.assertEqual(report["build_identity"]["github"]["job"], "build")
            self.assertEqual(report["build_identity"]["github"]["replica"], "one")
            self.assertEqual(report["pack_command"][1], "pack")
            self.assertEqual(report["pack_command"][3:5], ["--dir", "/dev/shm/agent-runtime-vfs::/usr/lib/python3.14"])
            self.assertEqual(report["repeat_pack_command"][1], "pack")
            self.assertTrue(report["repeat_pack_command"][-1].endswith("repeat.wasm"))

    def test_rejects_invalid_build_identity_before_writing(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            inputs = root / "inputs"
            inputs.mkdir()
            for name in ("raw", "final", "archive", "object", "cli", "lock"):
                (inputs / name).write_bytes(b"x")
            vfs = root / "vfs"
            vfs.mkdir()
            evidence = root / "evidence"

            with self.assertRaisesRegex(ValueError, "repository commit"):
                MODULE.write_evidence(
                    evidence_dir=evidence,
                    raw_wasm=inputs / "raw",
                    final_wasm=inputs / "final",
                    repeat_packed_wasm=inputs / "final",
                    patched_wasi_vfs_archive=inputs / "archive",
                    linked_storage_object=inputs / "object",
                    wasi_vfs_cli=inputs / "cli",
                    source_lock=inputs / "lock",
                    vfs_manifest=inputs / "lock",
                    repository_commit="not-a-commit",
                    source_date_epoch="1784734890",
                    run_id="1",
                    run_attempt="1",
                    job="build",
                    replica="one",
                    runner_os="Linux",
                    runner_arch="X64",
                    build_dir="/build",
                    dist_dir="/dist",
                    configured_vfs_root="/vfs",
                )
            self.assertFalse(evidence.exists())


if __name__ == "__main__":
    unittest.main()
