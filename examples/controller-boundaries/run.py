#!/usr/bin/env python3
"""Run four bounded local examples through the real apyrun CLI."""

from __future__ import annotations

import argparse
import functools
import http.server
import json
from pathlib import Path
import subprocess
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent

EXAMPLES = (
    ("01-local-transform", 0, {"count": 2, "descending": [10, 7], "total": 17}),
    ("02-one-source", 1, {"count": 2, "ids": ["gamma", "alpha"], "top": "gamma"}),
    (
        "03-two-sources",
        2,
        {
            "case_id": "workspace-summary",
            "ranked": [
                {"id": "gamma", "normalized_score": 0.1},
                {"id": "alpha", "normalized_score": 0.07},
                {"id": "beta", "normalized_score": 0.04},
            ],
            "suite_id": "pysolate-core",
        },
    ),
    (
        "04-workflow-with-workspace",
        2,
        {
            "case_id": "workspace-summary",
            "ranked": [
                {"id": "gamma", "normalized_score": 0.1},
                {"id": "alpha", "normalized_score": 0.07},
                {"id": "beta", "normalized_score": 0.04},
            ],
            "suite_id": "pysolate-core",
        },
    ),
)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifact", required=True, help="verified Guest WASM artifact")
    args = parser.parse_args()

    handler = functools.partial(
        http.server.SimpleHTTPRequestHandler,
        directory=str(HERE / "fixtures"),
    )
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    try:
        with tempfile.TemporaryDirectory() as temporary:
            config_path = Path(temporary) / "host.json"

            for name, expected_calls, expected_result in EXAMPLES:
                config = {
                    "information_sources": {
                        "demo_catalog": {
                            "endpoint": f"http://127.0.0.1:{server.server_port}/catalog.json",
                            "timeout_ms": 1000,
                            "max_response_bytes": 4096,
                        },
                        "benchmark_manifest": {
                            "endpoint": f"http://127.0.0.1:{server.server_port}/benchmark.json",
                            "timeout_ms": 1000,
                            "max_response_bytes": 262144,
                        },
                    },
                    "max_tool_calls": 2,
                }
                if name == "04-workflow-with-workspace":
                    workspace = Path(temporary) / "workspace"
                    workspace.mkdir()
                    config["workspace"] = {
                        "source_directory": str(workspace),
                        "disposition": "discard",
                        "limits": {"max_files": 32, "max_bytes": 1048576, "max_file_bytes": 262144, "max_depth": 8},
                    }
                config_path.write_text(json.dumps(config), encoding="utf-8")
                inputs = json.loads((HERE / "inputs" / f"{name}.json").read_text())
                request = {"run_id": f"example-{name}", "inputs": inputs}
                request["code"] = (HERE / f"{name}.py").read_text()
                command = [
                    "go", "run", "./cmd/apyrun",
                    "-artifact", str(Path(args.artifact).resolve()),
                ]
                if expected_calls:
                    command += ["-config", str(config_path)]
                completed = subprocess.run(
                    command,
                    cwd=ROOT,
                    input=json.dumps(request),
                    text=True,
                    capture_output=True,
                    check=False,
                )
                if completed.returncode:
                    raise SystemExit(f"{name}: apyrun failed: {completed.stderr.strip()}")
                response = json.loads(completed.stdout)
                calls = response["metrics"]["capability_calls"]
                receipts = response["receipts"]
                if (
                    response["status"] != "ok"
                    or response["result"] != expected_result
                    or calls != expected_calls
                    or len(receipts) != expected_calls
                    or any(receipt["outcome"] != "ok" for receipt in receipts)
                    or (name == "04-workflow-with-workspace" and response.get("workspace_receipt", {}).get("entry_count") != 2)
                ):
                    raise SystemExit(f"{name}: unexpected response: {response}")
                print(json.dumps({"example": name, "capability_calls": calls, "receipts": len(receipts), "result": response["result"]}, sort_keys=True))
    finally:
        server.shutdown()
        thread.join()
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
