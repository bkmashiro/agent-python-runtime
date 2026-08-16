# attrs-770 task-oracle and import-profile spike

**Status:** PARTIAL on 2026-08-16. The natural semantic oracle is runtime-compatible with the real Guest, while the current verified artifact profile correctly rejects `attr`. No package or shard profile was implemented.

## Question

Given the pinned Open-SWE task `python-attrs__attrs-770`, can Pysolate preserve a real repository patch-to-oracle causal path in CPython/WASI, and can the current Host profile bind the workspace-provided pure-Python package honestly?

Canonical body-safe result: [`../evidence/attrs-770-spike-v1.json`](../evidence/attrs-770-spike-v1.json).

Private source, model patch, oracle source, workspaces, raw responses and tracebacks remain under `~/.hermes/evidence/pysolate/attrs-770-spike-v1/` with directory mode `0700` and private files denied to group/other. They are not tracked.

## Frozen inputs

| Input | Identity |
|---|---|
| Open-SWE instance | `python-attrs__attrs-770` |
| Repository | `python-attrs/attrs` |
| Open-SWE dataset revision | `ad4805a5aa7de70d99cab0bb8f99b15304c76de0` |
| Dataset license | CC-BY-4.0 |
| Repository license | MIT |
| Corpus manifest | `sha256:8ffde0e8882097320e61a0ec8606c1e2c9ee60d71c357f143ce546b720c65dcf` |
| Corpus item | `sha256:e577adb28f3d66c75e935118ce991d019b2f6d9118dad3e67a095727cfee92f1` |
| Base commit | `58d2adce57f2c4e447eb12b892ebbb09cccbdcc3` |
| Public oracle | one Python-3-only `FAIL_TO_PASS`; 237 `PASS_TO_PASS` |
| Exact private model patch | `sha256:fdbfbdbb113809ae7982eb85e221ae5ddfdac9774a787114424e6ed2785f236e` |
| Native oracle source | `sha256:fb3d5d03575bbadd74d885150c81a73d41c5d480d915a5b31624ed62fcc62e94` |
| Guest source | `sha256:4398090b9a7116f54cc50b0c445447f92680d9413f37309460f6d0e234ed82b5` |
| Guest artifact | `sha256:664077c1d63445ec267b1b30e30ce31c72e7038d62a08fe1682c675a64cff257` |
| Artifact manifest | `sha256:154b5f8f2b8f21c718acb4917e9b317509ef21d5fc29d2d3b94698592a49c46b` |
| Runner source | `388ef3291a1586a3b02cf4b5f0c31c6407be152f` |
| Runner binary | `sha256:1b090b0d6e38a1be6e49d07227a035e82095e94d569cdcb4084e48f736cf3306` |

The standalone oracle retains the public semantic operation: create a generic parent with `types.new_class`, then ask `attr.make_class` to create a subclass. It does not copy pytest or the complete test suite into the Guest.

## Results

| Lane | Source/package state | Artifact profile | Result | Physical Guest |
|---|---|---|---|---|
| Native RED | pinned base | N/A | expected `TypeError` | no |
| Native GREEN | exact model patch | N/A | oracle passed | no |
| Guest RED | pinned base workspace package | deliberately unbound private runtime probe | expected `TypeError` | yes |
| Guest GREEN | patched workspace package | deliberately unbound private runtime probe | oracle passed | yes |
| Verified profile: operator requests `attr` | patched or base | artifact manifest supplied | `execution profile artifact mismatch` | no |
| Verified profile: only qualified `sys` declared | patched or base | artifact manifest supplied | `execution profile source comparison failed` | no |

### Runtime feasibility

The real Guest mounted 21 regular workspace entries. Base and patched package sizes were 162,861 and 162,921 bytes respectively. Both Guest lanes used zero Host capability calls and returned a discard receipt whose initial and final workspace identities were equal.

This validates a narrow mechanism:

```text
pinned repository source
→ exact Agent patch
→ workspace-provided pure-Python package
→ real CPython/WASI execution
→ public semantic RED/GREEN oracle
```

It does not validate artifact-profile admission because the feasibility lanes deliberately omitted both manifest and execution profile. `apyrun` rejects the invalid mixed state where a manifest is supplied without a profile.

### Verified admission

The current artifact inventory discovers standard-library roots but does not contain `attr`; its qualified roots also do not include `attr`, `types`, or `typing`.

Two controls closed different bypasses:

1. Declaring `attr` as allowed cannot manufacture artifact support: artifact binding rejects the profile.
2. Declaring only the actually qualified `sys` lets artifact binding complete, but source comparison rejects the undeclared imports before the runner factory is created (`cmd/apyrun/main.go:88-125`).

No Guest response or workspace receipt exists for either bound rejection. The `physical_guest_started=false` claim is therefore tied to control flow and absence of a runner response, not inferred from a Guest error.

## Verdict: PARTIAL

### What worked

- The pinned base reproduces RED under both native CPython and the real Guest.
- The exact Open-SWE model patch produces GREEN under both lanes.
- A mounted pure-Python repository package is runtime-compatible without package installation or native extensions.
- Workspace state remained unchanged and was discarded.

### What did not work

- The current verified base profile cannot admit `attr`.
- The artifact's qualification set is narrower than its discoverable standard-library inventory, so `types` and `typing` also require explicit qualification before this exact source can become profile-bound.

### Decision

Do not weaken admission, use dynamic-import tricks, or label the unbound lane profile-verified.

A future step may design a tiny artifact-bound pure-Python package/shard profile that carries:

- package source and license/provenance;
- artifact/VFS identity;
- discoverable and operation-qualified import roots;
- source/profile binding;
- cross-Run freshness tests.

That was a separate architecture decision. The subsequently authorized exact profile is recorded in [`attrs-770-profile-v1.md`](attrs-770-profile-v1.md); it does not generalize to an installer, resolver or shard scheduler.

## Reproduction

Regenerate the body-safe report from retained private evidence:

```sh
python3 scripts/attrs-770-spike.py \
  --root ~/.hermes/evidence/pysolate/attrs-770-spike-v1 \
  --artifact ~/.hermes/evidence/pysolate/source-bound-mg1-e79e821/dist/agent-python-runtime.wasm \
  --manifest ~/.hermes/evidence/pysolate/source-bound-mg1-e79e821/dist/manifest.json \
  --runner ~/.hermes/evidence/pysolate/attrs-770-spike-v1/apyrun \
  --runner-source-commit 388ef3291a1586a3b02cf4b5f0c31c6407be152f \
  --output docs/evidence/attrs-770-spike-v1.json
```

Validator tests:

```sh
python3 -m unittest scripts.tests.test_attrs_770_spike
```

## Claim limits

This experiment does not establish:

- complete attrs or SWE-bench correctness;
- compatibility across historical Python versions;
- a production package/shard design;
- performance benefit;
- natural multi-agent overlap or shared-execution opportunity;
- scheduler, worker-pool or coalescing value.

Source task: [SWE-rebench-V2 `python-attrs__attrs-770`](https://datasets-server.huggingface.co/filter?dataset=nebius%2FSWE-rebench-V2&config=default&split=train&where=%22instance_id%22%3D%27python-attrs__attrs-770%27&offset=0&length=1).
