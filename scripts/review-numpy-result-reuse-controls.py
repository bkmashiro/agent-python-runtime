#!/usr/bin/env python3
import hashlib, json, subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONTROL=ROOT/'docs/evidence/numpy-result-reuse-adversarial-controls-v1.json'
EXPECTED={
'reject_object_dtype':'b0b2bc5c6bfe7538df6f33e14782fea7d00c95cbe8cf9207891edf6ad5191d77',
'reject_noncontiguous_view':'e8479e3cb4072440c5e1ac879d7f2b4ac8409b9193027b29f1a2369d0c9a3bf0',
'reject_fortran_order':'e8479e3cb4072440c5e1ac879d7f2b4ac8409b9193027b29f1a2369d0c9a3bf0',
'reject_random_api':'b0b2bc5c6bfe7538df6f33e14782fea7d00c95cbe8cf9207891edf6ad5191d77',
'reject_source_drift':'b0b2bc5c6bfe7538df6f33e14782fea7d00c95cbe8cf9207891edf6ad5191d77',
'reject_corrupt_descriptor':'df5b1b0ba071fbc5bea6081b27a14afef90b97c196eca07f581cf3325339125f',
'reject_cross_profile':'df5b1b0ba071fbc5bea6081b27a14afef90b97c196eca07f581cf3325339125f',
'consumer_mutation_is_private':'a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38'}
FILES={
'b0b2bc5c6bfe7538df6f33e14782fea7d00c95cbe8cf9207891edf6ad5191d77':ROOT/'docs/evidence/numpy-producer-admission-phase6-linux-amd64-private-cow-v1.json',
'e8479e3cb4072440c5e1ac879d7f2b4ac8409b9193027b29f1a2369d0c9a3bf0':ROOT/'runtime/numpyproducer/admission_test.go',
'df5b1b0ba071fbc5bea6081b27a14afef90b97c196eca07f581cf3325339125f':ROOT/'runtime/numpycodec/codec_test.go',
'a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38':ROOT/'docs/evidence/numpy-result-reuse-phase5-linux-amd64-private-cow-v1.json'}
for digest,path in FILES.items(): assert hashlib.sha256(path.read_bytes()).hexdigest()==digest
payload=json.loads(CONTROL.read_text()); rows=payload['controls']; assert len(rows)==len(EXPECTED)
assert {r['id'] for r in rows}==set(EXPECTED)
for row in rows: assert row['passed'] and row['evidence_sha256']=='sha256:'+EXPECTED[row['id']]
subprocess.run(['go','test','./runtime/numpyproducer','./runtime/numpycodec','./runtime/resultblob','-count=1'],cwd=ROOT,check=True)
subprocess.run(['python3','scripts/review-numpy-result-reuse-phase5.py'],cwd=ROOT,check=True)
subprocess.run(['python3','scripts/review-numpy-producer-admission-phase6.py'],cwd=ROOT,check=True)
print('PASS numpy reuse adversarial controls')
