# EAGER-style comparator contract v1

Status: frozen Phase 3 comparator semantics. This is a research treatment, not a production execution mode and not a claim of source parity with EAGER.

## Primary source

The comparator is anchored to **Executing as You Generate: Hiding Execution Latency in LLM Code Generation**, arXiv `2604.00491`, PDF `https://arxiv.org/pdf/2604.00491`, SHA-256 `23af671ca94b7cbbc0866a37391520ae39e75c964320e7809b1612dfb3e023cb`.

The paper specifies target-language AST chunking with one-token lookahead, a persistent interpreter, dynamic batching, deferred low-yield declaration chunks, and a static name-based gate. Its default denied modules cover process creation, filesystem mutation, network/IPC and timing-sensitive operations: `os`, `subprocess`, `shutil`, `socket`, `multiprocessing`, `time`, `signal`, and `threading`. Dynamic entry points `eval`, `exec`, `compile`, and `__import__` fall back to serial execution.

## Frozen local treatment

The checked-in `eager_style_gate` treatment binds:

- exact target `cpython-3.14.0-wasi`;
- complete top-level statement boundaries from target-CPython AST plus one-token lookahead;
- one persistent interpreter namespace per trial;
- dynamic merging of queued chunks;
- `FunctionDef`, `AsyncFunctionDef`, and `ClassDef` as low-yield deferred chunks;
- static `Name` or attribute-root matching for the published denied modules and dynamic entry points;
- no early generation interruption, so every matched lane observes the same frozen source schedule;
- on the first denied chunk, that chunk and the remaining suffix stay sealed until final source completion; already executed prefixes are not replayed;
- a final invalid suffix reports syntax failure after source seal while already executed prefix effects remain separately recorded.

The last three bullets are explicit comparator choices needed for a matched deterministic campaign. They must not be attributed to the authors' implementation.

## Identities

- Contract schema: `pysolate.eager-style-gate-contract.v1`
- Contract identity: `sha256:16e76c741749bddb61c68ae80902827f73e9dc7efdad6208d858f8edae100ab8`
- Checked-in file: `docs/evidence/eager-style-gate-contract-v1.json`
- File SHA-256: `6fe4f615ac532d855b87be26bc785573c94291b8075dedc71125b44d042f263b`

Every `eager_style_gate` trial must bind the contract identity. Other treatments must leave the comparator-contract field empty.

## Claim boundary

This contract supports a fair local `eager_style_gate` implementation and matched timing study. It does not reproduce the authors' unpublished runtime source, claim identical chunk schedules on their benchmark corpus, or turn persistent-interpreter semantics into a Pysolate production feature.
