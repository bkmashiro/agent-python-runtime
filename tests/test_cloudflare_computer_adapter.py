import importlib.util
import json
import os
import pathlib
import socket
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "tools" / "cloudflare_computer_adapter.py"
SPEC = importlib.util.spec_from_file_location("cloudflare_computer_adapter", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
adapter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(adapter)


class CloudflareComputerAdapterTests(unittest.TestCase):
    def test_workspace_paths_fail_closed(self):
        for value in ("", "../escape", "/absolute", "a/../../b", "a\x00b"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                adapter.safe_path(value)
        self.assertEqual("dir/file.txt", adapter.safe_path("dir/file.txt"))

    def test_request_is_strict_and_normalized(self):
        request = {
            "schema_version": "cloudflare-computer-local-trial/v1",
            "workspace_id": "trial-1",
            "files": {"input.txt": "alpha\n"},
            "source": "export default async () => 1;",
            "input": {"n": 1},
            "output_files": ["output.txt"],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "request.json"
            path.write_text(json.dumps(request), encoding="utf-8")
            self.assertEqual(request, adapter.load_request(path))
            request["extra"] = True
            path.write_text(json.dumps(request), encoding="utf-8")
            with self.assertRaises(ValueError):
                adapter.load_request(path)

    def test_result_creation_is_exclusive(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "result.json"
            adapter.write_new(path, {"ok": True})
            self.assertEqual({"ok": True}, json.loads(path.read_text()))
            with self.assertRaises(FileExistsError):
                adapter.write_new(path, {"ok": False})

    @unittest.skipUnless(os.environ.get("CLOUDFLARE_COMPUTER_CHECKOUT"), "set CLOUDFLARE_COMPUTER_CHECKOUT")
    def test_real_worker_javascript_workspace(self):
        checkout = pathlib.Path(os.environ["CLOUDFLARE_COMPUTER_CHECKOUT"])
        request = {
            "schema_version": "cloudflare-computer-local-trial/v1",
            "workspace_id": "placement-integration",
            "files": {"metrics.csv": "alpha,5\nbeta,2\ngamma,9\n"},
            "source": (
                'import { readFile, writeFile } from "node:fs/promises";\n'
                "export default async () => {\n"
                '  const raw = await readFile("/workspace/metrics.csv", "utf8");\n'
                '  const labels = raw.trim().split("\\n").map(line => line.split(",")).filter(([, value]) => Number(value) > 4).map(([label]) => label);\n'
                '  await writeFile("/workspace/high_value_rows.txt", labels.join(","));\n'
                '  return { status: "completed" };\n};'
            ),
            "input": {},
            "output_files": ["high_value_rows.txt"],
        }
        with tempfile.TemporaryDirectory() as directory, socket.socket() as probe:
            request_path = pathlib.Path(directory) / "request.json"
            request_path.write_text(json.dumps(request), encoding="utf-8")
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
            probe.close()
            result = adapter.run_trial(checkout, request_path, port)
        self.assertEqual("completed", result["execution"]["status"])
        self.assertEqual(0, result["execution"]["exitCode"])
        self.assertEqual("alpha,gamma", result["output_files"]["high_value_rows.txt"])
        self.assertEqual(adapter.COMMIT, result["identity"]["commit"])


if __name__ == "__main__":
    unittest.main()
