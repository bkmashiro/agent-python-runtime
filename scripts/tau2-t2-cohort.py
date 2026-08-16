#!/usr/bin/env python3
"""Run one frozen tau2 T2 cohort cell with a hard provider-call budget."""

import argparse
import ast
import json
import os
import pathlib
import re
import subprocess
import sys
import traceback
from typing import Any, Optional

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
MODEL = "deepseek/deepseek-v4-pro"
PRIVATE_SCHEMA = "pysolate.tau2-t2-private-preregistration.v1"
SIGNATURES = {
    "get_reservation_details": ["reservation_id"],
    "get_user_details": ["user_id"],
    "search_direct_flight": ["origin", "destination", "date"],
}


class ProviderBudgetExceeded(RuntimeError):
    pass


def resolve_paths(args: argparse.Namespace) -> argparse.Namespace:
    args.tau2_python = os.path.abspath(args.tau2_python)
    for name in ("source_root", "repo_root", "artifact", "private_manifest", "evidence_root"):
        setattr(args, name, str(pathlib.Path(getattr(args, name)).resolve()))
    return args


class ProviderBudget:
    def __init__(self, maximum: int):
        self.maximum = maximum
        self.calls = 0

    def call(self, function, *args, **kwargs):
        if self.calls >= self.maximum:
            raise ProviderBudgetExceeded("preregistered provider-call budget exhausted")
        self.calls += 1
        return function(*args, **kwargs)


