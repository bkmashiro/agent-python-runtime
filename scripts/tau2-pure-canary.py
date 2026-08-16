#!/usr/bin/env python3
"""Private paired tau2 retail/24 pure communication canary."""

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
MODEL_DEFAULT = "deepseek/deepseek-v4-pro"
TASK_ID = "24"


def parse_pure_program_action(text: str) -> dict[str, str]:
    value = text.strip()
    fenced = re.fullmatch(r"```(?:json)?\s*(.*?)\s*```", value, re.DOTALL)
    if fenced:
        value = fenced.group(1)
    action = json.loads(value)
    if not isinstance(action, dict) or set(action) != {"kind", "source"} or action.get("kind") != "program":
        raise ValueError("model action must contain exactly kind=program and source")
    source = action.get("source")
    if not isinstance(source, str) or not source or len(source.encode()) > 16 * 1024:
        raise ValueError("source outside bounded size")
    tree = ast.parse(source)
    if len(tree.body) != 1 or not isinstance(tree.body[0], ast.Assign) or len(tree.body[0].targets) != 1:
        raise ValueError("pure program must be one assignment")
    assignment = tree.body[0]
    if not isinstance(assignment.targets[0], ast.Name) or assignment.targets[0].id != "result":
        raise ValueError("pure program must assign result")
    try:
        content = ast.literal_eval(assignment.value)
    except (ValueError, SyntaxError) as error:
        raise ValueError("pure result must be a literal") from error
    if not isinstance(content, str) or not content.strip():
        raise ValueError("pure result must be a non-empty string")
    return {"kind": "program", "source": source, "literal_content": content}


def count_embedded_tool_calls(messages) -> int:
    return sum(len(getattr(message, "tool_calls", None) or []) for message in messages)


def bridge_pure_turn(source: str, config: dict[str, str], index: int) -> dict[str, Any]:
    root = pathlib.Path(config["evidence_root"])
    source_path = root / f"treatment-turn-{index:02d}.py"
    output_path = root / f"treatment-turn-{index:02d}.json"
    source_path.write_text(source)
    source_path.chmod(0o600)
    env = os.environ.copy()
    env.update({
        "AGENT_RUNTIME_GUEST": config["artifact"],
        "PYSOLATE_TAU2_PURE_SOURCE_FILE": str(source_path),
        "PYSOLATE_TAU2_PURE_OUTPUT_FILE": str(output_path),
    })
    completed = subprocess.run(
        ["go", "test", "./integration/e2e", "-run", "^TestTau2PureDynamicModelTurnThroughZeroCapabilityGuest$", "-count=1"],
        cwd=config["repo_root"], env=env, capture_output=True, text=True, timeout=180,
    )
    if completed.returncode != 0:
        raise RuntimeError("Pysolate pure turn failed: " + (completed.stderr or completed.stdout)[-1000:])
    result = json.loads(output_path.read_text())
    required = {
        "schema_version": "pysolate.tau2-pure-turn-private.v1",
        "semantic_call_sites": 0,
        "broker_call_count": 0,
        "receipt_count": 0,
    }
    if any(result.get(key) != expected for key, expected in required.items()):
        raise ValueError("pure turn lacks zero-authority evidence")
    return result


PROGRAM_AGENT_INSTRUCTION = """
You are a customer service agent. Follow the retail policy exactly and respond naturally to the user.
You have no tools or external capabilities. Every response MUST be exactly one JSON object without markdown:
{"kind":"program","source":"result = 'your complete user-facing response'"}
The source must contain exactly one Python assignment of a non-empty string literal to result. It executes in a fresh isolated Guest with an empty capability Plan. Do not call tools, import modules, or add other statements.
""".strip()


def build_tau_components():
    from tau2.runner import build_environment, build_user, get_tasks
    tasks = get_tasks("retail", task_ids=[TASK_ID])
    if len(tasks) != 1:
        raise ValueError("exact task not found")
    task = tasks[0]
    criteria = task.evaluation_criteria
    if criteria is None or criteria.actions or criteria.env_assertions or len(criteria.communicate_info or []) != 2:
        raise ValueError("task pure shape drift")
    environment = build_environment("retail")
    return environment, task, build_user


def make_program_agent(config: dict[str, str], model: str, llm_args: dict[str, Any]):
    from pydantic import BaseModel
    from tau2.agent.base.llm_config import LLMConfigMixin
    from tau2.agent.base_agent import HalfDuplexAgent
    from tau2.data_model.message import AssistantMessage, Message, SystemMessage
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
            response = generate(
                model=self.llm,
                messages=state.system_messages + state.messages,
                call_name="pysolate_pure_agent_response",
                response_format={"type": "json_object"},
                **self.llm_args,
            )
            raw = response.content or ""
            action = parse_pure_program_action(raw)
            turn = bridge_pure_turn(action["source"], config, len(self.events) + 1)
            if turn["content"] != action["literal_content"]:
                raise ValueError("Guest result differs from admitted literal")
            final = AssistantMessage(role="assistant", content=turn["content"])
            state.messages.append(final)
            self.events.append({"model_response": raw, "source": action["source"], "turn": turn})
            return final, state

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
        config = {"repo_root": args.repo_root, "artifact": args.artifact, "evidence_root": args.evidence_root}
        ProgramAgent = make_program_agent(config, args.model, dict(llm_args))
        agent = ProgramAgent(environment.get_policy())
        events = agent.events
    orchestrator = Orchestrator(
        domain="retail", agent=agent, user=user, environment=environment, task=task,
        max_steps=args.max_steps, max_errors=2, seed=args.seed, validate_communication=True, timeout=600,
        simulation_id=f"pysolate-pure-{lane}-seed-{args.seed}",
    )
    try:
        result = run_simulation(orchestrator, evaluation_type=EvaluationType.COMMUNICATE)
    except Exception as exc:
        frames = [{"function": frame.name, "line": frame.lineno} for frame in traceback.extract_tb(exc.__traceback__)]
        payload = {
            "schema_version": "pysolate.tau2-pure-paired-private.v1", "source_revision": REVISION,
            "lane": lane, "model": args.model, "seed": args.seed, "temperature": 0.0,
            "evaluation_type": "COMMUNICATE", "status": "unscorable",
            "failure_stage": frames[-1]["function"] if frames else "run_simulation",
            "exception_type": type(exc).__name__, "exception_message": str(exc), "traceback_frames": frames,
            "simulation": None, "pysolate_events": events,
        }
        path = pathlib.Path(args.evidence_root) / f"{lane}-result.json"
        path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
        path.chmod(0o600)
        return {"lane": lane, "status": "unscorable", "reward": None, "pysolate_events": len(events or [])}
    tool_calls = count_embedded_tool_calls(result.messages)
    payload = {
        "schema_version": "pysolate.tau2-pure-paired-private.v1", "source_revision": REVISION,
        "lane": lane, "model": args.model, "seed": args.seed, "temperature": 0.0,
        "evaluation_type": "COMMUNICATE", "status": "completed", "simulation": result.model_dump(mode="json"),
        "tool_calls": tool_calls, "pysolate_events": events,
    }
    path = pathlib.Path(args.evidence_root) / f"{lane}-result.json"
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
    path.chmod(0o600)
    reward = result.reward_info.reward if result.reward_info else None
    return {"lane": lane, "status": "completed", "reward": reward, "messages": len(result.messages), "tool_calls": tool_calls, "pysolate_events": len(events or [])}


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
    print(json.dumps(run_lane(args.lane, args), sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
