# M4 next experiment decision

**Status:** decision and bounded follow-up executed on 2026-08-16. The initial spike was PARTIAL; the separately authorized exact `attrs-770` artifact profile is now SUPPORTED. See [`attrs-770-spike-v1.md`](attrs-770-spike-v1.md) and [`attrs-770-profile-v1.md`](attrs-770-profile-v1.md). This result does not authorize a generic package system, scheduler, worker pool, or sharing mechanism.

## Decision

Do not build another synthetic multi-agent overlap cohort. Pysolate already has:

- a bounded shared-Guest prototype with separate logical/physical evidence and real CPython/WASI execution (`multi-agent-shared-execution-next-step.md`);
- a completed 20-program authority-bound campaign (`../plans/2026-08-15-authority-bound-multi-agent-transparent-campaign-megagoal.md`).

The next useful experiment is instead:

> **A private `attrs-770` task-oracle/import-profile feasibility spike:** prove whether a pinned pure-Python repository package can execute the exact public semantic oracle in the real Guest, while the current Host profile either binds that package honestly or rejects it explicitly.

This addresses the missing natural-task oracle without repeating prior sharing fixtures.

## Public multi-agent data decision

No inspected public source is a direct natural-overlap corpus for the current Pysolate question.

| Candidate | What is actually public | Useful field | Missing decisive evidence | Decision |
|---|---|---|---|---|
| AWS multi-agent collaboration benchmark | Small scenario/agent dataset plus one sample multi-agent conversation | Per-agent trajectories, source/destination, actions and observations | No timestamps or overlap intervals; no shared workspace/root identity; no physical execution identity; no agent-written Python task oracle | Do not import for M4 sharing claims [1][2] |
| `agent-scaling` | MIT-licensed harness and benchmark inputs | Independent/decentralized implementations launch agents concurrently; conversation records carry timestamps | No checked-in run trajectories in the inspected data-availability contract; generating them requires models and benchmark environments; no Pysolate authority/workspace joins | Possible future harness, not an existing corpus [3][4] |
| Current Open-SWE pilot | Sequential single-agent trajectories with structured tool calls and resolved outcome | Real repository/task oracle and agent-written reproducer code | No multiple logical agents or concurrent tool messages; no Host authority/workspace-base equivalence | Use only for one natural task-oracle spike |

This is a bounded negative finding over the inspected candidates, not a universal claim that no multi-agent trajectory dataset exists.

## Why `attrs-770`

The public `python-attrs__attrs-770` instance binds repository `python-attrs/attrs` at base commit `58d2adce57f2c4e447eb12b892ebbb09cccbdcc3`. It has one `FAIL_TO_PASS` oracle and 237 `PASS_TO_PASS` tests; the public test patch adds one Python-3-only semantic case for creating a generic dynamic class [5][6].

The private Open-SWE trajectory contains:

- 95 messages;
- 31 `execute_bash` and 15 `str_replace_editor` calls;
- an agent-written `reproduce.py` plus inline Python probes;
- a final 775-byte model patch to `src/attr/_make.py` replacing `type(...)` with `types.new_class(...)`;
- outcome `resolved=1`.

The exact standalone semantic oracle needs only `types`, `typing`, and the repository's `attr` package:

```python
from types import new_class
from typing import Generic, TypeVar
import attr

T = TypeVar("T")
Parent = new_class("Parent", (Generic[T],), {})
attr.make_class("test", {"id": attr.ib(type=str)}, (Parent[int],))
result = {"oracle": "passed"}
```

The repository's historical tox contract is broad and multi-version [7], but the spike deliberately does not claim that full matrix.

## Why not Altair

`altair-viz__altair-3118` is a poor first replay candidate:

- outcome `resolved=0`;
- 143 `FAIL_TO_PASS` tests and no `PASS_TO_PASS` set [8];
- public reference patch: 27,299 bytes across a much broader compatibility change;
- runtime/test dependency surface includes NumPy, pandas, jsonschema, packaging and optional native-heavy visualization packages [9][10];
- the private model patch differs structurally from the public fix.

