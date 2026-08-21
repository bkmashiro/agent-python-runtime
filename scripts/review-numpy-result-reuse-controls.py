#!/usr/bin/env python3
import hashlib, json, subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONTROL=ROOT/'docs/evidence/numpy-result-reuse-adversarial-controls-v1.json'
EXPECTED={
'reject_object_dtype':'8e683d9cda2635b782e91eef41f80e6861d0c5151b067e6b1327a5bbb0ffe120',
'reject_noncontiguous_view':'109ec01acf78bf727a15637be34362d7133f80d56160fc54732d7464b286cc2c',
'reject_fortran_order':'109ec01acf78bf727a15637be34362d7133f80d56160fc54732d7464b286cc2c',
'reject_random_api':'8e683d9cda2635b782e91eef41f80e6861d0c5151b067e6b1327a5bbb0ffe120',
'reject_source_drift':'8e683d9cda2635b782e91eef41f80e6861d0c5151b067e6b1327a5bbb0ffe120',
'reject_corrupt_descriptor':'00bd8d61c9ef04e9ae9dd36d9e5bccd210bf3dacb8fab4e445c68e24a80735d0',
'reject_cross_profile':'00bd8d61c9ef04e9ae9dd36d9e5bccd210bf3dacb8fab4e445c68e24a80735d0',
'consumer_mutation_is_private':'a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38'}
FILES={
'8e683d9cda2635b782e91eef41f80e6861d0c5151b067e6b1327a5bbb0ffe120':ROOT/'docs/evidence/numpy-producer-admission-phase6-linux-amd64-private-cow-v1.json',
'109ec01acf78bf727a15637be34362d7133f80d56160fc54732d7464b286cc2c':ROOT/'runtime/numpyproducer/admission_test.go',
'00bd8d61c9ef04e9ae9dd36d9e5bccd210bf3dacb8fab4e445c68e24a80735d0':ROOT/'runtime/numpycodec/codec_test.go',
'a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38':ROOT/'docs/evidence/numpy-result-reuse-phase5-linux-amd64-private-cow-v1.json'}
for digest,path in FILES.items(): assert hashlib.sha256(path.read_bytes()).hexdigest()==digest
payload=json.loads(CONTROL.read_text()); rows=payload['controls']; assert len(rows)==len(EXPECTED)
assert {r['id'] for r in rows}==set(EXPECTED)
for row in rows: assert row['passed'] and row['evidence_sha256']=='sha256:'+EXPECTED[row['id']]
subprocess.run(['go','test','./runtime/numpyproducer','./runtime/numpycodec','./runtime/resultblob','-count=1'],cwd=ROOT,check=True)
subprocess.run(['python3','scripts/review-numpy-result-reuse-phase5.py'],cwd=ROOT,check=True)
subprocess.run(['python3','scripts/review-numpy-producer-admission-phase6.py'],cwd=ROOT,check=True)
print('PASS numpy reuse adversarial controls')
