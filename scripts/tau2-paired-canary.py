#!/usr/bin/env python3
"""Private paired tau2 airline/3 canary: direct tools vs Pysolate Python turns."""

import argparse
import ast
import json
import os
import pathlib
import re
import subprocess
import sys
from typing import Any, Optional

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
MODEL_DEFAULT = "deepseek/deepseek-chat"
TASK_ID = "3"


def parse_program_action(text: str) -> dict[str, Any]:
    value = text.strip()
    fenced = re.fullmatch(r"```(?:json)?\s*(.*?)\s*```", value, re.DOTALL)
    if fenced:
        value = fenced.group(1)
    action = json.loads(value)
    if not isinstance(action, dict) or set(action) not in ({"kind", "source"}, {"kind", "content"}):
        raise ValueError("model action must have exactly kind+source or kind+content")
    if action["kind"] == "answer":
        if set(action) != {"kind", "content"} or not isinstance(action["content"], str) or not action["content"].strip():
            raise ValueError("invalid answer action")
        return action
    if action["kind"] != "program" or set(action) != {"kind", "source"} or not isinstance(action["source"], str):
        raise ValueError("invalid program action")
    capability, arguments = inspect_single_tool_program(action["source"])
    return {**action, "capability": capability, "arguments": arguments}


def inspect_single_tool_program(source: str) -> tuple[str, dict[str, str]]:
    if not source or len(source.encode()) > 16 * 1024:
        raise ValueError("source outside bounded size")
    tree = ast.parse(source)
    if len(tree.body) != 1 or not isinstance(tree.body[0], ast.Assign) or len(tree.body[0].targets) != 1:
        raise ValueError("program must be one assignment")
    assignment = tree.body[0]
    if not isinstance(assignment.targets[0], ast.Name) or assignment.targets[0].id != "result":
        raise ValueError("program must assign result")
    call = assignment.value
    if not isinstance(call, ast.Call) or call.keywords or len(call.args) != 1:
        raise ValueError("program must contain one positional tool call")
    if not isinstance(call.func, ast.Attribute) or not isinstance(call.func.value, ast.Name) or call.func.value.id != "tools":
        raise ValueError("program must call tools")
    if not isinstance(call.args[0], ast.Constant) or not isinstance(call.args[0].value, str):
        raise ValueError("tool argument must be a string literal")
    allowed = {
        "get_reservation_details": ("tau2.airline.get_reservation_details", "reservation_id", "JMO1MG"),
        "get_user_details": ("tau2.airline.get_user_details", "user_id", "anya_garcia_5901"),
    }
    contract = allowed.get(call.func.attr)
    if contract is None or call.args[0].value != contract[2]:
        raise ValueError("tool or exact task-local resource is not allowed")
    return contract[0], {contract[1]: contract[2]}


def bridge_turn(source: str, capability: str, arguments: dict[str, str], config: dict[str, str], index: int) -> dict[str, Any]:
    root = pathlib.Path(config["evidence_root"])
    source_path = root / f"treatment-turn-{index:02d}.py"
    output_path = root / f"treatment-turn-{index:02d}.json"
    source_path.write_text(source)
    source_path.chmod(0o600)
    env = os.environ.copy()
    env.update({
        "AGENT_RUNTIME_GUEST": config["artifact"],
        "PYSOLATE_TAU2_PYTHON": config["tau2_python"],
        "PYSOLATE_TAU2_SOURCE_ROOT": config["source_root"],
        "PYSOLATE_TAU2_DYNAMIC_SOURCE_FILE": str(source_path),
        "PYSOLATE_TAU2_DYNAMIC_OUTPUT_FILE": str(output_path),
        "PYSOLATE_TAU2_EXPECTED_CAPABILITY": capability,
        "PYSOLATE_TAU2_EXPECTED_ARGUMENTS": json.dumps(arguments, sort_keys=True, separators=(",", ":")),
    })
    completed = subprocess.run(
        ["go", "test", "./integration/e2e", "-run", "^TestTau2DynamicModelTurnThroughRealGuest$", "-count=1"],
        cwd=config["repo_root"], env=env, capture_output=True, text=True, timeout=180,
    )
    if completed.returncode != 0:
        raise RuntimeError("Pysolate dynamic turn failed: " + completed.stderr[-1000:])
    result = json.loads(output_path.read_text())
    if result.get("schema_version") != "pysolate.tau2-dynamic-turn-private.v1":
        raise ValueError("invalid dynamic turn output")
    receipt = result.get("receipt", {})
    if receipt.get("outcome") != "ok" or not isinstance(receipt.get("source"), dict):
        raise ValueError("dynamic turn lacks bound successful receipt")
    return result


PROGRAM_AGENT_INSTRUCTION = """
You are a customer service agent. Follow the policy exactly.
You do not have native function calling. Every response MUST be exactly one JSON object, without markdown.
To read one tool, respond {"kind":"program","source":"result = tools.get_reservation_details('JMO1MG')"}
or {"kind":"program","source":"result = tools.get_user_details('anya_garcia_5901')"}.
A program may contain exactly one tool call and is executed in a fresh isolated Guest. Its result will be returned as a runtime observation.
When you have enough information, respond {"kind":"answer","content":"your user-facing answer"}.
Never place both a program and an answer in one response. Do not invent tool results.
""".strip()


def build_tau_components():
    from tau2.runner import build_environment, build_user, get_tasks
    tasks = get_tasks("airline", task_ids=[TASK_ID])
    if len(tasks) != 1:
        raise ValueError("exact task not found")
    environment = build_environment("airline")
    return environment, tasks[0], build_user


