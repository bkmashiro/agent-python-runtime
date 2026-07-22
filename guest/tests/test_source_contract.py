import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
HEADER = ROOT / "guest" / "include" / "agent_runtime_v1.h"
SOURCE = ROOT / "guest" / "src" / "runtime.c"
BOOTSTRAP = ROOT / "guest" / "bootstrap" / "agent_runtime" / "__init__.py"
TOOLS = ROOT / "guest" / "bootstrap" / "agent_runtime" / "tools.py"
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
            "wasi-vfs-static-library",
            "spdx-2.3-json-schema",
        ]:
            self.assertIn(source_id, text)
        self.assertIn("-mexec-model=reactor", text)
        for static_dependency in ["libmpdec.a", "libHacl_", "libexpat.a", "-ldl"]:
            self.assertIn(static_dependency, text)
        self.assertNotIn("--export=wasi_vfs_pack_fs", text)
        self.assertIn("--max-memory=536870912", text)
        self.assertEqual(1, text.count('--dir "'))
        self.assertIn("VFS_PYTHON_DIR", text)
        self.assertIn("copy_tree_deterministic.py", text)
        self.assertNotIn('cp -R "${CPYTHON_DIR}/Lib', text)
        self.assertIn("site-packages/agent_runtime", text)
        self.assertNotIn("latest", text.lower())
        self.assertNotIn("wasi-wheels", text.lower())

    def test_host_call_import_is_narrow_and_bounded(self):
        header = HEADER.read_text()
        source = SOURCE.read_text()
        tools = TOOLS.read_text()
        self.assertIn('AGENT_RUNTIME_IMPORT("agent_runtime_v1", "host_call")', header)
        self.assertIn("agent_runtime_host_call", header)
        self.assertIn('PyImport_AppendInittab("_agent_runtime_host"', source)
        self.assertIn("AGENT_RUNTIME_TOOL_RESPONSE_MAX", source)
        self.assertIn('"capability": "fetch_many"', tools)
        self.assertNotIn('"url"', tools)
        self.assertNotIn('"headers"', tools)

    def test_response_buffer_has_explicit_bound(self):
        text = SOURCE.read_text()
        match = re.search(r"#define AGENT_RUNTIME_RESPONSE_MAX \((\d+) \* 1024\)", text)
        if match is None:
            self.fail("AGENT_RUNTIME_RESPONSE_MAX is missing")
        self.assertLessEqual(int(match.group(1)), 1024)


if __name__ == "__main__":
    unittest.main()