def canonical(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def load_task_contract(path: pathlib.Path, task_id: str) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    value = json.loads(path.read_text())
    if value.get("schema_version") != PRIVATE_SCHEMA or value.get("source", {}).get("revision") != REVISION:
        raise ValueError("private preregistration mismatch")
    matches = [item for item in value.get("tasks", []) if item.get("task_id") == task_id]
    if len(matches) != 1:
        raise ValueError("task is outside frozen cohort")
    protocol = value.get("protocol")
    if not isinstance(protocol, dict) or protocol.get("model") != MODEL or protocol.get("post_provider_reruns") != 0:
        raise ValueError("frozen protocol mismatch")
    return protocol, matches[0]["reference_actions"]


def inspect_program(source: str, allowed_actions: list[dict[str, Any]]) -> dict[str, Any]:
    if not source or len(source.encode()) > 16 * 1024:
        raise ValueError("source outside bounded size")
    tree = ast.parse(source)
    if len(tree.body) != 1 or not isinstance(tree.body[0], ast.Assign) or len(tree.body[0].targets) != 1:
        raise ValueError("program must be one assignment")
    assignment = tree.body[0]
    if not isinstance(assignment.targets[0], ast.Name) or assignment.targets[0].id != "result":
        raise ValueError("program must assign result")
    call = assignment.value
    if not isinstance(call, ast.Call) or not isinstance(call.func, ast.Attribute) or not isinstance(call.func.value, ast.Name) or call.func.value.id != "tools":
        raise ValueError("program must contain one tools call")
    names = SIGNATURES.get(call.func.attr)
    if names is None or len(call.args) + len(call.keywords) != len(names):
        raise ValueError("tool signature mismatch")
    arguments: dict[str, str] = {}
    if call.args:
        if call.keywords or len(call.args) != len(names):
            raise ValueError("do not mix positional and keyword arguments")
        for name, node in zip(names, call.args):
            if not isinstance(node, ast.Constant) or not isinstance(node.value, str):
                raise ValueError("tool arguments must be string literals")
            arguments[name] = node.value
    else:
        for keyword in call.keywords:
            if keyword.arg is None or keyword.arg not in names or keyword.arg in arguments or not isinstance(keyword.value, ast.Constant) or not isinstance(keyword.value.value, str):
                raise ValueError("invalid keyword argument")
            arguments[keyword.arg] = keyword.value.value
        if set(arguments) != set(names):
            raise ValueError("missing keyword argument")
        arguments = {name: arguments[name] for name in names}
    if not any(action.get("name") == call.func.attr and action.get("arguments") == arguments for action in allowed_actions):
        raise ValueError("tool call is outside frozen task reference scope")
    return {"source": source, "tool": call.func.attr, "capability": "tau2.airline." + call.func.attr, "arguments": arguments, "argument_names": names}


def parse_action(text: str, allowed_actions: list[dict[str, Any]]) -> dict[str, Any]:
    value = text.strip()
    fenced = re.fullmatch(r"```(?:json)?\s*(.*?)\s*```", value, re.DOTALL)
    if fenced:
        value = fenced.group(1)
    action = json.loads(value)
    if not isinstance(action, dict) or set(action) not in ({"kind", "source"}, {"kind", "content"}):
        raise ValueError("model action must be exactly kind+source or kind+content")
    if action["kind"] == "answer":
        if set(action) != {"kind", "content"} or not isinstance(action["content"], str) or not action["content"].strip():
            raise ValueError("invalid answer action")
        return action
    if action["kind"] != "program" or not isinstance(action.get("source"), str):
        raise ValueError("invalid program action")
    return {"kind": "program", **inspect_program(action["source"], allowed_actions)}


def bridge_turn(action: dict[str, Any], config: dict[str, str], index: int) -> dict[str, Any]:
    root = pathlib.Path(config["evidence_root"])
    source_path = root / f"turn-{index:02d}.py"
    output_path = root / f"turn-{index:02d}.json"
    source_path.write_text(action["source"])
    source_path.chmod(0o600)
    env = os.environ.copy()
    env.update({
        "AGENT_RUNTIME_GUEST": config["artifact"], "PYSOLATE_TAU2_PYTHON": config["tau2_python"],
        "PYSOLATE_TAU2_SOURCE_ROOT": config["source_root"], "PYSOLATE_TAU2_T2_PRIVATE_MANIFEST": config["private_manifest"],
        "PYSOLATE_TAU2_T2_TASK_ID": config["task_id"], "PYSOLATE_TAU2_DYNAMIC_SOURCE_FILE": str(source_path),
        "PYSOLATE_TAU2_DYNAMIC_OUTPUT_FILE": str(output_path), "PYSOLATE_TAU2_EXPECTED_CAPABILITY": action["capability"],
        "PYSOLATE_TAU2_EXPECTED_ARGUMENTS": canonical(action["arguments"]), "PYSOLATE_TAU2_EXPECTED_ARGUMENT_NAMES": canonical(action["argument_names"]),
    })
    completed = subprocess.run(
        ["go", "test", "./integration/e2e", "-run", "^TestTau2T2DynamicModelTurnThroughRealGuest$", "-count=1"],
        cwd=config["repo_root"], env=env, capture_output=True, text=True, timeout=240,
    )
    if completed.returncode != 0:
        raise RuntimeError("T2 dynamic Guest turn failed: " + (completed.stderr + completed.stdout)[-1200:])
    result = json.loads(output_path.read_text())
    receipt = result.get("receipt", {})
    if result.get("schema_version") != "pysolate.tau2-t2-dynamic-turn-private.v1" or result.get("task_id") != config["task_id"] or receipt.get("outcome") != "ok" or not isinstance(receipt.get("source"), dict):
        raise ValueError("T2 dynamic turn lacks successful source-bound receipt")
    return result


def instruction_for(actions: list[dict[str, Any]]) -> str:
    tools = sorted(set(action["name"] for action in actions))
    signatures = [f"tools.{tool}({', '.join(repr(name) for name in SIGNATURES[tool])})" for tool in tools]
    return (
        "You are a customer service agent. Follow the policy exactly. You do not have native function calling. "
        "Every response MUST be exactly one JSON object without markdown. To perform one READ, respond "
        '{"kind":"program","source":"result = tools.method(\\"literal\\")"}. '
        "A program is one assignment and one task-scoped READ with string literals. Available signatures: "
        + "; ".join(signatures)
        + '. To speak to the user, ask a question, or finish, respond {"kind":"answer","content":"..."}. '
        "Never combine a program and user-facing text. Never invent a runtime observation."
    )


def make_program_agent(config: dict[str, str], allowed_actions: list[dict[str, Any]], budget: ProviderBudget, llm_args: dict[str, Any]):
    from pydantic import BaseModel
    from tau2.agent.base.llm_config import LLMConfigMixin
    from tau2.agent.base_agent import HalfDuplexAgent
    from tau2.data_model.message import AssistantMessage, Message, SystemMessage, UserMessage
    from tau2.utils.llm_utils import generate as original_generate

    class ProgramState(BaseModel):
        system_messages: list[SystemMessage]
        messages: list[Any]

    class ProgramAgent(LLMConfigMixin, HalfDuplexAgent[ProgramState]):
        def __init__(self, domain_policy: str):
            super().__init__(tools=[], domain_policy=domain_policy, llm=MODEL, llm_args=llm_args)
            self.events: list[dict[str, Any]] = []

        def get_init_state(self, message_history: Optional[list[Message]] = None) -> ProgramState:
            system = SystemMessage(role="system", content=instruction_for(allowed_actions) + "\n\n<policy>\n" + self.domain_policy + "\n</policy>")
            return ProgramState(system_messages=[system], messages=list(message_history or []))

        def generate_next_message(self, message: Any, state: ProgramState):
            state.messages.append(message)
            while True:
                if len(self.events) >= 16:
                    raise ProviderBudgetExceeded("preregistered agent action budget exhausted")
                response = budget.call(
                    original_generate, model=self.llm, messages=state.system_messages + state.messages,
                    call_name="pysolate_t2_agent_response", response_format={"type": "json_object"}, **self.llm_args,
                )
                raw = response.content or ""
                event: dict[str, Any] = {"model_response": raw}
                try:
                    action = parse_action(raw, allowed_actions)
                except (SyntaxError, ValueError, json.JSONDecodeError) as exc:
                    event.update({"kind": "invalid_model_action", "error_type": type(exc).__name__})
                    self.events.append(event)
                    invalid = AssistantMessage(role="assistant", content=raw or "[empty invalid model action]")
                    state.messages.append(invalid)
                    return invalid, state
                event["kind"] = action["kind"]
                if action["kind"] == "answer":
                    final = AssistantMessage(role="assistant", content=action["content"])
                    state.messages.append(final)
                    self.events.append(event)
                    return final, state
                turn = bridge_turn(action, config, len(self.events) + 1)
                event.update({"source": action["source"], "tool": action["tool"], "arguments": action["arguments"], "turn": turn})
                self.events.append(event)
                state.messages.append(AssistantMessage(role="assistant", content=raw))
                state.messages.append(UserMessage(role="user", content="<runtime_observation>" + turn["content"] + "</runtime_observation>\nContinue with one JSON action."))

    return ProgramAgent


def run_cell(args) -> dict[str, Any]:
    from tau2.agent import llm_agent as llm_agent_module
    from tau2.data_model.message import AssistantMessage, ToolCall
    from tau2.evaluator.evaluator import EvaluationType
    from tau2.evaluator.evaluator_action import ActionEvaluator
    from tau2.orchestrator.orchestrator import Orchestrator
    from tau2.runner import build_agent, build_environment, build_user, get_tasks, run_simulation
    from tau2.user import user_simulator as user_module

    protocol, allowed_actions = load_task_contract(pathlib.Path(args.private_manifest), args.task_id)
    environment = build_environment("airline")
    tasks = get_tasks("airline", task_ids=[args.task_id])
    if len(tasks) != 1:
        raise ValueError("frozen task missing")
    task = tasks[0]
    budget = ProviderBudget(int(protocol["max_total_provider_invocations_per_trial"]))
    llm_args = {"temperature": 0.0}
    original_agent_generate = llm_agent_module.generate
    original_user_generate = user_module.generate
    llm_agent_module.generate = lambda *a, **kw: budget.call(original_agent_generate, *a, **kw)
    user_module.generate = lambda *a, **kw: budget.call(original_user_generate, *a, **kw)
    events = None
    try:
        user = build_user("user_simulator", environment, task, llm=MODEL, llm_args=dict(llm_args))
        if args.lane == "direct":
            agent = build_agent("llm_agent", environment, llm=MODEL, llm_args=dict(llm_args), task=task)
        else:
            config = {
                "repo_root": args.repo_root, "source_root": args.source_root, "tau2_python": args.tau2_python,
                "artifact": args.artifact, "evidence_root": args.evidence_root, "private_manifest": args.private_manifest, "task_id": args.task_id,
            }
            ProgramAgent = make_program_agent(config, allowed_actions, budget, dict(llm_args))
            agent = ProgramAgent(environment.get_policy())
            events = agent.events
        orchestrator = Orchestrator(
            domain="airline", agent=agent, user=user, environment=environment, task=task,
            max_steps=int(protocol["max_steps"]), max_errors=int(protocol["max_errors"]), seed=int(protocol["seed"]),
            validate_communication=True, timeout=600, simulation_id=f"pysolate-t2-{args.task_id}-{args.lane}-seed-{protocol['seed']}",
        )
        result = run_simulation(orchestrator, evaluation_type=EvaluationType.ALL)
        if args.lane == "direct":
            diagnostic_messages = result.messages
        else:
            calls = [ToolCall(id=f"treatment-{index}", name=event["tool"], arguments=event["arguments"]) for index, event in enumerate(events or []) if event.get("kind") == "program"]
            diagnostic_messages = [AssistantMessage(role="assistant", content=None, tool_calls=calls)] if calls else []
        action_info = ActionEvaluator.calculate_reward(task, diagnostic_messages).model_dump(mode="json")
        return {
            "schema_version": "pysolate.tau2-t2-cell-private.v1", "source_revision": REVISION,
            "task_id": args.task_id, "lane": args.lane, "model": MODEL, "seed": protocol["seed"], "temperature": 0.0,
            "status": "completed", "provider_calls": budget.calls, "simulation": result.model_dump(mode="json"),
            "official_action_diagnostic": action_info, "pysolate_events": events,
        }
    except Exception as exc:
        frames = [{"function": frame.name, "line": frame.lineno} for frame in traceback.extract_tb(exc.__traceback__)]
        return {
            "schema_version": "pysolate.tau2-t2-cell-private.v1", "source_revision": REVISION,
            "task_id": args.task_id, "lane": args.lane, "model": MODEL, "seed": protocol["seed"], "temperature": 0.0,
            "status": "unscorable", "provider_calls": budget.calls, "simulation": None, "official_action_diagnostic": None,
            "pysolate_events": events, "failure": {"exception_type": type(exc).__name__, "message": str(exc), "frames": frames},
        }
    finally:
        llm_agent_module.generate = original_agent_generate
        user_module.generate = original_user_generate


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--lane", choices=["direct", "programmatic_python"], required=True)
    parser.add_argument("--task-id", required=True)
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--repo-root", required=True)
    parser.add_argument("--tau2-python", required=True)
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--private-manifest", required=True)
    parser.add_argument("--evidence-root", required=True)
    args = resolve_paths(parser.parse_args())
    root = pathlib.Path(args.evidence_root)
    root.mkdir(parents=True, exist_ok=True)
    root.chmod(0o700)
    payload = run_cell(args)
    path = root / f"task-{args.task_id}-{args.lane}.json"
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
    path.chmod(0o600)
    print(json.dumps({"task_id": args.task_id, "lane": args.lane, "status": payload["status"], "provider_calls": payload["provider_calls"]}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
