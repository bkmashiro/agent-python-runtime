#!/usr/bin/env python3
"""Convert pinned upstream reference programs/calls into Pysolate corpus v1 JSONL."""

import argparse
import gzip
import json
import keyword
import os
from pathlib import Path
import re
import tempfile


SCHEMA_VERSION = 1


def read_jsonl(path):
    opener = gzip.open if str(path).endswith(".gz") else open
    with opener(path, "rt", encoding="utf-8") as stream:
        for line_number, line in enumerate(stream, 1):
            if not line.strip():
                continue
            try:
                yield json.loads(line)
            except json.JSONDecodeError as error:
                raise ValueError("invalid JSON at {}:{}: {}".format(path, line_number, error)) from error


def write_cases(path, cases):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            for case in cases:
                stream.write(json.dumps(case, sort_keys=True, separators=(",", ":")))
                stream.write("\n")
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def import_humaneval(input_path, output_path, revision, limit):
    cases = []
    for row in read_jsonl(input_path):
        task_id = require_string(row, "task_id")
        prompt = require_string(row, "prompt")
        solution = require_string(row, "canonical_solution")
        tests = require_string(row, "test")
        entry_point = require_identifier(row, "entry_point")
        source = "{}{}\n{}\ncheck({})\nresult = {{\"passed\": True}}\n".format(
            prompt, solution, tests, entry_point
        )
        compile(source, "<{}>".format(task_id), "exec")
        cases.append({
            "schema_version": SCHEMA_VERSION,
            "id": "human-eval/{}".format(task_id),
            "source": source,
            "inputs": {},
            "expected": {"passed": True},
            "origin": {
                "dataset": "openai/human-eval",
                "revision": revision,
                "case_id": task_id,
            },
        })
        if limit and len(cases) >= limit:
            break
    if not cases:
        raise ValueError("HumanEval input produced no cases")
    write_cases(output_path, cases)
    return len(cases)


def import_bfcl(question_path, answer_path, output_path, revision, category, limit):
    answers = {}
    for answer in read_jsonl(answer_path):
        case_id = require_string(answer, "id")
        if case_id in answers:
            raise ValueError("duplicate BFCL answer id: {}".format(case_id))
        answers[case_id] = answer
    cases = []
    for question in read_jsonl(question_path):
        case_id = require_string(question, "id")
        answer = answers.get(case_id)
        cases.append(convert_bfcl_case(question, answer, revision, category))
        if limit and len(cases) >= limit:
            break
    if not cases:
        raise ValueError("BFCL input produced no cases")
    write_cases(output_path, cases)
    return len(cases)


