#!/usr/bin/env python3
"""Build the pinned GitChameleon NumPy-derived PLM workload manifest."""

from __future__ import annotations

import argparse
import ast
import hashlib
import importlib
import json
import pathlib
from typing import Any

DATASET_SHA256 = "978c7c581cad399989cb8399ec208ddd0edb6260ef576b3ce442aeaae455609e"
DATASET_COMMIT = "3a1b6045a6b2a276bd24d715589cb041f8eccb93"
EXAMPLE_IDS = tuple(str(value) for value in range(66, 81))
RATES = (20, 50, 100, 200)
SCHEMA = "pysolate.gitchameleon-numpy-derived-plm.v1"


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def function_inputs(tree: ast.AST) -> list[str]:
    defined = {node.name for node in ast.walk(tree) if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))}
    names: list[str] = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Name) or node.func.id not in defined:
            continue
        for argument in node.args:
            if isinstance(argument, ast.Name) and argument.id not in names:
                names.append(argument.id)
    return names


def input_assignments(tree: ast.AST, names: list[str]) -> dict[str, tuple[Any, int, int]]:
    assignments: dict[str, tuple[Any, int, int]] = {}
    for node in ast.walk(tree):
        if not isinstance(node, ast.Assign) or len(node.targets) != 1 or not isinstance(node.targets[0], ast.Name):
            continue
        name = node.targets[0].id
        if name not in names:
            continue
        call = node.value
        if not isinstance(call, ast.Call) or not isinstance(call.func, ast.Attribute):
            raise ValueError(f"{name}: input is not np.array")
        if not isinstance(call.func.value, ast.Name) or call.func.value.id != "np" or call.func.attr != "array" or not call.args:
            raise ValueError(f"{name}: input is not np.array")
        value = ast.literal_eval(call.args[0])
        assignments[name] = (value, node.lineno, node.end_lineno or node.lineno)
    require(set(assignments) == set(names), "not every function input has a literal NumPy assignment")
    return assignments


def remove_input_assignments(test_source: str, assignments: dict[str, tuple[Any, int, int]]) -> str:
    removed = set()
    for _, start, end in assignments.values():
        removed.update(range(start, end + 1))
    return "\n".join(line for number, line in enumerate(test_source.splitlines(), 1) if number not in removed) + "\n"


def without_numpy_import(source: str) -> str:
    lines = source.splitlines()
    retained = [line for line in lines if line.strip() not in {"import numpy", "import numpy as np"}]
    require(len(retained) < len(lines), "NumPy source has no removable top-level import")
    return "\n".join(retained).strip() + "\n"


def encode_numeric_vector(value: Any) -> tuple[str, str]:
    require(isinstance(value, list) and len(value) > 0, "Host input must be a non-empty flat list")
    require(all(type(item) in {int, float} for item in value), "Host input must contain only numeric scalars")
    decoder = "float" if any(type(item) is float for item in value) else "int"
    return ",".join(str(item) for item in value), decoder


def build(dataset: pathlib.Path, tokenizer_name: str, tokenizer_version: str) -> dict[str, Any]:
    raw = dataset.read_bytes()
    require(hashlib.sha256(raw).hexdigest() == DATASET_SHA256, "dataset digest drift")
    rows = {
        str(row["example_id"]): (line_number, row)
        for line_number, line in enumerate(raw.splitlines(), 1)
        for row in [json.loads(line)]
    }
    tiktoken = importlib.import_module("tiktoken")
    encoding = tiktoken.get_encoding(tokenizer_name)
    tasks = []
    for example_id in EXAMPLE_IDS:
        dataset_row, row = rows[example_id]
        require(row.get("library") == "numpy" and not row.get("additional_dependencies"), f"{example_id}: package-stack drift")
        implementation = without_numpy_import(str(row["starting_code"]) + str(row["solution"]))
        test_source = str(row["test"])
        tree = ast.parse(implementation + "\n" + test_source)
        names = function_inputs(tree)
        assignments = input_assignments(ast.parse(test_source), names)
        inputs = []
        prefix_lines = []
        materialize_lines = []
        for name in names:
            value = assignments[name][0]
            path = f"gitchameleon/{example_id}/{name}"
            body, decoder = encode_numeric_vector(value)
            inputs.append({"name": name, "path": path, "body": body, "decoder": decoder})
            raw_name = f"_host_{name}"
            prefix_lines.append(f'{raw_name} = sources.read("{path}")')
            materialize_lines.append(f"{name} = np.array([{decoder}(_item) for _item in {raw_name}.split(\",\")])")
        prefix = "\n".join(prefix_lines) + "\n"
        suffix = "\n".join(materialize_lines) + "\n" + implementation.rstrip() + "\n" + remove_input_assignments(test_source, assignments).rstrip() + f'\nresult = {{"example_id": "{example_id}", "oracle": "passed"}}\n'
        suffix_tokens = len(encoding.encode(suffix))
        tasks.append({
            "example_id": example_id,
            "dataset_row": dataset_row,
            "target_numpy_version": row["version"],
            "api": row["name_of_class_or_func"],
            "inputs": inputs,
            "prefix": prefix,
            "suffix": suffix,
            "suffix_tokens": suffix_tokens,
            "source_sha256": "sha256:" + hashlib.sha256((prefix + suffix).encode()).hexdigest(),
            "cells": [{"tokens_per_second": rate, "source_window_ms": round(suffix_tokens * 1000 / rate)} for rate in RATES],
            "oracle": "public assertions plus terminal result sentinel",
        })
    return {
        "schema_version": SCHEMA,
        "dataset": {
            "name": "GitChameleonBenchmark",
            "commit": DATASET_COMMIT,
            "sha256": DATASET_SHA256,
            "scanned_rows": len(rows),
            "selection_rule": "all NumPy rows with no additional dependency, a public assertion oracle, and literal array inputs used by the function under test",
        },
        "mock_stream": {"tokenizer": tokenizer_name, "tokenizer_version": tokenizer_version, "batch_size": 1, "rates_tokens_per_second": list(RATES), "clock_scope": "complete Host-input prefix at t=0, retained source suffix at fixed mock token rate"},
        "provider_delay_ms": 200,
        "task_count": len(tasks),
        "tasks": tasks,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dataset", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--tokenizer", default="cl100k_base")
    args = parser.parse_args()
    tiktoken = importlib.import_module("tiktoken")
    payload = build(args.dataset, args.tokenizer, tiktoken.__version__)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(json.dumps({"output": str(args.output), "tasks": payload["task_count"]}, sort_keys=True))


if __name__ == "__main__":
    main()
