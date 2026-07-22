import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
HEADER = ROOT / "guest" / "include" / "agent_runtime_v1.h"
SOURCE = ROOT / "guest" / "src" / "runtime.c"
BOOTSTRAP = ROOT / "guest" / "bootstrap" / "agent_runtime" / "__init__.py"
BUILD_SCRIPT = ROOT / "guest" / "build" / "build-guest.sh"


class GuestSourceContractTests(unittest.TestCase):
    def test_header_declares_only_neutral_v1_exports(self):
        text = HEADER.read_text()
        exports = set(re.findall(r"AGENT_RUNTIME_EXPORT\(\"([^\"]+)\"\)", text))
        self.assertEqual(
            {"runtime_init", "runtime_prepare", "alloc", "dealloc", "execute"},
            exports,
        )

    def test_source_and_bootstrap_exclude_product_specific_protocol(self):
        text = SOURCE.read_text() + "\n" + BOOTSTRAP.read_text()
        for forbidden in [
            "shimmy",
            "lambda feedback",
            "evaluation_function",
            "preview_function",
            "py_exec",
            "resp_buf",
            "resp_len",
        ]:
            self.assertNotIn(forbidden, text.lower())

    def test_build_uses_official_pinned_inputs(self):
        text = BUILD_SCRIPT.read_text()
        self.assertIn("Tools/wasm/wasi", text)
        self.assertIn("fetch_locked_source.py", text)
        for source_id in [
            "cpython-source",
            "wasi-sdk-linux-x86_64",
            "wasmtime-linux-x86_64",
            "wasi-vfs-cli-linux-x86_64",
            "wasi-vfs-lib-wasm32-wasip1",
        ]:
            self.assertIn(source_id, text)
        self.assertIn("-mexec-model=reactor", text)
        self.assertNotIn("latest", text.lower())
        self.assertNotIn("wasi-wheels", text.lower())

    def test_response_buffer_has_explicit_bound(self):
        text = SOURCE.read_text()
        match = re.search(r"#define AGENT_RUNTIME_RESPONSE_MAX \((\d+) \* 1024\)", text)
        if match is None:
            self.fail("AGENT_RUNTIME_RESPONSE_MAX is missing")
        self.assertLessEqual(int(match.group(1)), 1024)


if __name__ == "__main__":
    unittest.main()
