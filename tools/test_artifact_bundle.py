import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

import artifact_bundle


class ArtifactBundleTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.artifact = self.root / "pysolate.wasm"
        self.artifact.write_bytes(b"\x00asm" + b"fixture")
        self.manifest = self.root / "pysolate.manifest.json"
        self.write_manifest()

    def write_manifest(self):
        data = self.artifact.read_bytes()
        self.manifest.write_text(json.dumps({
            "schema_version": 1,
            "profile": "agent-core",
            "target": "wasm32-wasip1",
            "artifact": {
                "filename": "pysolate.wasm",
                "bytes": len(data),
                "sha256": hashlib.sha256(data).hexdigest(),
            },
        }), encoding="utf-8")

    def test_verify_rejects_tampered_artifact(self):
        verified = artifact_bundle.verify_artifact(self.artifact, self.manifest)
        self.assertEqual(verified["profile"], "agent-core")
        self.artifact.write_bytes(b"\x00asmchanged")
        with self.assertRaisesRegex(ValueError, "digest"):
            artifact_bundle.verify_artifact(self.artifact, self.manifest)

    def test_bundle_install_round_trip(self):
        bundle = self.root / "agent-core.tar.gz"
        second_bundle = self.root / "agent-core-second.tar.gz"
        artifact_bundle.create_bundle(self.artifact, self.manifest, bundle)
        artifact_bundle.create_bundle(self.artifact, self.manifest, second_bundle)
        self.assertEqual(bundle.read_bytes(), second_bundle.read_bytes())
        destination = self.root / "installed"
        artifact_bundle.install_bundle(str(bundle), destination)
        self.assertEqual((destination / "pysolate.wasm").read_bytes(), self.artifact.read_bytes())
        artifact_bundle.verify_artifact(destination / "pysolate.wasm", destination / "pysolate.manifest.json")

    def test_install_rejects_archive_escape(self):
        bundle = self.root / "unsafe.tar.gz"
        with tarfile.open(bundle, "w:gz") as archive:
            payload = b"bad"
            info = tarfile.TarInfo("../escape")
            info.size = len(payload)
            archive.addfile(info, io.BytesIO(payload))
        with self.assertRaisesRegex(ValueError, "bundle entries"):
            artifact_bundle.install_bundle(str(bundle), self.root / "installed")


if __name__ == "__main__":
    unittest.main()
