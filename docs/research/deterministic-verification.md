# Deterministic-verification profile

Status: **Experimental/Partial**. The schema is
`pysolate.deterministic-verification.v1`. It is a bounded verification profile,
not a claim that arbitrary Python, every platform, or a complete Agent is
deterministic.

## Claim

For a qualified workload, repeated fresh Guests on the currently tested Host
implementation can compare exact outputs when all of these inputs are held
constant:

- exact CPython/WASI artifact bytes and verified execution profile;
- deterministic-profile identity and Host-owned random seed;
- admitted code, canonical inputs, output schema and compatibility request;
- no mounted WASI workspace;
- the same captured/overridden capability inputs and sealed Plan/Grants when
  capabilities are used;
- the same Runtime implementation and applicable platform behavior.

The profile makes these conditions explicit and falsifiable. It does not turn
live external reads into deterministic inputs, replay external writes, or hide
a source rewrite in Agent code.

## Version 1 controls

`NewDeterministicVerificationProfile` binds an exact artifact SHA-256 and a
bounded Host seed into a domain-separated profile identity. The execution-
profile binding used by Playback also includes that identity, and
`execution.started` observation evidence records it separately.

The wazero module configuration supplies:

- deterministic WASI random bytes from `sha256-counter-v1`, keyed by the Host
  seed and reset for each fresh Guest;
- a virtual wall clock starting at `1700000000000000000` Unix nanoseconds;
- a virtual monotonic clock starting at `1000000000` nanoseconds;
- a `1000` nanosecond step for clock reads;
- deterministic nanosleep advancement rather than Host sleeping.

The profile does not monkey-patch Python modules or rewrite Agent source. The
exact artifact digest is checked before compilation, so artifact substitution
fails admission.

The current real-Guest repeat probe exercises Python `datetime`, `time_ns`,
`os.urandom`, and Python string hashing in two fresh Guests and requires the
complete responses to match. That is evidence for this qualified probe, not a
blanket guarantee for every library that may consume clock or entropy. Python
`random` default seeding and `secrets` are not separately exercised by that
probe and remain outside the current documented qualification.

## Admission denials

The profile requires a verified execution profile and rejects a mounted
workspace. Static imports rooted at `asyncio`, `concurrent`, `locale`,
`multiprocessing`, or `threading` are rejected before Guest execution. The
ordinary conservative import admission path still independently rejects
dynamic, relative, nested, late, compound, or otherwise unsupported import
forms.

Directory enumeration is not canonicalized. Instead, the profile admits no
mounted WASI filesystem, so it cannot claim deterministic transformation of a
workspace or directory traversal. This is an honest exclusion, not an
invisible sorting shim.

Locale policy is similarly narrow: no Host environment locale is inherited and
the `locale` import class is denied. Locale mutation and all locale-sensitive
behavior have not been proven equivalent across artifacts/platforms.

No independent timezone configuration is exposed to the Guest, but the
profile does not bind a named timezone database or claim timezone-sensitive
equivalence across Guest artifacts/platforms. The qualified `datetime` probe
is the extent of Current timezone-related evidence.

## Unsupported and unclaimed cases

The versioned profile descriptor explicitly names these unsupported classes:

- concurrent scheduling;
- floating-point cross-platform equivalence;
- locale mutation;
- mounted-WASI directory enumeration.

The Partial label also means the following are not claimed:

- equivalence across Runtime, wazero, Go, OS, architecture, or Guest-artifact
  upgrades;
- deterministic live network data, service behavior, or Host scheduling;
- deterministic behavior for libraries not covered by the artifact profile and
  a real-Guest qualification probe;
- Python `random` default seeding, `secrets`, timezone-database behavior, or
  other clock/entropy consumers not separately probed;
- stable exception text, timing metrics, resource-exhaustion boundaries, or
  side channels across platforms;
- deterministic filesystem transformations, because mounted workspaces are
  denied;
- deterministic complete-Agent/provider replay;
- instruction, bytecode, local-variable, heap, stack, or WebAssembly-memory
  replay.

The profile identity currently binds its artifact and declared virtual-input
policy, but not a full Host implementation or platform identity. That is one
reason it remains Experimental/Partial. A claim crossing those boundaries
requires additional binding and qualification, not a documentation change.

## Capabilities and branches

Capability results are deterministic inputs only when they are protected and
reused through strict Playback or an explicit branch override/recorded suffix.
A `live_suffix` is a live external read and is outside exact repeatability
unless its returned bytes are subsequently captured and compared as a new
qualified input.

Counterfactual children may intentionally diverge from their parent. The
deterministic claim is that repeated executions of the *same child manifest and
qualified inputs* agree, not that a child agrees with its parent.

Every repeat and branch starts a fresh Guest and re-executes the original
request from the initial state. No Python heap or WASM snapshot is restored.

## Operator contract

The Host CLI accepts this profile only with:

```json
{
  "execution_profile": {
    "id": "base",
    "allowed_imports": ["datetime", "sys"]
  },
  "deterministic_verification": {
    "status": "experimental_partial",
    "random_seed": "study-1"
  }
}
```

The verified artifact manifest is still required. Configuring a workspace or a
different status fails before execution. Agent input cannot select the seed,
clock, status, or artifact.

## Interpreting a successful repeat

A matching pair demonstrates equality for the named artifact, profile, inputs,
captured capabilities, Runtime build, platform, and probes that were actually
run. It does not prove semantic correctness or eliminate unmeasured sources of
nondeterminism. A mismatch falsifies the qualification and must not be hidden by
normalizing the result after execution.

Future expansion should add one source at a time with a RED real-Guest probe,
an explicit control or admission denial, identity binding, and repeated GREEN
evidence. If a source cannot be controlled, captured, or denied, it remains
outside the profile.