def convert_bfcl_case(question, answer, revision, category):
    case_id = require_string(question, "id")
    if answer is None or answer.get("id") != case_id:
        raise ValueError("missing BFCL ground truth for {}".format(case_id))
    definitions = question.get("function")
    if not isinstance(definitions, list) or not definitions:
        raise ValueError("BFCL case {} has no function definitions".format(case_id))
    tools = []
    by_name = {}
    paths = set()
    for definition in definitions:
        name = require_string(definition, "name")
        if name in by_name:
            raise ValueError("duplicate BFCL function name: {}".format(name))
        python_path = "bfcl." + normalize_python_path(name)
        if python_path in paths:
            raise ValueError("BFCL Python path collision: {}".format(python_path))
        paths.add(python_path)
        schema = normalize_schema(definition.get("parameters", {"type": "object"}))
        if not isinstance(schema, dict):
            raise ValueError("BFCL parameters must be an object: {}".format(name))
        tools.append({
            "name": "bfcl/" + name,
            "python_path": python_path,
            "description": str(definition.get("description", "")),
            "input_schema": schema,
            "allow_early_read": True,
        })
        by_name[name] = python_path
    ground_truth = answer.get("ground_truth")
    if not isinstance(ground_truth, list) or not ground_truth:
        raise ValueError("BFCL case {} has no ground_truth calls".format(case_id))
    source_lines = ["_results = []"]
    replay_by_key = {}
    replay_order = []
    expected = []
    for call in ground_truth:
        if not isinstance(call, dict) or len(call) != 1:
            raise ValueError("BFCL ground_truth call must name exactly one function")
        name, candidates = next(iter(call.items()))
        if name not in by_name:
            raise ValueError("BFCL ground_truth references unknown function: {}".format(name))
        if not isinstance(candidates, dict):
            raise ValueError("BFCL ground_truth arguments must be an object")
        arguments = {}
        for argument, values in candidates.items():
            if not isinstance(argument, str) or not argument.isidentifier() or keyword.iskeyword(argument):
                raise ValueError("invalid Python keyword argument: {}".format(argument))
            if not isinstance(values, list) or not values:
                raise ValueError("BFCL argument alternatives must be a non-empty list: {}".format(argument))
            arguments[argument] = values[0]
        rendered = ", ".join("{}={}".format(key, repr(value)) for key, value in arguments.items())
        source_lines.append("_results.append({}({}))".format(by_name[name], rendered))
        receipt = {"tool": name, "args": arguments}
        expected.append(receipt)
        canonical_args = json.dumps(arguments, sort_keys=True, separators=(",", ":"))
        key = (name, canonical_args)
        if key not in replay_by_key:
            entry = {
                "tool": "bfcl/" + name,
                "args": arguments,
                "value": receipt,
                "count": 1,
            }
            replay_by_key[key] = entry
            replay_order.append(entry)
        else:
            replay_by_key[key]["count"] += 1
    source_lines.append("result = _results")
    source = "\n".join(source_lines) + "\n"
    compile(source, "<{}>".format(case_id), "exec")
    return {
        "schema_version": SCHEMA_VERSION,
        "id": "bfcl/{}/{}".format(category, case_id),
        "source": source,
        "inputs": {},
        "tools": tools,
        "replay": replay_order,
        "expected": expected,
        "origin": {
            "dataset": "gorilla-llm/BFCL",
            "revision": revision,
            "case_id": case_id,
        },
    }


def normalize_python_path(name):
    normalized = []
    for segment in name.split("."):
        segment = re.sub(r"[^A-Za-z0-9_]", "_", segment)
        if not segment or segment[0].isdigit():
            segment = "_" + segment
        if keyword.iskeyword(segment):
            segment += "_"
        if not segment.isidentifier():
            raise ValueError("cannot normalize BFCL function path: {}".format(name))
        normalized.append(segment)
    return ".".join(normalized)


def normalize_schema(value):
    if isinstance(value, dict):
        normalized = {key: normalize_schema(item) for key, item in value.items()}
        aliases = {"dict": "object", "list": "array", "float": "number", "int": "integer", "bool": "boolean", "str": "string"}
        if isinstance(normalized.get("type"), str):
            normalized["type"] = aliases.get(normalized["type"], normalized["type"])
        return normalized
    if isinstance(value, list):
        return [normalize_schema(item) for item in value]
    return value


def require_string(mapping, key):
    value = mapping.get(key)
    if not isinstance(value, str) or not value:
        raise ValueError("{} must be a non-empty string".format(key))
    return value


def require_identifier(mapping, key):
    value = require_string(mapping, key)
    if not value.isidentifier() or keyword.iskeyword(value):
        raise ValueError("{} must be a Python identifier".format(key))
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    human = subparsers.add_parser("humaneval")
    human.add_argument("--input", type=Path, required=True)
    human.add_argument("--output", type=Path, required=True)
    human.add_argument("--revision", required=True)
    human.add_argument("--limit", type=int, default=0)
    bfcl = subparsers.add_parser("bfcl")
    bfcl.add_argument("--questions", type=Path, required=True)
    bfcl.add_argument("--answers", type=Path, required=True)
    bfcl.add_argument("--output", type=Path, required=True)
    bfcl.add_argument("--revision", required=True)
    bfcl.add_argument("--category", required=True)
    bfcl.add_argument("--limit", type=int, default=0)
    arguments = parser.parse_args()
    if arguments.limit < 0:
        parser.error("--limit must be non-negative")
    if arguments.command == "humaneval":
        count = import_humaneval(arguments.input, arguments.output, arguments.revision, arguments.limit)
    else:
        count = import_bfcl(arguments.questions, arguments.answers, arguments.output, arguments.revision, arguments.category, arguments.limit)
    print("wrote {} frozen cases to {}".format(count, arguments.output))


if __name__ == "__main__":
    main()
