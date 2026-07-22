# Threat Model

## Status and protected asset

This is the V1 target threat model. The protected asset is the Host process and its authority while executing generated Python. This runtime is not claimed to provide microVM-grade kernel isolation.

## Adversary

Assume generated code, inputs, capability arguments, returned pointers, response lengths, and Python exceptions are malicious. The adversary may try to persist state across runs, exhaust resources, escape the guest, obtain credentials, access arbitrary endpoints, or forge success evidence.

## Security goals

V1 must prevent or bound:

- ambient filesystem reads/writes;
- inherited environment variables and secrets;
- direct network access;
- process creation and subprocess execution;
- runtime dependency installation;
- unbounded linear-memory growth;
- unbounded wall-clock execution;
- unbounded response and traceback data;
- undeclared or excessive Host tool calls;
- guest-selected credentials or arbitrary destinations;
- stale run-local state contaminating a later run;
- invalid pointer/length reads and writes;
- unhealthy instances returning to the pool;
- receipts that claim operations not mediated by the Host.

## Non-goals

V1 does not promise:

- defense equivalent to a hardened microVM or separate kernel;
- arbitrary native extensions;
- full POSIX, shell, PTY, daemon, or background-process behavior;
- write-side external effects or rollback;
- arbitrary MCP/plugin installation;
- distributed scheduling or multi-host isolation;
- instruction-count CPU metering if wazero cannot enforce it.

Unsupported hard limits are rejected or documented as unsupported; they are not represented as enforced flags.

## Attack surfaces and controls

### RunRequest authority injection

Control: schemas reject unknown and authority-bearing fields. `RunConfig` is constructed by trusted Host code and is not decoded from model JSON.

### WASI ambient authority

Control: instantiate without inherited arguments, environment, stdio, preopened directories, network sockets, or Host process APIs. Packed read-only runtime files are artifact content, not ambient Host paths.

### Pointer and length corruption

Control: validate arithmetic for overflow, memory bounds, maximum request/response sizes, and length-prefixed response layout before copying or decoding.

### CPU/wall-time denial of service

Control: require a Host deadline and close the module on cancellation. If instruction/fuel metering is unavailable, do not call the wall deadline an instruction budget.

### Memory denial of service

Control: configure maximum pages/bytes before instantiation, reject unsupported requested limits, detect memory-size drift, and discard instances that cannot be reset safely.

### Output and traceback floods

Control: impose limits while reading/copying, not after an unbounded buffer has already been materialized. Tracebacks are structured and truncated.

### State contamination

Control: instantiate a fresh guest for every Run and discard it after success, structured error, trap, or cancellation. Any future prepared-state optimization must restore Python globals, modules, random state, buffers, memory growth, globals/tables, and Host resources; reset failure must close the instance.

### Host capability abuse

Control: resolve only pre-granted capability IDs; enforce destination allowlists, per-call timeout, per-call byte cap, and total-call budget. Credentials never enter guest memory. Direct guest network access remains unavailable. The production-style client ignores ambient proxies, resolves hostnames at dial time, rejects the whole resolution if any address is private, loopback, link-local, unspecified, multicast, or reserved, and dials a validated IP directly. Only an IP-loopback literal is accepted as an explicit local fixture; DNS names resolving to loopback are denied.

### Evidence forgery

Control: Host creates receipts from operations it actually mediates. Bind receipts to run ID, capability ID, operation index, bounded request/response digests, outcome, and timing. Guest-provided receipt-like JSON has no authority.

### Supply-chain substitution

Control: build from an immutable source lock with SHA-256 and license metadata. Verify downloaded bytes, exact artifact imports/exports, manifest digest, SBOM, and CI run identity. Mutable `latest` URLs are forbidden.

## Fail-closed events

Close/discard the instance on:

- trap or deadline cancellation;
- memory shape drift outside the reset contract;
- malformed or out-of-bounds response pointers;
- unsupported capability import;
- reset failure;
- Host import protocol violation;
- output cap violation where continued instance health is uncertain.

## Evidence required before security wording

A configuration flag is not evidence. Each relevant goal requires an executable denial test or live probe using the actual Linux-built artifact and Go/wazero dispatch path. Remaining gaps must be listed in the artifact manifest and evidence report.
