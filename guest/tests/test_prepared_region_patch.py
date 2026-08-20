import copy
import hashlib
import json
import traceback
import unittest

from agent_runtime.prepared_region import (
    PATCH_SCHEMA_VERSION,
    derive_prepared_region_tree,
    emit_prepared_region_patch_binding,
    emit_prepared_region_patch_request_json,
    encode_prepared_region_live_ins,
    execute_prepared_region_scratch_request_json,
)
from agent_runtime.semantic import analyze_source


def digest_bytes(value):
    return "sha256:" + hashlib.sha256(value).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True)


def contract_canonical(value):
    return canonical(value) + "\n"


class PreparedRegionPatchTests(unittest.TestCase):
    def decision(self, source, line=2, live_ins=None):
        bindings = {
            "artifact_sha256": "sha256:" + "a" * 64,
            "execution_profile_sha256": "sha256:" + "b" * 64,
            "import_closure_sha256": "sha256:" + "c" * 64,
            "capability_plan_sha256": "sha256:" + "d" * 64,
        }
        report = analyze_source(source, bindings, [])
        region = next(row for row in report["candidate_regions"] if row["span"]["start_line"] == line)
        lines = source.encode("utf-8").splitlines(keepends=True)
        span = region["span"]
        start = sum(len(row) for row in lines[:span["start_line"] - 1]) + span["start_column"]
        end = sum(len(row) for row in lines[:span["end_line"] - 1]) + span["end_column"]
        identity = {
            "schema_version": "pysolate.prepared-region-decision.v1",
            "consumer": "prepared_pure_region",
            "pass_schema": "pysolate.prepared-pure-region-pass.v1",
            "max_payload_bytes": 256,
            "source_sha256": report["source_sha256"],
            "ast_sha256": report["ast_sha256"],
            "analysis_sha256": digest_bytes(canonical(report).encode("utf-8")),
            "region_id": region["id"],
            "region_span": span,
            "region_source_sha256": digest_bytes(source.encode("utf-8")[start:end]),
            "live_ins_sha256": digest_bytes((live_ins if live_ins is not None else canonical(region["live_ins"])).encode("utf-8")),
            "environment_sha256": "sha256:" + "e" * 64,
            "execution_profile_sha256": bindings["execution_profile_sha256"],
            "import_closure_sha256": bindings["import_closure_sha256"],
            "capability_plan_sha256": bindings["capability_plan_sha256"],
            "pass_config_sha256": "sha256:" + "f" * 64,
            "codec": "canonical_json_bool_or_int64.v1",
            "output_name": region["live_outs"][0],
        }
        identity["identity_sha256"] = digest_bytes(contract_canonical(identity).encode("utf-8"))
        return contract_canonical(identity)

    def seal_patch(self, binding):
        patch = {
            "schema_version": PATCH_SCHEMA_VERSION,
            "pass_schema": "pysolate.prepared-pure-region-pass.v1",
            "helper_binding": "__pysolate_materialize_value__",
            **binding,
        }
        patch["identity_sha256"] = digest_bytes(contract_canonical(patch).encode("utf-8"))
        return contract_canonical(patch)

    def test_emits_one_rhs_patch_and_preserves_traceback_location(self):
        source = "seed = 40\nvalue = seed + 2\nresult = value\n"
        decision = self.decision(source)
        binding = emit_prepared_region_patch_binding(source, decision)
        self.assertEqual("value", binding["output_name"])
        self.assertEqual(2, binding["region_span"]["start_line"])
        tree, repeated = derive_prepared_region_tree(source, decision, self.seal_patch(binding))
        self.assertEqual(binding, repeated)
        namespace = {"__pysolate_materialize_value__": lambda _decision: 42}
        exec(compile(tree, "<agent-run>", "exec"), namespace, namespace)
        self.assertEqual(42, namespace["value"])
        self.assertEqual(42, namespace["result"])

        def fail(_decision):
            raise RuntimeError("claim failed")

        namespace = {"__pysolate_materialize_value__": fail}
        try:
            exec(compile(tree, "<agent-run>", "exec"), namespace, namespace)
        except RuntimeError as exc:
            frame = next(row for row in traceback.extract_tb(exc.__traceback__) if row.filename == "<agent-run>")
        else:
            self.fail("claim failure was not propagated")
        self.assertEqual(("<agent-run>", 2), (frame.filename, frame.lineno))

    def test_rejects_non_exact_shape_reserved_binding_and_all_identity_drift(self):
        valid_source = "seed = 40\nvalue = seed + 2\nresult = value\n"
        decision = self.decision(valid_source)
        binding = emit_prepared_region_patch_binding(valid_source, decision)
        patch = self.seal_patch(binding)
        cases = {
            "final source": (valid_source + "# drift\n", decision, patch),
            "decision": (valid_source, decision.replace('"output_name":"value"', '"output_name":"other"'), patch),
            "patch": (valid_source, decision, patch.replace('"output_name":"value"', '"output_name":"other"')),
            "region bytes": (valid_source.replace("seed + 2", "seed + 3"), decision, patch),
            "reserved helper": (valid_source + "__pysolate_materialize_value__ = None\n", decision, patch),
        }
        for name, (source, candidate_decision, candidate_patch) in cases.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                derive_prepared_region_tree(source, candidate_decision, candidate_patch)

        for source in (
            "seed = 40\nvalue = other = seed + 2\nresult = value\n",
            "seed = 40\nvalue: int = seed + 2\nresult = value\n",
            "seed = 40\nvalue += 2\nresult = value\n",
        ):
            with self.subTest(source=source), self.assertRaises(ValueError):
                emit_prepared_region_patch_binding(source, self.decision(source))

    def test_decoder_rejects_unknown_fields_trailing_data_and_noncanonical_json(self):
        source = "seed = 40\nvalue = seed + 2\nresult = value\n"
        decision = self.decision(source)
        binding = emit_prepared_region_patch_binding(source, decision)
        patch = self.seal_patch(binding)
        candidates = (
            decision.rstrip("\n")[:-1] + ',"extra":true}\n',
            decision + " ",
            json.dumps(json.loads(decision), indent=2),
        )
        for candidate in candidates:
            with self.subTest(candidate=candidate), self.assertRaises(ValueError):
                emit_prepared_region_patch_binding(source, candidate)
        with self.assertRaises(ValueError):
            derive_prepared_region_tree(source, decision, patch + " ")

    def test_bounded_rpc_emits_only_canonical_patch_binding(self):
        source = "seed = 40\nvalue = seed + 2\nresult = value\n"
        decision = self.decision(source)
        request = canonical({"decision": decision, "final_source": source})
        response = emit_prepared_region_patch_request_json(request)
        self.assertEqual(emit_prepared_region_patch_binding(source, decision), json.loads(response))
        self.assertEqual(response, canonical(json.loads(response)))
        for candidate in (request + " ", request[:-1] + ',"extra":true}'):
            with self.subTest(candidate=candidate), self.assertRaises(ValueError):
                emit_prepared_region_patch_request_json(candidate)

    def test_scratch_executes_only_the_exact_scalar_rhs_and_returns_typed_ready(self):
        source = "seed = 40\nvalue = seed + 2\nresult = value\n"
        live_ins = encode_prepared_region_live_ins({"seed": 40})
        decision = self.decision(source, live_ins=live_ins)
        request = canonical({"decision": decision, "final_source": source, "live_ins": live_ins})
        response = execute_prepared_region_scratch_request_json(request)
        decoded = json.loads(response)
        self.assertEqual(decoded["status"], "ready")
        self.assertEqual(decoded["payload"], 42)
        self.assertEqual(decoded["error_type"], "")
        self.assertEqual(decoded["payload_sha256"], digest_bytes(b"42"))
        self.assertEqual(response, canonical(decoded))

    def test_scratch_rejects_live_in_drift_unsafe_rhs_and_out_of_range_result(self):
        source = "seed = 40\nvalue = seed + 2\nresult = value\n"
        live_ins = encode_prepared_region_live_ins({"seed": 40})
        decision = self.decision(source, live_ins=live_ins)
        wrong_live_ins = encode_prepared_region_live_ins({"seed": 41})
        with self.assertRaises(ValueError):
            execute_prepared_region_scratch_request_json(canonical({"decision": decision, "final_source": source, "live_ins": wrong_live_ins}))

        unsafe = "seed = 40\nvalue = int(seed)\nresult = value\n"
        unsafe_decision = self.decision(unsafe, live_ins=live_ins)
        with self.assertRaises(ValueError):
            execute_prepared_region_scratch_request_json(canonical({"decision": unsafe_decision, "final_source": unsafe, "live_ins": live_ins}))

        overflow = "seed = 9223372036854775807\nvalue = seed * seed\nresult = value\n"
        overflow_live_ins = encode_prepared_region_live_ins({"seed": 9223372036854775807})
        overflow_decision = self.decision(overflow, live_ins=overflow_live_ins)
        response = json.loads(execute_prepared_region_scratch_request_json(canonical({"decision": overflow_decision, "final_source": overflow, "live_ins": overflow_live_ins})))
        self.assertEqual(response["status"], "rejected")
        self.assertEqual(response["error_type"], "result_out_of_range")
        self.assertIsNone(response["payload"])
        self.assertEqual(response["payload_sha256"], "")


if __name__ == "__main__":
    unittest.main()
