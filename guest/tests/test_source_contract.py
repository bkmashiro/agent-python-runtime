import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
HEADER = ROOT / "guest" / "include" / "agent_runtime_v1.h"
SOURCE = ROOT / "guest" / "src" / "runtime.c"
BOOTSTRAP = ROOT / "guest" / "bootstrap" / "agent_runtime" / "__init__.py"

BUILD_SCRIPT = ROOT / "guest" / "build" / "build-guest.sh"
ARTIFACT_WORKFLOW = ROOT / ".github" / "workflows" / "guest-artifact.yml"


class GuestSourceContractTests(unittest.TestCase):
    def test_header_declares_only_neutral_v1_exports(self):
        text = HEADER.read_text()
        exports = set(re.findall(r"AGENT_RUNTIME_EXPORT\(\"([^\"]+)\"\)", text))
        self.assertEqual(
            {"runtime_init", "runtime_validate_source", "runtime_analyze_source", "runtime_emit_prepared_region_patch", "runtime_execute_prepared_region_scratch", "runtime_select_prepared_region_execution", "runtime_prepare", "alloc", "dealloc", "execute"},
            exports,
        )

    def test_guest_patches_cpython_import_path_before_module_cache_lookup(self):
        build = BUILD_SCRIPT.read_text()
        source = SOURCE.read_text()
        self.assertIn("patch_cpython_import_gate.py", build)
        self.assertIn("PySys_AddAuditHook(agent_runtime_audit_hook", source)
        self.assertIn("seal_imports", source)

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
            "wasm-tools-linux-x86_64",
            "wasmtime-linux-x86_64",
            "wasi-vfs-cli-linux-x86_64",
            "wasi-vfs-static-library",
            "wasi-vfs-linked-storage-source",
        ]:
            self.assertIn(source_id, text)
        self.assertIn("-mexec-model=reactor", text)
        for static_dependency in ["libmpdec.a", "libHacl_", "libexpat.a", "-ldl"]:
            self.assertIn(static_dependency, text)
        self.assertNotIn("--export=wasi_vfs_pack_fs", text)
        self.assertIn("MAX_MEMORY_BYTES=536870912", text)
        self.assertIn("if [[ -z ${AGENT_RUNTIME_MEMORY_MODEL+x} ]]; then", text)
        self.assertIn("case \"${AGENT_RUNTIME_MEMORY_MODEL}\" in", text)
        self.assertIn("cow-fixed)", text)
        self.assertIn("AGENT_RUNTIME_MEMORY_MODEL must be growable or cow-fixed", text)
        self.assertIn("MAX_MEMORY_BYTES=\"${INITIAL_MEMORY_BYTES}\"", text)
        self.assertNotIn("COW_FIXED_MEMORY", text)
        self.assertEqual(1, text.count('-Wl,--max-memory="${MAX_MEMORY_BYTES}"'))
        self.assertEqual(1, text.count('--dir "${VFS_PYTHON_DIR}::/usr/lib/python3.14"'))
        self.assertIn("VFS_PYTHON_DIR", text)
        self.assertIn("copy_tree_deterministic.py", text)
        self.assertIn("patch_wasi_vfs_storage.py", text)
        self.assertIn("patch_cpython_wasi_timer_config.py", text)
        self.assertIn("config.site-wasm32-wasi", text)
        self.assertIn("CPython WASI build unexpectedly enabled absolute clock_nanosleep", text)
        self.assertIn("CPython WASI build did not enable relative nanosleep", text)
        self.assertIn('"${LLVM_AR}" d', text)
        self.assertIn("_sysconfigdata_*.py", text)
        self.assertIn("expected exactly one target sysconfigdata file", text)
        self.assertNotIn('cp -R "${CPYTHON_DIR}/Lib', text)
        self.assertIn("site-packages/agent_runtime", text)
        self.assertIn("AGENT_RUNTIME_PROBE_RUNNER", text)
        self.assertIn('tar -cf "${FINAL_CACHE_TMP}/dist.tar" -C "${ROOT_DIR}" dist', text)
        self.assertIn('tar -xf "${FINAL_CACHE_ENTRY}/dist.tar" -C "${ROOT_DIR}"', text)
        self.assertIn("--root dist --regular-only", text)
        self.assertNotIn("latest", text.lower())
        self.assertNotIn("wasi-wheels", text.lower())

    def test_workflow_builds_only_the_base_profile(self):
        workflow = ARTIFACT_WORKFLOW.read_text()
        self.assertIn("AGENT_RUNTIME_ARTIFACT_PROFILE: base", workflow)
        self.assertNotIn("artifact_profile:", workflow)
        self.assertNotIn("numpy-core", workflow)

    def test_attrs_staged_tree_is_reverified_before_packing(self):
        text = BUILD_SCRIPT.read_text()
        copy = text.index('"${VFS_PYTHON_DIR}/site-packages/attr"')
        verify = text.index("verify-tree", copy)
        pack = text.index('pack_guest "${FINAL_GUEST}"', verify)
        self.assertLess(copy, verify)
        self.assertLess(verify, pack)

    def test_final_cache_hits_reverify_restored_bundle(self):
        text = BUILD_SCRIPT.read_text()
        restore = text.index('tar -xf "${FINAL_CACHE_ENTRY}/dist.tar"')
        hit = text.index("FINAL_CACHE_STATUS=hit", restore)
        self.assertLess(text.index("verify-artifact.py", restore), hit)
        self.assertLess(text.index("write-supply-chain.py", restore), hit)
        self.assertIn("effective-source-lock", text[:restore])

    def test_attrs_profile_requires_private_patch_before_cache_lookup(self):
        text = BUILD_SCRIPT.read_text()
        self.assertIn("AGENT_RUNTIME_ARTIFACT_PROFILE", text)
        self.assertIn("PACKAGE_PROFILE_RECIPE} == attrs-770-v1", text)
        self.assertIn('PACKAGE_PROFILE_TOOL="${ROOT_DIR}/guest/build/package_profile.py"', text)
        self.assertIn("attrs-770 profile requires AGENT_RUNTIME_EXTENSION_PATCH", text)
        self.assertEqual(1, text.count("extension_profile.py\" verify-patch"))
        self.assertLess(text.index("extension_profile.py\" verify-patch"), text.index("FINAL_CACHE_KEY="))
        self.assertNotIn("pip install", text)

    def test_workflow_memory_model_dispatch_is_bounded_choice(self):
        workflow = ARTIFACT_WORKFLOW.read_text()
        self.assertIn("workflow_dispatch:", workflow)
        self.assertIn("inputs:", workflow)
        self.assertIn("memory_model:", workflow)
        self.assertIn("required: true", workflow)
        self.assertIn("- growable", workflow)
        self.assertIn("- cow-fixed", workflow)
        self.assertIn("AGENT_RUNTIME_MEMORY_MODEL: ${{ inputs.memory_model || 'growable' }}", workflow)
        self.assertNotIn("AGENT_RUNTIME_MEMORY_MODEL: growable", workflow)
        self.assertNotIn("AGENT_RUNTIME_COW_FIXED_MEMORY", workflow)
        self.assertNotIn("push:", workflow)
        self.assertNotIn("pull_request:", workflow)
        self.assertNotIn("schedule:", workflow)

    def test_host_call_import_is_narrow_and_bounded(self):
        header = HEADER.read_text()
        source = SOURCE.read_text()
        self.assertIn('AGENT_RUNTIME_IMPORT("agent_runtime_v1", "host_call")', header)
        self.assertIn("agent_runtime_host_call", header)
        self.assertIn('AGENT_RUNTIME_IMPORT("agent_runtime_v1", "materialize_value")', header)
        self.assertIn("agent_runtime_materialize_value", header)
        self.assertIn('PyImport_AppendInittab("_agent_runtime_host"', source)
        self.assertIn("AGENT_RUNTIME_TOOL_RESPONSE_MAX", source)
        self.assertIn("AGENT_RUNTIME_MATERIALIZED_RESPONSE_MAX", source)

    def test_response_buffer_has_explicit_bound(self):
        text = SOURCE.read_text()
        match = re.search(r"#define AGENT_RUNTIME_RESPONSE_MAX \((\d+) \* 1024\)", text)
        if match is None:
            self.fail("AGENT_RUNTIME_RESPONSE_MAX is missing")
        self.assertLessEqual(int(match.group(1)), 1024)



if __name__ == "__main__":
    unittest.main()