def make_program_agent(config: dict[str, str], model: str, llm_args: dict[str, Any]):
    from pydantic import BaseModel
    from tau2.agent.base.llm_config import LLMConfigMixin
    from tau2.agent.base_agent import HalfDuplexAgent
    from tau2.data_model.message import AssistantMessage, Message, SystemMessage, UserMessage
    from tau2.utils.llm_utils import generate

    class ProgramState(BaseModel):
        system_messages: list[SystemMessage]
        messages: list[Any]

    class ProgramAgent(LLMConfigMixin, HalfDuplexAgent[ProgramState]):
        def __init__(self, domain_policy: str):
            super().__init__(tools=[], domain_policy=domain_policy, llm=model, llm_args=llm_args)
            self.events: list[dict[str, Any]] = []

        def get_init_state(self, message_history: Optional[list[Message]] = None) -> ProgramState:
            system = SystemMessage(role="system", content=PROGRAM_AGENT_INSTRUCTION + "\n\n<policy>\n" + self.domain_policy + "\n</policy>")
            return ProgramState(system_messages=[system], messages=list(message_history or []))

        def generate_next_message(self, message: Any, state: ProgramState):
            state.messages.append(message)
            for _ in range(4):
                response = generate(model=self.llm, messages=state.system_messages + state.messages, call_name="pysolate_agent_response", **self.llm_args)
                raw = response.content or ""
                try:
                    action = parse_program_action(raw)
                except (SyntaxError, ValueError, json.JSONDecodeError) as exc:
                    self.events.append({"model_response": raw, "kind": "invalid_model_action", "error_type": type(exc).__name__})
                    invalid = AssistantMessage(role="assistant", content=None)
                    state.messages.append(invalid)
                    return invalid, state
                event: dict[str, Any] = {"model_response": raw, "kind": action["kind"]}
                if action["kind"] == "answer":
                    final = AssistantMessage(role="assistant", content=action["content"])
                    state.messages.append(final)
                    self.events.append(event)
                    return final, state
                turn = bridge_turn(action["source"], action["capability"], action["arguments"], config, len(self.events) + 1)
                event.update({"source": action["source"], "capability": action["capability"], "arguments": action["arguments"], "turn": turn})
                self.events.append(event)
                state.messages.append(AssistantMessage(role="assistant", content=raw))
                state.messages.append(UserMessage(role="user", content="<runtime_observation>" + turn["content"] + "</runtime_observation>\nContinue with one JSON action."))
            raise RuntimeError("program agent exceeded four internal actions")

    return ProgramAgent


def run_lane(lane: str, args) -> dict[str, Any]:
    from tau2.evaluator.evaluator import EvaluationType
    from tau2.orchestrator.orchestrator import Orchestrator
    from tau2.runner import build_agent, run_simulation

    environment, task, build_user = build_tau_components()
    llm_args = {"temperature": 0.0}
    user = build_user("user_simulator", environment, task, llm=args.model, llm_args=dict(llm_args))
    events = None
    if lane == "direct":
        agent = build_agent("llm_agent", environment, llm=args.model, llm_args=dict(llm_args), task=task)
    else:
        config = {
            "repo_root": args.repo_root, "source_root": args.source_root, "tau2_python": sys.executable,
            "artifact": args.artifact, "evidence_root": args.evidence_root,
        }
        ProgramAgent = make_program_agent(config, args.model, dict(llm_args))
        agent = ProgramAgent(environment.get_policy())
        events = agent.events
    orchestrator = Orchestrator(
        domain="airline", agent=agent, user=user, environment=environment, task=task,
        max_steps=args.max_steps, max_errors=2, seed=args.seed, validate_communication=True, timeout=600,
        simulation_id=f"pysolate-paired-{lane}-seed-{args.seed}",
    )
    try:
        result = run_simulation(orchestrator, evaluation_type=EvaluationType.ALL)
    except ValueError as exc:
        payload = {
            "schema_version": "pysolate.tau2-paired-private.v1", "source_revision": REVISION,
            "lane": lane, "model": args.model, "seed": args.seed, "temperature": 0.0,
            "status": "unscorable", "failure_stage": "orchestrator_protocol_validation",
            "exception_type": type(exc).__name__, "simulation": None, "pysolate_events": events,
        }
        path = pathlib.Path(args.evidence_root) / f"{lane}-result.json"
        path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
        path.chmod(0o600)
        return {"lane": lane, "status": "unscorable", "reward": None, "pysolate_events": len(events or [])}
    raw = result.model_dump(mode="json")
    payload = {
        "schema_version": "pysolate.tau2-paired-private.v1", "source_revision": REVISION,
        "lane": lane, "model": args.model, "seed": args.seed, "temperature": 0.0,
        "status": "completed", "simulation": raw, "pysolate_events": events,
    }
    path = pathlib.Path(args.evidence_root) / f"{lane}-result.json"
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
    path.chmod(0o600)
    reward = result.reward_info.reward if result.reward_info else None
    return {"lane": lane, "reward": reward, "messages": len(result.messages), "pysolate_events": len(events or [])}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--lane", choices=["direct", "treatment"], required=True)
    parser.add_argument("--model", default=MODEL_DEFAULT)
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--max-steps", type=int, default=12)
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--repo-root", required=True)
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--evidence-root", required=True)
    args = parser.parse_args()
    root = pathlib.Path(args.evidence_root)
    root.mkdir(parents=True, exist_ok=True)
    root.chmod(0o700)
    if pathlib.Path(args.source_root).resolve() != pathlib.Path.cwd().resolve():
        raise ValueError("run from exact tau2 source root")
    summary = run_lane(args.lane, args)
    print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
