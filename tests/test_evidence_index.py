import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "write_evidence_index", ROOT / "tools" / "write_evidence_index.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


class EvidenceIndexTests(unittest.TestCase):
    def fixture(self, root):
        wasm = root / "agent-python-runtime.wasm"
        wasm.write_bytes(b"wasm")
        artifact_hash = sha256(wasm.read_bytes())
        manifest = root / "manifest.json"
        manifest.write_text(json.dumps({
            "artifact": {"name": wasm.name, "size": wasm.stat().st_size, "sha256": artifact_hash},
            "build": {"repository_commit": "a" * 40},
            "target": "wasm32-wasip1",
        }))
        sbom = root / "sbom.spdx.json"
        sbom.write_text(json.dumps({
            "spdxVersion": "SPDX-2.3",
            "packages": [{"name": wasm.name, "checksums": [{"algorithm": "SHA256", "checksumValue": artifact_hash}]}],
        }))
        notices = root / "THIRD_PARTY_NOTICES.md"
        notices.write_text(f"Artifact SHA-256: `{artifact_hash}`\n")
        return wasm, manifest, sbom, notices

    def test_builds_strict_identity_bound_index(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            wasm, manifest, sbom, notices = self.fixture(root)
            evidence = MODULE.build_index(
                artifact=wasm,
                manifest_path=manifest,
                sbom_path=sbom,
                notices_path=notices,
                commit="a" * 40,
                run_id="12345",
                run_url="https://github.com/bkmashiro/agent-python-runtime/actions/runs/12345",
            )
            self.assertEqual(1, evidence["schema_version"])
            self.assertEqual("12345", evidence["workflow"]["run_id"])
            self.assertEqual(sha256(wasm.read_bytes()), evidence["artifact"]["sha256"])
            self.assertEqual("passed", evidence["tests"]["guest_e2e"])
            self.assertIn("not established", " ".join(evidence["limitations"]).lower())
            self.assertEqual([], MODULE.validate_index(evidence, wasm, manifest, sbom, notices))

    def test_rejects_artifact_or_commit_drift(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            wasm, manifest, sbom, notices = self.fixture(root)
            with self.assertRaisesRegex(ValueError, "commit"):
                MODULE.build_index(
                    artifact=wasm, manifest_path=manifest, sbom_path=sbom, notices_path=notices,
                    commit="b" * 40, run_id="1", run_url="https://example.invalid/actions/runs/1",
                )
            manifest_data = json.loads(manifest.read_text())
            manifest_data["artifact"]["sha256"] = "0" * 64
            manifest.write_text(json.dumps(manifest_data))
            with self.assertRaisesRegex(ValueError, "artifact"):
                MODULE.build_index(
                    artifact=wasm, manifest_path=manifest, sbom_path=sbom, notices_path=notices,
                    commit="a" * 40, run_id="1", run_url="https://example.invalid/actions/runs/1",
                )


if __name__ == "__main__":
    unittest.main()
