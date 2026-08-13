# Real Agent acceptance contract: Full Composable Runtime

Status: **Prepared for Yuzhe/Hermes joint review; not executed by this Megagoal.**
Date: 2026-08-13

## Decision this experiment may support

Determine whether the bounded composable mechanisms preserve semantics and improve useful overlap/reuse for repository-shaped Agent work strongly enough to retain and productize them. This is a post-Megagoal acceptance experiment, not a correctness substitute and not a production benchmark.

## Preconditions

Freeze before running:

- exact Pysolate source commit and verified Guest artifact/profile/import closure;
- one user-approved local repository snapshot with no credentials, generated assets, submodules requiring network, or external writes;
- one fixed Agent prompt and model/provider response corpus, or a replayed deterministic model transcript, so Runtime treatments do not silently change Agent decisions;
- one immutable parent workspace Capsule/root and one explicit privacy partition;
- one frozen capability Plan containing only credential-free reads needed by the task;
- explicit CPU/memory/time/storage bounds and cleanup path;
- the accepted `pysolate.composable-evidence.v1` verifier.

Do not use a live evolving repository, live model response drift, paid external provider effects, network writes, issue/PR publication, package installation, shell/subprocess, or ambient Host filesystem access.

## Repository-shaped task

Choose a small but real repository task that naturally contains all of:

1. inspect multiple source files;
2. derive two independent candidate analyses in child branches;
3. run one repeated deterministic transformation suitable for an admitted Agent Function;
4. wait at one explicit Harness boundary;
5. refresh one named read-only observation;
6. select one child result and materialize a bounded patch/report into a private output root;
7. terminate without publishing externally.

The acceptance task must have a deterministic semantic oracle: expected selected files, normalized patch/report content, workspace root identity relation, and prohibited-output list. Test wall time alone is not an oracle.

## Matched treatments

Run the same frozen Agent decisions and input root through:

1. complete-source fresh baseline with every successor mechanism off;
2. streaming only;
3. streaming plus two-child fan-out;
4. fan-out plus function cache off/on;
5. retention off with single-flight off/on under concurrent duplicate calls;
6. wait with fresh re-evaluation off/on;
7. prepared runtime off/on;
8. all bounded implemented mechanisms on;
9. invalid-parent, invalid-child, changed-observation, branch-conflict, cache-corruption, and cancellation negatives.

Memory COW is not a treatment unless a later joint decision reopens it and a new exact implementation passes its own authority/state census first.

## Required equivalence and safety assertions

Every valid treatment must agree on:

- normalized Agent-visible result;
- selected workspace files and bytes;
- selected-root relation to the same immutable parent;
- capability call semantics after accounting for explicitly allowed speculative orphan reads;
- zero external writes/effects;
- no result/body substitution across source, root, policy, privacy, freshness, profile, artifact, or occurrence mismatches.

Every invalid/cancelled treatment must prove:

- zero selected/public workspace root;
- child/private branch disposal;
- no surviving Guest, `/tmp`, prepared slot containing request state, Broker handle, or single-flight entry;
- no cache write after failed purity/admission;
- typed terminal disposition.

## Evidence

Retain body-free `pysolate.composable-evidence.v1` records plus a separately protected semantic oracle result. Evidence must include:

- source/artifact/profile/Plan/input/root identities;
- selected/fallback mechanism matrix;
- logical node and physical Guest counts;
- staged dispatch/consume/orphan ownership;
- child lineage and relative monotonic timeline;
- cache/single-flight/invalidated-node counts;
- changed/materialized/retained bytes;
- prepared/fresh counts and teardown;
- terminal dispositions and cleanup proof.

The public/reportable record must not contain Host paths, repository bodies, prompts/model text, credentials, endpoints, absolute Host time, private cache existence from another partition, or workspace/result bodies.

## Interpretation

Correctness and cleanup are hard gates. After they pass, compare only matched treatment deltas:

- time to first useful child completion and parent critical path;
- physical Guest creation/destruction;
- duplicate compute eliminated by cache/single-flight;
- retained explicit bytes and waiting Guest instance-time released;
- branch materialization/garbage;
- prepared startup/request/teardown phases.

Do not generalize one repository task to universal Agent speedup, provider latency, multi-tenant cache value, exactly-once effects, production readiness, or memory-COW benefit. If a mechanism adds complexity without measurable value in this acceptance population, retain its off-state and review removal or continued Experimental status jointly.

## Joint execution protocol

Yuzhe selects and approves the exact repository snapshot, task, model-transcript treatment, and resource budget. Hermes materializes a source-bound run plan and predicted outputs-to-inspect before execution. Both review the plan; only then may the real acceptance run begin. This document itself grants no permission to execute the real workload.
