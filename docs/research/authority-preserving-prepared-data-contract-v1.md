# Authority-preserving prepared data contract v1

Status: **Phase 0 frozen; implementation not started**

Parent source: `a75987eaa8a7fbef33cc0cee48d076eb4c3f2769`

This document freezes the smallest research contract for moving one immutable NumPy dataset read and bounded decode before final source generation while retaining Host-owned authority and exact logical claim. It is not a production API or a generic cache contract.

## Research question

Can Pysolate overlap an explicitly authorized immutable `.npy` read and typed decode with later source generation, then supply the value to fresh Guests without changing logical Python/effect semantics or widening authority?

The mechanism succeeds independently of economics when it proves:

1. syntax alone starts no physical work;
2. one sealed Host declaration plus verified source facts admits exactly one bounded physical attempt;
3. physical read/decode and logical claim are recorded separately;
4. unreached or invalid final execution never becomes a logical load;
5. exact result/error and fresh-Guest mutation isolation match the serial oracle.

A slower treatment is a valid result.

## Existing lifecycle reused

The current path is:

```text
target Guest AnalyzeVerified(prefix, Plan binding)
→ semantic.StreamingPrefixAdmission.AdmitVerifiedPrefix
→ semantic.CanPreissueStreamingPrefix
→ capability.Plan.PreDispatch + StreamingObservationBinding
→ semantic.SemanticPreDispatch.Start
→ capability.PreparedPreDispatch.Call
→ streaming.StagedObservation(ready)
→ unchanged Broker capability boundary
→ exact staged Claim / one-shot Consume
→ consumed | orphaned | cancelled | failed
```

Existing owners remain authoritative for:

- capability registration, schema canonicalization and physical handler call: `runtime/capability`;
- verified source occurrence and Host-plan legality join: `runtime/semantic`;
- one-shot staged physical result and terminal disposition: `runtime/streaming`;
- bounded ndarray descriptor/body/publication: `runtime/numpycodec` and `runtime/resultblob`;
- Wazero prepared/private-COW baseline: `runtime/engine/wazero`.

Prepared data adds a research-only joined contract and typed transform. It must reuse these owners rather than duplicate their validators.

The P3 prototype keeps the 8 MiB body out of the Broker response. The qualified `sources.read` handler reads the exact immutable file into Run-private Host staging and returns only a bounded digest/byte-count receipt. The authority-free decoder consumes that Host staging; the existing prepared-region scalar token records the later dynamic claim. Thus the physical body never widens the 1 MiB capability transport boundary.

## Authority equation

```text
verified np.load candidate
+ exact occurrence and canonical loader arguments
+ explicit Host PreparedDataContract
+ exact sealed capability Plan identity
+ qualified sources.read binding
+ immutable workspace root and exact file digest
+ numpy-core artifact/profile/import closure
+ Run-private budget and privacy context
= eligible physical preparation
```

The candidate fact is authority-free. The contract is Host-authored and positive. Neither can substitute for the other.

## PreparedDataContract v1

The canonical contract is cryptographically joined to, but need not widen, the existing capability Plan. It contains:

```text
schema_version                  pysolate.prepared-data-contract.v1
capability                      sources.read
capability_plan_sha256          exact sealed Plan
call                            numpy.load
occurrence                      stream epoch + admitted-prefix digest + exact span + canonical arguments + occurrence 1
resource                        workspace / literal /workspace/input.npy
source_policy                   immutable_workspace_root
workspace_root_sha256           exact immutable root
file_sha256                     exact canonical .npy file
freshness                       plan_epoch
unclaimed                       discard_with_disposition
privacy_partition               exact Run partition
loader                          numpy_npy_c_v1
allow_pickle                    false
mmap_mode                       absent
artifact/profile/import         exact numpy-core bindings
codec                           numpy_ndarray_c_v1
max_file_bytes                  8,388,736
max_body_bytes                  8,388,608
max_result_bytes                4,096 (body-free receipt only)
cost_units                      positive Host budget
```

Changing one field changes contract identity. Unknown or omitted fields fail validation. A contract is valid only when its referenced capability is independently eligible for existing read pre-dispatch.

The Host declaration cannot bind a final-source digest before streaming generation has produced it. Its occurrence selector therefore binds the stream epoch, admitted-prefix digest, exact span, canonical call arguments and dynamic occurrence `1`. The later claim adds the sealed final-source digest and proves that the final source extends the admitted prefix without changing the occurrence:

```text
PreparationIdentity = stream epoch + prefix source + span + arguments + Host contract
ClaimIdentity       = PreparationIdentity + sealed final source + exact unchanged occurrence
```

The final-source digest is claim authority, not speculative-start authority.

## Narrow source form

Only this equivalent shape is in scope:

```python
import numpy as np
dataset = np.load('/workspace/input.npy', allow_pickle=False)
```

The analyzer may emit a bounded candidate only when:

- `numpy` is imported exactly as `np`;
- the callee is exactly `np.load`;
- there is one literal absolute research path;
- `allow_pickle=False` is explicit;
- `mmap_mode` and all other options are absent;
- the occurrence is source-located and unique in the admitted prefix.

Alias ambiguity, dynamic path, star imports, rebinding `np`, positional pickle arguments, extra keywords and unknown call shapes emit no eligible candidate. This recognition remains insufficient without the Host contract.

## Physical versus logical effects

Physical preparation may happen before final source seal:

```text
planned
→ read_issued
→ source_verified
→ decode_running
→ typed_staging
→ sealed
```

