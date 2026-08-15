import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "verify-workstation-build.py"
SPEC = importlib.util.spec_from_file_location("workstation_build_verifier", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
verifier = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verifier)


def digest(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


class WorkstationBuildVerifierTests(unittest.TestCase):
    def make_evidence(self, root: pathlib.Path) -> None:
        (root / "dist").mkdir(parents=True, exist_ok=True)
        commit = "a" * 40
        key = "sha256:" + "b" * 64
        layer = "sha256:" + "c" * 64
        artifact = b"wasm fixture"
        ready = {
            "schema_version": "pysolate.workstation-guest-build.v0",
            "source_commit": commit,
            "source_tree": "d" * 40,
            "builder": "gpu31.doc.ic.ac.uk",
            "target": "wasm32-wasip1",
            "requested_cache_mode": "auto",
            "cache_key": key,
            "cache_disposition": "hit",
            "cache_layer_sha256": layer,
            "final_cache_key": "sha256:" + "e" * 64,
            "final_cache_disposition": "hit",
            "build_millis": 123,
        }
        cache = {
            "schema_version": "pysolate.guest-build-cache-evidence.v1",
            "cache_key": key,
            "disposition": "hit",
            "layer_sha256": layer,
            "final_cache_key": "sha256:" + "e" * 64,
            "final_cache_disposition": "hit",
        }
        manifest = {"artifact": {"sha256": digest(artifact), "size": len(artifact)}}
        files = {
            "RESULT.READY": json.dumps(ready, sort_keys=True, separators=(",", ":")).encode() + b"\n",
            "build.log": b"bounded build log\n",
            "dist/agent-python-runtime.wasm": artifact,
            "dist/build-cache.json": json.dumps(cache, sort_keys=True, separators=(",", ":")).encode() + b"\n",
            "dist/manifest.json": json.dumps(manifest, sort_keys=True, separators=(",", ":")).encode() + b"\n",
        }
        for relative, body in files.items():
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(body)
        sums = "".join(f"{digest(body)}  {relative}\n" for relative, body in sorted(files.items()))
        (root / "SHA256SUMS").write_text(sums)

    def test_accepts_exact_bound_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.make_evidence(root)
            report = verifier.verify(root, "a" * 40, "d" * 40)
            self.assertEqual("hit", report["cache_disposition"])
            self.assertTrue(str(report["artifact_sha256"]).startswith("sha256:"))

    def test_rejects_mutation_source_drift_and_cache_drift(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.make_evidence(root)
            (root / "dist/agent-python-runtime.wasm").write_bytes(b"changed")
            with self.assertRaises(ValueError):
                verifier.verify(root, "a" * 40, "d" * 40)
            self.make_evidence(root)
            with self.assertRaises(ValueError):
                verifier.verify(root, "e" * 40, "d" * 40)
            with self.assertRaises(ValueError):
                verifier.verify(root, "a" * 40, "e" * 40)
            self.make_evidence(root)
            cache = json.loads((root / "dist/build-cache.json").read_text())
            cache["disposition"] = "miss"
            body = json.dumps(cache, sort_keys=True, separators=(",", ":")).encode() + b"\n"
            (root / "dist/build-cache.json").write_bytes(body)
            lines = (root / "SHA256SUMS").read_text().splitlines()
            lines = [f"{digest(body)}  dist/build-cache.json" if line.endswith("  dist/build-cache.json") else line for line in lines]
            (root / "SHA256SUMS").write_text("\n".join(lines) + "\n")
            with self.assertRaises(ValueError):
                verifier.verify(root, "a" * 40, "d" * 40)

    def test_rejects_missing_checksum_duplicate_and_symlinked_dist(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.make_evidence(root)
            lines = (root / "SHA256SUMS").read_text().splitlines()
            (root / "SHA256SUMS").write_text("\n".join(lines[1:]) + "\n")
            with self.assertRaises(ValueError):
                verifier.verify(root, "a" * 40, "d" * 40)

            self.make_evidence(root)
            (root / "SHA256SUMS").write_text((root / "SHA256SUMS").read_text() + lines[0] + "\n")
            with self.assertRaises(ValueError):
                verifier.verify(root, "a" * 40, "d" * 40)

        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as target:
            root = pathlib.Path(directory)
            self.make_evidence(root)
            dist = root / "dist"
            for child in dist.iterdir():
                child.unlink()
            dist.rmdir()
            dist.symlink_to(pathlib.Path(target), target_is_directory=True)
            with self.assertRaises(ValueError):
                verifier.verify(root, "a" * 40, "d" * 40)


if __name__ == "__main__":
    unittest.main()