A reduced Altair probe would be a new synthetic test rather than a faithful replay.

## Spike contract

### Inputs

- private checkout of `python-attrs/attrs` at exact base commit;
- private exact model patch from the existing Open-SWE raw bundle;
- exact public standalone semantic oracle derived from the sole `FAIL_TO_PASS` test;
- named real Guest artifact and manifest;
- body-safe public output containing only identities, terminal states, bounds and claim limitations.

### Arms

1. **Native RED:** base source must fail the semantic oracle.
2. **Native GREEN:** model-patched source must pass.
3. **Guest runtime feasibility:** execute the same patched package and oracle in the real Guest using a private bounded workspace/package path, without claiming profile admission if the run is intentionally unbound.
4. **Current-profile admission control:** submit the exact source under the current verified artifact profile. `attr` is not in the current artifact import inventory, so honest behavior is either a verified bound import or an explicit pre-execution rejection—never a silent dynamic-import bypass.

### Success conditions

The spike succeeds only if:

- native RED/GREEN establish patch causality;
- Guest execution returns the same semantic result or a typed, attributable runtime incompatibility;
- current-profile admission is explicit and body-safe;
- source, model-patch, oracle, workspace, artifact and manifest identities are recorded;
- no shell, package installer, network, Git mutation, live LLM or external write is used inside the Guest.

### Stop conditions

Stop without implementing a package profile if:

- native RED/GREEN cannot be reproduced at the pinned source;
- the Guest cannot import the workspace-provided pure-Python source even in an explicitly unbound private feasibility arm;
- making `attr` importable requires broad package-installation or native-extension machinery;
- the only route is to weaken source/import admission or mislabel a dynamic import as verified;
- the resulting evidence proves only another completion smoke test rather than the public semantic oracle.

## Decision after the spike

- **Runtime passes; profile rejects:** prepare a separate design decision for a tiny artifact-bound pure-Python package/shard profile. Do not implement it automatically.
- **Runtime fails:** record the incompatibility and close this M4 route.
- **Profile already binds honestly:** run one bounded oracle replay and close the natural-task evidence gap.

No result from this spike can establish natural multi-agent overlap, sharing frequency, speedup, scheduler value, or cross-agent result safety.

## Sources

[1] https://github.com/aws-samples/multiagent-collab-scenario-benchmark/tree/cb82575c0846bb147423bebacc8597bc24196142
[2] https://github.com/aws-samples/multiagent-collab-scenario-benchmark/blob/cb82575c0846bb147423bebacc8597bc24196142/sample_conversations/travel/conversation_0.json
[3] https://github.com/ybkim95/agent-scaling/blob/6f3bfb78a6481c1098d182680f39b0f904b292a2/agent_scaling/agents/multiagent_independent.py
[4] https://github.com/ybkim95/agent-scaling/blob/6f3bfb78a6481c1098d182680f39b0f904b292a2/DATA_AVAILABILITY.md
[5] https://datasets-server.huggingface.co/filter?dataset=nebius%2FSWE-rebench-V2&config=default&split=train&where=%22instance_id%22%3D%27python-attrs__attrs-770%27&offset=0&length=1
[6] https://github.com/python-attrs/attrs/commit/58d2adce57f2c4e447eb12b892ebbb09cccbdcc3
[7] https://raw.githubusercontent.com/python-attrs/attrs/58d2adce57f2c4e447eb12b892ebbb09cccbdcc3/tox.ini
[8] https://datasets-server.huggingface.co/filter?dataset=nebius%2FSWE-rebench-V2&config=default&split=train&where=%22instance_id%22%3D%27altair-viz__altair-3118%27&offset=0&length=1
[9] https://github.com/vega/altair/commit/935e4e84828860e91c3f67999353ee290c2b17c0
[10] https://raw.githubusercontent.com/vega/altair/935e4e84828860e91c3f67999353ee290c2b17c0/pyproject.toml
