import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
HEADER = ROOT / "guest" / "include" / "agent_runtime_v1.h"
SOURCE = ROOT / "guest" / "src" / "runtime.c"
BOOTSTRAP = ROOT / "guest" / "bootstrap" / "agent_runtime" / "__init__.py"
TOOLS = ROOT / "guest" / "bootstrap" / "agent_runtime" / "tools.py"
BUILD_SCRIPT = ROOT / "guest" / "build" / "build-guest.sh"
ARTIFACT_WORKFLOW = ROOT / ".github" / "workflows" / "guest-artifact.yml"


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
            "wasi-vfs-source",
            "wasi-vfs-wasi-submodule-source",
            "wasi-vfs-static-library",
            "wasi-vfs-linked-storage-source",
            "spdx-2.3-json-schema",
        ]:
            self.assertIn(source_id, text)
        self.assertIn("-mexec-model=reactor", text)
        for static_dependency in ["libmpdec.a", "libHacl_", "libexpat.a", "-ldl"]:
            self.assertIn(static_dependency, text)
        self.assertNotIn("--export=wasi_vfs_pack_fs", text)
        self.assertIn("MAX_MEMORY_BYTES=536870912", text)
        self.assertIn("COW_FIXED_MEMORY=${AGENT_RUNTIME_COW_FIXED_MEMORY:-0}", text)
        self.assertIn("1) MAX_MEMORY_BYTES=${INITIAL_MEMORY_BYTES}", text)
        self.assertEqual(3, text.count('-Wl,--max-memory="${MAX_MEMORY_BYTES}"'))
        self.assertEqual(2, text.count('--dir "${VFS_PYTHON_DIR}::/usr/lib/python3.14"'))
        self.assertIn("VFS_PYTHON_DIR", text)
        self.assertIn("copy_tree_deterministic.py", text)
        self.assertIn("patch_wasi_vfs_storage.py", text)
        self.assertIn("patch_cpython_wasi_timer_config.py", text)
        self.assertIn("config.site-wasm32-wasi", text)
        self.assertIn('"${LLVM_AR}" d', text)
        self.assertIn("_sysconfigdata_*.py", text)
        self.assertIn("expected exactly one target sysconfigdata file", text)
        self.assertNotIn('cp -R "${CPYTHON_DIR}/Lib', text)
        self.assertIn("site-packages/agent_runtime", text)
        self.assertNotIn("latest", text.lower())
        self.assertNotIn("wasi-wheels", text.lower())

    def test_artifact_profile_is_explicit_and_base_remains_default(self):
        build = BUILD_SCRIPT.read_text()
        workflow = ARTIFACT_WORKFLOW.read_text()
        self.assertIn("AGENT_RUNTIME_ARTIFACT_PROFILE", build)
        self.assertIn("${AGENT_RUNTIME_ARTIFACT_PROFILE:-base}", build)
        self.assertIn("base)", build)
        self.assertIn("numpy-core)", build)
        self.assertIn("unsupported artifact profile", build)
        self.assertIn("--artifact-profile", build)
        self.assertIn("--extension-selection", build)
        self.assertIn('bash "${ROOT_DIR}/experiments/numpy-wasi/probe.sh"', build)
        self.assertIn("artifact_profile:", workflow)
        self.assertIn("default: base", workflow)
        self.assertIn("- base", workflow)
        self.assertIn("- numpy-core", workflow)
        self.assertIn("AGENT_RUNTIME_ARTIFACT_PROFILE", workflow)
        self.assertIn("${{ github.ref }}-${{ inputs.artifact_profile || 'base' }}", workflow)
        self.assertGreaterEqual(
            workflow.count("if: env.AGENT_RUNTIME_ARTIFACT_PROFILE == 'base'"), 6
        )
        self.assertEqual(
            4, workflow.count("if: env.AGENT_RUNTIME_ARTIFACT_PROFILE == 'numpy-core'")
        )
        self.assertIn("-class profile-candidate", workflow)
        self.assertIn("runtime-profile-candidate-linux-amd64.json", workflow)
        self.assertIn("prepared-profile-candidate-linux-amd64.json", workflow)
        self.assertIn('register_selected_builtins()', SOURCE.read_text())
        self.assertIn('AGENT_RUNTIME_WASM_EXTENSION_FINDER_SCRIPT', SOURCE.read_text())
        self.assertIn('wasm_extension_finder.h', SOURCE.read_text())

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

    def test_preinitialization_spike_is_build_only_and_base_scoped(self):
        source = SOURCE.read_text()
        build = BUILD_SCRIPT.read_text()
        self.assertIn("#ifdef AGENT_RUNTIME_PREINITIALIZATION_SPIKE", source)
        self.assertIn('export_name("runtime_preinitialize")', source)
        self.assertIn('export_name("runtime_preinitialized_initialize")', source)
        self.assertIn("preinitialize_python_or_trap();", source)
        self.assertIn("python_initialized=%d", source)
        self.assertIn("config.use_hash_seed = 1", source)
        self.assertIn("config.hash_seed = 0xa9e17f5dUL", source)
        self.assertNotIn("runtime_preinitialize", HEADER.read_text())
        self.assertIn("AGENT_RUNTIME_PREINITIALIZATION_SPIKE must be 0 or 1", build)
        self.assertIn("preinitialization spike is restricted to the base artifact profile", build)
        self.assertEqual(1, build.count("-DAGENT_RUNTIME_PREINITIALIZATION_SPIKE=1"))
        self.assertIn('PREINITIALIZATION_INPUT_DIR="${PREINITIALIZATION_SPIKE_DIR}/input"', build)
        self.assertIn('PREINITIALIZATION_INPUT_GUEST="${PREINITIALIZATION_INPUT_DIR}/agent-python-runtime.wasm"', build)


if __name__ == "__main__":
    unittest.main()
