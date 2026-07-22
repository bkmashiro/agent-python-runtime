import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "compare_guest_builds", ROOT / "tools" / "compare_guest_builds.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def write_bundle(directory, wasm, epoch="1784729528", limitation="same"):
    directory.mkdir(parents=True)
    manifest = {
        "schema_version": 1,
        "artifact": {"filename": "agent-python-runtime.wasm", "size": len(wasm), "sha256": sha256(wasm)},
        "build": {"repository_commit": "a" * 40, "source_date_epoch": epoch},
        "limitations": [limitation],
    }
    manifest_bytes = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode()
    (directory / "agent-python-runtime.wasm").write_bytes(wasm)
    (directory / "manifest.json").write_bytes(manifest_bytes)
    sums = f"{sha256(wasm)}  agent-python-runtime.wasm\n{sha256(manifest_bytes)}  manifest.json\n"
    (directory / "SHA256SUMS").write_text(sums)
    (directory / "THIRD_PARTY_NOTICES.md").write_text("locked notices\n")


class GuestBuildComparisonTests(unittest.TestCase):
    def test_exact_bundles_are_the_only_success(self):
        wasm = b"\x00asm\x01\x00\x00\x00\x00\x05\x04name"
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm)
            write_bundle(root / "right", wasm)
            report = MODULE.compare_directories(root / "left", root / "right")
        self.assertTrue(report["exact_match"])
        self.assertEqual(report["source_date_epoch"], "1784729528")
        self.assertTrue(all(row["match"] for row in report["files"]))
        self.assertEqual(report["manifest_differences"], [])
        self.assertTrue(all(row["match"] for row in report["wasm_sections"]))

    def test_wasm_mismatch_is_localized_by_section_without_weakening_failure(self):
        left = b"\x00asm\x01\x00\x00\x00\x0b\x02\x01x"
        right = b"\x00asm\x01\x00\x00\x00\x0b\x02\x01y"
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", left)
            write_bundle(root / "right", right)
            report = MODULE.compare_directories(root / "left", root / "right")
        self.assertFalse(report["exact_match"])
        self.assertFalse(next(row for row in report["files"] if row["path"].endswith(".wasm"))["match"])
        mismatch = next(row for row in report["wasm_sections"] if not row["match"])
        self.assertEqual(mismatch["section_id"], 11)
        self.assertNotEqual(mismatch["left_sha256"], mismatch["right_sha256"])

    def test_manifest_difference_reports_pointer_and_epoch_mismatch_fails(self):
        wasm = b"\x00asm\x01\x00\x00\x00"
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm)
            write_bundle(root / "right", wasm, epoch="1784729529", limitation="changed")
            report = MODULE.compare_directories(root / "left", root / "right")
        self.assertFalse(report["exact_match"])
        pointers = {row["pointer"] for row in report["manifest_differences"]}
        self.assertIn("/build/source_date_epoch", pointers)
        self.assertIn("/limitations/0", pointers)
        self.assertIsNone(report["source_date_epoch"])

    def test_missing_or_extra_bundle_file_is_an_exact_mismatch(self):
        wasm = b"\x00asm\x01\x00\x00\x00"
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm)
            write_bundle(root / "right", wasm)
            (root / "right" / "unexpected.txt").write_text("unexpected")
            report = MODULE.compare_directories(root / "left", root / "right")
        self.assertFalse(report["exact_match"])
        row = next(row for row in report["files"] if row["path"] == "unexpected.txt")
        self.assertFalse(row["match"])
        self.assertIsNone(row["left_sha256"])


if __name__ == "__main__":
    unittest.main()