Logical execution occurs only when final admitted Python reaches the exact original occurrence:

```text
sealed
→ exact occurrence/arguments/source/contract/object claim
→ claimed
```

Otherwise the attempt terminates as one of:

```text
orphaned | cancelled | late | rejected | failed
```

An early filesystem/object read remains a real externally observable effect. The Host declaration must authorize its speculative timing, cost, quota/privacy partition and unclaimed disposition. The transform lane receives no Broker, workspace, network, credential, clock or entropy authority.

No prepared result is a durable cache hit. No claim may fall back to a second physical read after a targeted staged attempt has started and become ambiguous or mismatched.

## Deterministic fixture v1

The fixture is a canonical NumPy `.npy` v1.0 file generated without Host NumPy:

```text
dtype                    <i8
shape                    [1024, 1024]
order                    C
value(i)                 int64(i), i = 0..1,048,575
header alignment         64 bytes
header bytes             128
body bytes               8,388,608
file bytes               8,388,736
body SHA-256             sha256:a78cee677876b925402c15818acd3fc020a47754d9d1c26688914ea09070f8d0
file SHA-256             sha256:390bd7abd4f26dfdf3440f060f77bd02526e97e0a46838337f56cb8ef978af7e
first                    0
last                     1,048,575
sum                      549,755,289,600
```

Canonical header dictionary:

```python
{'descr': '<i8', 'fortran_order': False, 'shape': (1024, 1024), }
```

A checked-in generator must reproduce these identities before any real run. The 8 MiB body is the matched core because the existing `numpy_ndarray_c_v1` private-copy path is bounded at 8 MiB. Payloads of 64/256 MiB are not part of the core and may enter only through a separately frozen pre-observation extension where all compared treatments support them.

## Source release schedule

Each trial uses the same complete source and one of three frozen lead gaps:

```text
chunk 1 at 0 ms:    import and fixed setup
chunk 2 at 100 ms:  complete np.load statement becomes visible
final chunk at:     100 ms + lead_gap_ms
lead_gap_ms:        0 | 250 | 1000
```

Baseline treatments may not physically start before final source. Early treatments may start only after chunk 2 is verified and Host-qualified. Timing evidence records actual release, analysis, physical read/decode, finalization, claim and terminal times.

## Matched treatments

Core treatments, three trials per retained coordinate with deterministic alternating order:

```text
serial_whole_source
EAGER_style_persistent_interpreter
raw_read_only_pre_dispatch
prepared_data_private_copy
prepared_data_private_cow_pages     Linux only if P4 proves it
prepared_data_data_local_compute
```

The EAGER treatment is a mechanism comparator based on arXiv `2604.00491v2`, not a claim of published implementation parity.

Consumer counts are `1 | 2 | 4` only where the operation is semantically defined. Fixed harness fanout is used; no public fanout API or single-flight is introduced.

## Oracles and controls

All retained result lanes must match:

```text
dtype, shape, first, last, sum
logical load count
result/error class
stdout/stderr policy
workspace root before/after
capability/authority counts
consumer mutation isolation
terminal object/lease dispositions
```

Required negative controls:

- no Host declaration;
- one-field Plan/contract/source/root/profile/import/codec drift;
- dynamic path or ambiguous NumPy alias;
- `allow_pickle=True`, object dtype, corrupt header/body and wrong body length;
- invalid later suffix, earlier exception and branch not taken;
- cancellation before read, during read and after seal;
- source replacement, late result, claim mismatch and claim replay;
- attempted loader workspace/network/Broker access;
- consumer A mutation versus Host canonical body and consumer B.

Controls should mutate one owner/invariant representatively rather than enumerate redundant combinations.

## Metrics

Record body-free identities plus:

```text
source chunk/finalization timestamps
analysis and admission intervals
physical read start/end and bytes
source verification and decode intervals
typed staging/seal interval
logical occurrence/claim timestamp
copy/mapping/compute/teardown intervals
critical-path interval union
physical attempts and logical claims
producer and fresh consumer counts
bytes read/copied/mapped
RSS/PSS where available
page faults/private-COW signal where available
orphaned bytes/work
terminal dispositions
```

Mechanism and authority gates are universal. Economics has no universal-positive threshold. Report every observed cell and do not interpolate unmeasured break-even points.

## Promotion gates

- **P1 shard:** prove whether NumPy is imported before baseline seal; if not, use one profile-owned or research-only trusted preparation seam. Do not broaden `base`.
- **P2/P3 prepared data:** syntax without declaration starts zero work; an exact declaration starts one attempt; original occurrence alone commits one logical claim.
- **P4 extent:** promote only if one sealed body is privately mapped to fresh Guests, mutation is isolated, cleanup is complete and no Host pointer/FD reaches Guest Python. A precise blocker is a valid result.
- **P5 controls:** compare recompute, private copy, private COW and data-local compute without public fanout or coalescing.
- **P6 formal observation:** freeze exact binaries/artifacts/schedule before running; no post-observation matrix edits.

## Parallel ownership after P0

At most two writer lanes may run concurrently in isolated worktrees:

```text
Authority lane:
  runtime or research prepared-data declaration/analysis/claim contracts and focused tests

Runtime lane:
  runtime/engine/wazero package-ready baseline and fixed sealed-extent spike

Harness lane (after one writer lane returns):
  research/prepareddataset, cmd/prepared-data-*, report/checker
```

The main controller owns cross-lane contract changes, integration, roadmap updates, signatures, push and final evidence. Frozen predecessor evidence remains untouched.
