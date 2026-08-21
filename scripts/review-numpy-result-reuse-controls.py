#!/usr/bin/env python3
import hashlib, json, subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONTROL=ROOT/'docs/evidence/numpy-result-reuse-adversarial-controls-v1.json'
EXPECTED={
'reject_object_dtype':'cde85daaf6a1b284e117f336dcb61abaa7c4742a609a99946747443fbc19b1da',
'reject_noncontiguous_view':'c3b43aa740f86765aa53d60f153858b7543f5eb050854418aa7d58f92f8307a5',
'reject_fortran_order':'c3b43aa740f86765aa53d60f153858b7543f5eb050854418aa7d58f92f8307a5',
'reject_random_api':'cde85daaf6a1b284e117f336dcb61abaa7c4742a609a99946747443fbc19b1da',
'reject_source_drift':'cde85daaf6a1b284e117f336dcb61abaa7c4742a609a99946747443fbc19b1da',
'reject_corrupt_descriptor':'289fdacc020e49d4d03fd8321a3ccde947f9428147ab90c675d598c670146c51',
'reject_cross_profile':'289fdacc020e49d4d03fd8321a3ccde947f9428147ab90c675d598c670146c51',
'consumer_mutation_is_private':'a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38'}
FILES={
'cde85daaf6a1b284e117f336dcb61abaa7c4742a609a99946747443fbc19b1da':ROOT/'docs/evidence/numpy-producer-admission-phase6-linux-amd64-private-cow-v1.json',
'c3b43aa740f86765aa53d60f153858b7543f5eb050854418aa7d58f92f8307a5':ROOT/'runtime/numpyproducer/admission_test.go',
'289fdacc020e49d4d03fd8321a3ccde947f9428147ab90c675d598c670146c51':ROOT/'runtime/numpycodec/codec_test.go',
'a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38':ROOT/'docs/evidence/numpy-result-reuse-phase5-linux-amd64-private-cow-v1.json'}
for digest,path in FILES.items(): assert hashlib.sha256(path.read_bytes()).hexdigest()==digest
payload=json.loads(CONTROL.read_text()); rows=payload['controls']; assert len(rows)==len(EXPECTED)
assert {r['id'] for r in rows}==set(EXPECTED)
for row in rows: assert row['passed'] and row['evidence_sha256']=='sha256:'+EXPECTED[row['id']]
subprocess.run(['go','test','./runtime/numpyproducer','./runtime/numpycodec','./runtime/resultblob','-count=1'],cwd=ROOT,check=True)
subprocess.run(['python3','scripts/review-numpy-result-reuse-phase5.py'],cwd=ROOT,check=True)
subprocess.run(['python3','scripts/review-numpy-producer-admission-phase6.py'],cwd=ROOT,check=True)
print('PASS numpy reuse adversarial controls')
