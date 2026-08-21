#!/usr/bin/env python3
import hashlib, json, subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONTROL=ROOT/'docs/evidence/numpy-result-reuse-adversarial-controls-v1.json'
EXPECTED={
'reject_object_dtype':'f9840f4e9be0127d85446e200b70d0ea03868a0e56b9d437148711aee1d15330',
'reject_noncontiguous_view':'c3b43aa740f86765aa53d60f153858b7543f5eb050854418aa7d58f92f8307a5',
'reject_fortran_order':'c3b43aa740f86765aa53d60f153858b7543f5eb050854418aa7d58f92f8307a5',
'reject_random_api':'f9840f4e9be0127d85446e200b70d0ea03868a0e56b9d437148711aee1d15330',
'reject_source_drift':'f9840f4e9be0127d85446e200b70d0ea03868a0e56b9d437148711aee1d15330',
'reject_corrupt_descriptor':'29955d362411a30535f78a36684ec57b1c0639163d79ba20130988aab2d7a76d',
'reject_cross_profile':'29955d362411a30535f78a36684ec57b1c0639163d79ba20130988aab2d7a76d',
'consumer_mutation_is_private':'a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38'}
FILES={
'f9840f4e9be0127d85446e200b70d0ea03868a0e56b9d437148711aee1d15330':ROOT/'docs/evidence/numpy-producer-admission-phase6-linux-amd64-private-cow-v1.json',
'c3b43aa740f86765aa53d60f153858b7543f5eb050854418aa7d58f92f8307a5':ROOT/'runtime/numpyproducer/admission_test.go',
'29955d362411a30535f78a36684ec57b1c0639163d79ba20130988aab2d7a76d':ROOT/'runtime/numpycodec/codec_test.go',
'a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38':ROOT/'docs/evidence/numpy-result-reuse-phase5-linux-amd64-private-cow-v1.json'}
for digest,path in FILES.items(): assert hashlib.sha256(path.read_bytes()).hexdigest()==digest
payload=json.loads(CONTROL.read_text()); rows=payload['controls']; assert len(rows)==len(EXPECTED)
assert {r['id'] for r in rows}==set(EXPECTED)
for row in rows: assert row['passed'] and row['evidence_sha256']=='sha256:'+EXPECTED[row['id']]
subprocess.run(['go','test','./runtime/numpyproducer','./runtime/numpycodec','./runtime/resultblob','-count=1'],cwd=ROOT,check=True)
subprocess.run(['python3','scripts/review-numpy-result-reuse-phase5.py'],cwd=ROOT,check=True)
subprocess.run(['python3','scripts/review-numpy-producer-admission-phase6.py'],cwd=ROOT,check=True)
print('PASS numpy reuse adversarial controls')
